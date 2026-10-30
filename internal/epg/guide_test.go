package epg

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

var discardLogger = log.New(io.Discard, "", 0)

// fakeSchedule serves one programme (05:00–06:00 MSK) for 2026-10-29..2026-11-01
// and an empty day afterwards, mimicking the site's ~10-day horizon.
type fakeSchedule struct {
	requests  atomic.Int64
	fail      atomic.Bool
	failFirst bool
}

func (f *fakeSchedule) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	n := f.requests.Add(1)
	if f.fail.Load() || (f.failFirst && n == 1) {
		http.Error(w, "boom", http.StatusInternalServerError)
		return
	}
	parts := strings.Split(strings.TrimSuffix(r.URL.Path, "/nearest_events.json"), "/")
	day, err := time.ParseInLocation("2006/01/02", strings.Join(parts[len(parts)-3:], "/"), Moscow)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	first := time.Date(2026, 10, 29, 0, 0, 0, 0, Moscow)
	last := time.Date(2026, 11, 1, 0, 0, 0, 0, Moscow)
	if day.Before(first) || day.After(last) {
		_, _ = io.WriteString(w, "[]")
		return
	}
	start := day.Add(5 * time.Hour)
	_, _ = fmt.Fprintf(w, `[{"link":"/announce/%s","title":"Show","announce":"","start_at":%d,"end_at":%d}]`,
		day.Format("20060102"), start.Unix(), start.Add(time.Hour).Unix())
}

func newFakeGuide(t *testing.T, f *fakeSchedule) *Guide {
	t.Helper()
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	return NewGuide(srv.Client(), srv.URL+"/schedule/Europe/Moscow", 12, discardLogger)
}

var guideNow = time.Date(2026, 10, 30, 12, 0, 0, 0, Moscow)

func TestGuideRefreshStopsAtHorizon(t *testing.T) {
	f := &fakeSchedule{}
	g := newFakeGuide(t, f)
	if err := g.Refresh(context.Background(), guideNow); err != nil {
		t.Fatal(err)
	}
	if got := f.requests.Load(); got != 5 {
		t.Errorf("requests = %d, want 5 (29.10–02.11)", got)
	}
	if got := len(g.Programmes()); got != 4 {
		t.Errorf("programmes = %d, want 4", got)
	}
	want := Status{Programmes: 4, LastSuccess: guideNow}
	if got := g.Status(); !got.LastSuccess.Equal(want.LastSuccess) || got.Programmes != want.Programmes || got.LastError != "" {
		t.Errorf("status = %+v, want %+v", got, want)
	}
}

func TestGuideRefreshErrorKeepsCache(t *testing.T) {
	f := &fakeSchedule{}
	g := newFakeGuide(t, f)
	if err := g.Refresh(context.Background(), guideNow); err != nil {
		t.Fatal(err)
	}
	f.fail.Store(true)
	if err := g.Refresh(context.Background(), guideNow.Add(time.Hour)); err == nil {
		t.Fatal("expected refresh error")
	}
	if got := len(g.Programmes()); got != 4 {
		t.Errorf("programmes = %d, want cached 4", got)
	}
	st := g.Status()
	if !st.LastSuccess.Equal(guideNow) {
		t.Errorf("LastSuccess = %v, want %v", st.LastSuccess, guideNow)
	}
	if !strings.Contains(st.LastError, "500") {
		t.Errorf("LastError = %q, want mention of 500", st.LastError)
	}
}

func TestRetryDelayDoublesAndCaps(t *testing.T) {
	cases := []struct {
		failures int
		want     time.Duration
	}{
		{1, time.Minute},
		{2, 2 * time.Minute},
		{4, 8 * time.Minute},
		{9, 256 * time.Minute},
		{10, 6 * time.Hour}, // 512m exceeds the cap
		{100, 6 * time.Hour},
	}
	for _, tc := range cases {
		if got := retryDelay(time.Minute, 6*time.Hour, tc.failures); got != tc.want {
			t.Errorf("retryDelay(%d) = %v, want %v", tc.failures, got, tc.want)
		}
	}
}

func TestGuideNextDelay(t *testing.T) {
	g := NewGuide(http.DefaultClient, "http://unused", 12, discardLogger)
	cases := []struct {
		every    time.Duration
		failures int
		want     time.Duration
	}{
		{6 * time.Hour, 0, 6 * time.Hour},
		{6 * time.Hour, 1, time.Minute},
		{6 * time.Hour, 100, 30 * time.Minute}, // failures retry well before the refresh interval
		{10 * time.Minute, 100, 10 * time.Minute},
	}
	for _, tc := range cases {
		if got := g.nextDelay(tc.every, tc.failures); got != tc.want {
			t.Errorf("nextDelay(%v, %d) = %v, want %v", tc.every, tc.failures, got, tc.want)
		}
	}
}

func TestGuideRunRetriesSoonAfterFailure(t *testing.T) {
	f := &fakeSchedule{failFirst: true}
	g := newFakeGuide(t, f)
	g.retryMin = 10 * time.Millisecond
	g.now = func() time.Time { return guideNow }
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go g.Run(ctx, time.Hour)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if len(g.Programmes()) > 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("guide did not retry soon after the first failure")
}
