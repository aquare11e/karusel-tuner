package epg

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func serveBody(t *testing.T, status int, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

var testDay = time.Date(2026, 10, 30, 0, 0, 0, 0, Moscow)

func TestFetchDayParsesSchedule(t *testing.T) {
	fixture, err := os.ReadFile("testdata/day.json")
	if err != nil {
		t.Fatal(err)
	}
	var path, ua string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path, ua = r.URL.Path, r.Header.Get("User-Agent")
		_, _ = w.Write(fixture)
	}))
	defer srv.Close()

	got, err := FetchDay(context.Background(), srv.Client(), srv.URL+"/schedule/Europe/Moscow", testDay)
	if err != nil {
		t.Fatal(err)
	}
	if path != "/schedule/Europe/Moscow/2026/10/30/nearest_events.json" {
		t.Errorf("path = %q", path)
	}
	if ua != UserAgent {
		t.Errorf("User-Agent = %q", ua)
	}
	if len(got) != 3 {
		t.Fatalf("len = %d, want 3", len(got))
	}
	first := got[0]
	if first.Title != "Смешарики" || first.Link != "/announce/8451-smeshariki" {
		t.Errorf("first = %+v", first)
	}
	if !first.Start.Equal(time.Unix(1793325600, 0)) || !first.Stop.Equal(time.Unix(1793332800, 0)) {
		t.Errorf("first times = %v..%v", first.Start, first.Stop)
	}
}

func TestFetchDayEmptyMeansBeyondHorizon(t *testing.T) {
	srv := serveBody(t, http.StatusOK, "[]")
	got, err := FetchDay(context.Background(), srv.Client(), srv.URL, testDay)
	if err != nil || len(got) != 0 {
		t.Fatalf("got %v, err %v", got, err)
	}
}

func TestFetchDayRejectsNonJSON(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
	}{
		{"html", http.StatusOK, "<!DOCTYPE html><html>"},
		{"server error", http.StatusInternalServerError, "[]"},
		{"too large", http.StatusOK, "[" + strings.Repeat(" ", 1<<20) + "]"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := serveBody(t, tc.status, tc.body)
			if _, err := FetchDay(context.Background(), srv.Client(), srv.URL, testDay); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

func TestFetchDaySkipsInvalidEntries(t *testing.T) {
	srv := serveBody(t, http.StatusOK, `[
		{"link":"/a","title":"  ","announce":"","start_at":1,"end_at":2},
		{"link":"/b","title":"X","announce":"","start_at":5,"end_at":5},
		{"link":"/c","title":"OK","announce":"","start_at":1,"end_at":2}
	]`)
	got, err := FetchDay(context.Background(), srv.Client(), srv.URL, testDay)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Title != "OK" {
		t.Fatalf("got %+v", got)
	}
}

func TestMergeDedupesAndClipsOverlaps(t *testing.T) {
	t0 := time.Unix(1793325600, 0)
	at := func(m int) time.Time { return t0.Add(time.Duration(m) * time.Minute) }
	a := Programme{Title: "A", Link: "/a", Start: at(0), Stop: at(60)}
	b := Programme{Title: "B", Link: "/b", Start: at(60), Stop: at(120)}
	c := Programme{Title: "C", Link: "/c", Start: at(90), Stop: at(150)}
	d := Programme{Title: "D", Link: "/d", Start: at(120), Stop: at(180)}

	got := Merge([]Programme{b, a}, []Programme{b, c, d})
	var titles []string
	for _, p := range got {
		titles = append(titles, p.Title)
	}
	if strings.Join(titles, ",") != "A,B,C,D" {
		t.Fatalf("titles = %v", titles)
	}
	// Overlaps clip the earlier programme instead of dropping the later one.
	if !got[1].Stop.Equal(at(90)) || !got[2].Stop.Equal(at(120)) {
		t.Errorf("clipped stops = %v, %v", got[1].Stop, got[2].Stop)
	}

	// One malformed long entry must not swallow the programmes after it.
	long := Programme{Title: "Long", Link: "/l", Start: at(0), Stop: at(600)}
	next := Programme{Title: "Next", Link: "/n", Start: at(60), Stop: at(120)}
	got = Merge([]Programme{long, next})
	if len(got) != 2 || !got[0].Stop.Equal(at(60)) || got[1].Title != "Next" {
		t.Errorf("long entry merge = %+v", got)
	}
}

func TestFetchDayNetworkError(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close()
	if _, err := FetchDay(context.Background(), http.DefaultClient, url, testDay); err == nil {
		t.Fatal("expected error from unreachable schedule host")
	}
}
