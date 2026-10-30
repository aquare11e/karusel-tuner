package relay

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// countingScript prefixes body with a counter so each invocation sees its
// zero-based run number in $n; runs(dir) reports how many times it ran.
func countingScript(dir, body string) string {
	return fmt.Sprintf("f=%s; n=$(cat \"$f\" 2>/dev/null || echo 0); echo $((n+1)) > \"$f\"\n%s",
		filepath.Join(dir, "count"), body)
}

func runs(t *testing.T, dir string) int {
	t.Helper()
	b, _ := os.ReadFile(filepath.Join(dir, "count"))
	n, _ := strconv.Atoi(strings.TrimSpace(string(b)))
	return n
}

func newRestartServer(t *testing.T, ctx context.Context, cfg Config, tune func(*Handler)) *httptest.Server {
	t.Helper()
	h := newHandler(ctx, cfg, fakeGuide{}, log.New(io.Discard, "", 0))
	h.backoffMin = 10 * time.Millisecond
	if tune != nil {
		tune(h)
	}
	srv := httptest.NewServer(h.routes())
	t.Cleanup(srv.Close)
	return srv
}

func waitFor(t *testing.T, within time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(15 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func readPIDs(t *testing.T, path string) []int {
	t.Helper()
	b, _ := os.ReadFile(path)
	var pids []int
	for _, f := range strings.Fields(string(b)) {
		pid, err := strconv.Atoi(f)
		if err != nil {
			t.Fatal(err)
		}
		pids = append(pids, pid)
	}
	return pids
}

func gone(pid int) bool {
	return errors.Is(syscall.Kill(pid, 0), syscall.ESRCH)
}

func TestLiveRestartsUpstreamWithinResponse(t *testing.T) {
	dir := t.TempDir()
	cfg := testConfig(t, countingScript(dir, `if [ "$n" -lt 2 ]; then printf "part$n"; exit 0; fi; exit 1`))
	cfg.ReconnectWindow = 300 * time.Millisecond
	srv := newRestartServer(t, context.Background(), cfg, nil)

	resp, err := srv.Client().Get(srv.URL + "/live.ts")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if string(body) != "part0part1" {
		t.Fatalf("body = %q, want part0part1", body)
	}
	waitFor(t, time.Second, "slot release", func() bool {
		st := getStatus(t, srv)
		return st["active_viewers"] == float64(0) && st["upstream_restarts"] == float64(1)
	})
}

func TestLiveGivesUpAfterReconnectWindow(t *testing.T) {
	dir := t.TempDir()
	cfg := testConfig(t, countingScript(dir, `if [ "$n" -eq 0 ]; then printf a; exit 0; fi; exit 1`))
	cfg.ReconnectWindow = 200 * time.Millisecond
	srv := newRestartServer(t, context.Background(), cfg, func(h *Handler) { h.backoffMin = 50 * time.Millisecond })

	started := time.Now()
	resp, err := srv.Client().Get(srv.URL + "/live.ts")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Errorf("response closed after %s, want < 2s", elapsed)
	}
	if string(body) != "a" {
		t.Errorf("body = %q, want a", body)
	}
	if n := runs(t, dir); n > 5 {
		t.Errorf("streamlink ran %d times, want <= 5 (no hot loop)", n)
	}
}

func TestDisconnectDuringReconnectKillsUpstream(t *testing.T) {
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "pids")
	cfg := testConfig(t, countingScript(dir,
		fmt.Sprintf(`if [ "$n" -eq 0 ]; then printf x; exit 0; fi; echo $$ >> %s; exec sleep 5`, pidFile)))
	cfg.ReconnectWindow = 5 * time.Second
	srv := newRestartServer(t, context.Background(), cfg, nil)

	resp, err := srv.Client().Get(srv.URL + "/live.ts")
	if err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 1)
	if _, err := io.ReadFull(resp.Body, buf); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 2*time.Second, "restart attempt", func() bool { return len(readPIDs(t, pidFile)) > 0 })
	_ = resp.Body.Close()

	waitFor(t, 2*time.Second, "slot release and dead helpers", func() bool {
		if getStatus(t, srv)["active_viewers"] != float64(0) {
			return false
		}
		for _, pid := range readPIDs(t, pidFile) {
			if !gone(pid) {
				return false
			}
		}
		return true
	})
}

