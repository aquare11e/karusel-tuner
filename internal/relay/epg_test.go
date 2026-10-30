package relay

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/aquare11e/karusel-tuner/internal/epg"
)

type fakeGuide struct {
	progs  []epg.Programme
	status epg.Status
}

func (f fakeGuide) Programmes() []epg.Programme { return f.progs }
func (f fakeGuide) Status() epg.Status          { return f.status }

func newGuideServer(t *testing.T, guide GuideSource) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(NewHandler(context.Background(), testConfig(t, "exit 0"), guide, log.New(io.Discard, "", 0)))
	t.Cleanup(srv.Close)
	return srv
}

func TestEPGServesXMLTV(t *testing.T) {
	srv := newGuideServer(t, fakeGuide{progs: []epg.Programme{{
		Title: "Спокойной ночи, малыши!", Start: time.Unix(1793383200, 0), Stop: time.Unix(1793384400, 0),
	}}})
	resp, err := srv.Client().Get(srv.URL + "/epg.xml")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/xml; charset=utf-8" {
		t.Errorf("Content-Type = %q", ct)
	}
	if !strings.Contains(string(body), `<channel id="1">`) || !strings.Contains(string(body), "Спокойной ночи, малыши!") {
		t.Errorf("body:\n%s", body)
	}
}

func TestEPGUnavailableBeforeFirstLoad(t *testing.T) {
	srv := newGuideServer(t, fakeGuide{})
	resp, err := srv.Client().Get(srv.URL + "/epg.xml")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable || resp.Header.Get("Retry-After") != "60" {
		t.Fatalf("status = %d, Retry-After = %q", resp.StatusCode, resp.Header.Get("Retry-After"))
	}
}

func getStatus(t *testing.T, srv *httptest.Server) map[string]any {
	t.Helper()
	resp, err := srv.Client().Get(srv.URL + "/status")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var st map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&st); err != nil {
		t.Fatal(err)
	}
	return st
}

func TestStatusIncludesEPG(t *testing.T) {
	srv := newGuideServer(t, fakeGuide{status: epg.Status{
		Programmes: 4, LastSuccess: time.Date(2026, 10, 30, 9, 0, 0, 0, time.UTC), LastError: "boom",
	}})
	st := getStatus(t, srv)
	for _, key := range []string{"active_viewers", "max_viewers", "uptime_seconds"} {
		if _, ok := st[key]; !ok {
			t.Errorf("status lacks %q: %v", key, st)
		}
	}
	e, _ := st["epg"].(map[string]any)
	if e["programmes"] != float64(4) || e["last_success"] != "2026-10-30T09:00:00Z" || e["last_error"] != "boom" {
		t.Errorf("epg = %v", e)
	}

	never := getStatus(t, newGuideServer(t, fakeGuide{}))
	if e, _ := never["epg"].(map[string]any); e["last_success"] != "" {
		t.Errorf("last_success before first load = %v", e["last_success"])
	}
}
