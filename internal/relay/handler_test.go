package relay

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func fakeCLI(t *testing.T, script string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "streamlink")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+script+"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	return path
}

func testConfig(t *testing.T, script string) Config {
	t.Helper()
	c, err := LoadConfig(envWith(map[string]string{
		"STREAMLINK_BIN": fakeCLI(t, script),
		// Slot tests (busy/503) assume a single tuner.
		"MAX_VIEWERS": "1",
		// The default player URL is resolved over the network first; unit
		// tests must stay offline.
		"SOURCE_URL": "https://example.invalid/live",
		// macOS vets a freshly written executable on first exec (~0.3s);
		// a tighter timeout makes healthy fakes look like stalled upstreams.
		"STARTUP_TIMEOUT": "3s",
		// Upstream EOF now triggers restarts within the same response; keep
		// the window short so tests never hold a response for minutes.
		"RECONNECT_WINDOW": "1s",
	}))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestHealthPlaylistAndStatus(t *testing.T) {
	cfg := testConfig(t, "printf 'unused'")
	server := httptest.NewServer(NewHandler(context.Background(), cfg, fakeGuide{}, log.New(io.Discard, "", 0)))
	defer server.Close()

	response, err := server.Client().Get(server.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if response.StatusCode != 200 || string(body) != "ok\n" {
		t.Fatalf("health: %d %q", response.StatusCode, body)
	}

	response, err = server.Client().Get(server.URL + "/playlist.m3u")
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(response.Body)
	_ = response.Body.Close()
	if response.StatusCode != 200 || !strings.Contains(string(body), "Карусель") || !strings.Contains(string(body), "http://127.0.0.1:18080/live.ts") {
		t.Fatalf("playlist: %d %q", response.StatusCode, body)
	}

	response, err = server.Client().Get(server.URL + "/status")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var status struct {
		Active int64 `json:"active_viewers"`
		Max    int   `json:"max_viewers"`
	}
	if err := json.NewDecoder(response.Body).Decode(&status); err != nil {
		t.Fatal(err)
	}
	if status.Active != 0 || status.Max != 1 {
		t.Fatalf("status: %+v", status)
	}
}

func TestLiveStreamsCommandOutput(t *testing.T) {
	cfg := testConfig(t, "printf 'mock-mpeg-ts-data'")
	server := httptest.NewServer(NewHandler(context.Background(), cfg, fakeGuide{}, log.New(io.Discard, "", 0)))
	defer server.Close()
	response, err := server.Client().Get(server.URL + "/live.ts")
	if err != nil {
		t.Fatal(err)
	}
	// The fake exits after writing, so the relay restarts it and the bytes
	// repeat; the first chunk must arrive unchanged.
	body := make([]byte, len("mock-mpeg-ts-data"))
	_, err = io.ReadFull(response.Body, body)
	_ = response.Body.Close()
	if err != nil || response.StatusCode != 200 || string(body) != "mock-mpeg-ts-data" {
		t.Fatalf("live: code=%d body=%q err=%v", response.StatusCode, body, err)
	}
	if response.Header.Get("Content-Type") != "video/mp2t" {
		t.Fatal("content-type mismatch")
	}
}

func TestLiveStartupTimeout(t *testing.T) {
	cfg := testConfig(t, "sleep 2; printf 'too late'")
	cfg.StartupTimeout = 100 * time.Millisecond
	server := httptest.NewServer(NewHandler(context.Background(), cfg, fakeGuide{}, log.New(io.Discard, "", 0)))
	defer server.Close()
	started := time.Now()
	response, err := server.Client().Get(server.URL + "/live.ts")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d", response.StatusCode)
	}
	if time.Since(started) > time.Second {
		t.Fatalf("startup timeout blocked for %s", time.Since(started))
	}
}

func TestBusySlotAndRelease(t *testing.T) {
	cfg := testConfig(t, "printf 'first'; sleep 2")
	server := httptest.NewServer(NewHandler(context.Background(), cfg, fakeGuide{}, log.New(io.Discard, "", 0)))
	defer server.Close()

	r1, err := server.Client().Get(server.URL + "/live.ts")
	if err != nil {
		t.Fatal(err)
	}
	defer r1.Body.Close()
	r2, err := server.Client().Get(server.URL + "/live.ts")
	if err != nil {
		t.Fatal(err)
	}
	_ = r2.Body.Close()
	if r2.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", r2.StatusCode)
	}
	_ = r1.Body.Close()
}

func TestHeadDoesNotOpenStreamlink(t *testing.T) {
	cfg := testConfig(t, "exit 77")
	server := httptest.NewServer(NewHandler(context.Background(), cfg, fakeGuide{}, log.New(io.Discard, "", 0)))
	defer server.Close()
	req, _ := http.NewRequest(http.MethodHead, server.URL+"/live.ts", nil)
	response, err := server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatalf("head returned %d", response.StatusCode)
	}
}

func TestViewerDisconnectReleasesSlot(t *testing.T) {
	cfg := testConfig(t, "while :; do printf 'packet'; sleep 0.05; done")
	server := httptest.NewServer(NewHandler(context.Background(), cfg, fakeGuide{}, log.New(io.Discard, "", 0)))
	defer server.Close()

	response, err := server.Client().Get(server.URL + "/live.ts")
	if err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 6)
	if _, err := io.ReadFull(response.Body, buf); err != nil {
		t.Fatal(err)
	}
	if err := response.Body.Close(); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		r, err := server.Client().Get(server.URL + "/status")
		if err != nil {
			t.Fatal(err)
		}
		var status struct {
			Active int64 `json:"active_viewers"`
		}
		decErr := json.NewDecoder(r.Body).Decode(&status)
		_ = r.Body.Close()
		if decErr != nil {
			t.Fatal(decErr)
		}
		if status.Active == 0 {
			return
		}
		time.Sleep(15 * time.Millisecond)
	}
	t.Fatal("slot not released after viewer disconnect")
}

func TestSourceURLIsArgNotShell(t *testing.T) {
	cfg := testConfig(t, "printf '%s' \"$4\"")
	cfg.SourceURL = "https://example.invalid/live?x=1;echo-injection"
	server := httptest.NewServer(NewHandler(context.Background(), cfg, fakeGuide{}, log.New(io.Discard, "", 0)))
	defer server.Close()
	response, err := server.Client().Get(server.URL + "/live.ts")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body := make([]byte, len(cfg.SourceURL))
	_, err = io.ReadFull(response.Body, body)
	if err != nil || string(body) != cfg.SourceURL {
		t.Fatalf("argument not passed literally, got %q, err %v", body, err)
	}
}