func TestAppShutdownStopsUpstream(t *testing.T) {
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "pid")
	cfg := testConfig(t, fmt.Sprintf(`echo $$ > %s; while :; do printf p; sleep 0.05; done`, pidFile))
	appCtx, cancelApp := context.WithCancel(context.Background())
	defer cancelApp()
	srv := newRestartServer(t, appCtx, cfg, nil)

	resp, err := srv.Client().Get(srv.URL + "/live.ts")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	buf := make([]byte, 1)
	if _, err := io.ReadFull(resp.Body, buf); err != nil {
		t.Fatal(err)
	}
	cancelApp()

	done := make(chan struct{})
	go func() { _, _ = io.Copy(io.Discard, resp.Body); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("response still open after app shutdown")
	}
	pids := readPIDs(t, pidFile)
	if len(pids) != 1 {
		t.Fatalf("pids = %v", pids)
	}
	waitFor(t, time.Second, "streamlink reaped", func() bool { return gone(pids[0]) })
}

func TestDisconnectDuringPauseReleasesSlot(t *testing.T) {
	dir := t.TempDir()
	cfg := testConfig(t, countingScript(dir, `printf x; exit 0`))
	cfg.ReconnectWindow = time.Minute
	srv := newRestartServer(t, context.Background(), cfg, func(h *Handler) { h.backoffMin = 5 * time.Second })

	resp, err := srv.Client().Get(srv.URL + "/live.ts")
	if err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 1)
	if _, err := io.ReadFull(resp.Body, buf); err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()

	waitFor(t, time.Second, "slot release during reconnect pause", func() bool {
		return getStatus(t, srv)["active_viewers"] == float64(0)
	})
	if n := runs(t, dir); n != 1 {
		t.Errorf("streamlink ran %d times, want 1 (viewer left during the pause)", n)
	}
}

type syncBuffer struct {
	mu  sync.Mutex
	buf strings.Builder
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func TestUpstreamEndLogReportsExitStatus(t *testing.T) {
	dir := t.TempDir()
	cfg := testConfig(t, countingScript(dir, `if [ "$n" -eq 0 ]; then printf a; exit 0; fi; exit 3`))
	cfg.ReconnectWindow = 100 * time.Millisecond
	var logs syncBuffer
	h := newHandler(context.Background(), cfg, fakeGuide{}, log.New(&logs, "", 0))
	h.backoffMin = 10 * time.Millisecond
	srv := httptest.NewServer(h.routes())
	defer srv.Close()

	resp, err := srv.Client().Get(srv.URL + "/live.ts")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.ReadAll(resp.Body)
	_ = resp.Body.Close()

	waitFor(t, time.Second, "give-up log", func() bool { return strings.Contains(logs.String(), "did not recover") })
	out := logs.String()
	if !strings.Contains(out, "upstream ended (exit status 0)") {
		t.Errorf("end of stream should report the real exit status:\n%s", out)
	}
	if !strings.Contains(out, "exit status 3") {
		t.Errorf("failed restart should report the real exit status:\n%s", out)
	}
}

func TestFlappingUpstreamBacksOff(t *testing.T) {
	dir := t.TempDir()
	// Every run yields a byte and dies: a source that keeps flapping.
	cfg := testConfig(t, countingScript(dir, `printf a; exit 0`))
	cfg.ReconnectWindow = time.Minute
	srv := newRestartServer(t, context.Background(), cfg, func(h *Handler) {
		h.backoffMin = 20 * time.Millisecond
		h.backoffMax = 10 * time.Second // runs far shorter than this count as flapping
	})

	resp, err := srv.Client().Get(srv.URL + "/live.ts")
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Second)
	_ = resp.Body.Close()
	// Growing pauses 20+40+80+160+320ms fit ~6 runs into a second;
	// resetting to 20ms after every short run would allow ~15.
	if n := runs(t, dir); n > 8 {
		t.Errorf("streamlink ran %d times in 1s, want <= 8 (pauses must keep growing)", n)
	}
}

func TestStreamlinkStderrIsRedacted(t *testing.T) {
	cfg := testConfig(t, `echo "error: Unable to open URL: $4 (403 Client Error)" >&2; exit 1`)
	cfg.SourceURL = "https://example.invalid/live.m3u8?token=SECRET&e=1"
	var stderr syncBuffer
	srv := newRestartServer(t, context.Background(), cfg, func(h *Handler) { h.stderr = &stderr })

	resp, err := srv.Client().Get(srv.URL + "/live.ts")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	waitFor(t, time.Second, "streamlink stderr", func() bool { return strings.Contains(stderr.String(), "Unable to open URL") })
	out := stderr.String()
	if strings.Contains(out, "SECRET") {
		t.Fatalf("token leaked to logs: %q", out)
	}
	if !strings.Contains(out, "https://example.invalid/live.m3u8?<redacted>") {
		t.Errorf("URL host/path should stay for diagnosis: %q", out)
	}
}
