package relay

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const fakePlayerURL = "https://player.mediavitrina.ru/karusel/test/player.html"

// fakePlayerPage mirrors the shape of the real player: the streams API URL
// sits in an inline remoteConfig object with template placeholders.
const fakePlayerPage = `<html><script>
const remoteConfig = {
    vitrina_slug: "test",
    streams_api_v2_url: 'https://media.mediavitrina.ru/balancer/v3/karusel/karusel/streams.json?application_id={{APPLICATION_ID}}&player_referer_hostname={{PLAYER_REFERER_HOSTNAME}}&config_checksum_sha256={{CONFIG_CHECKSUM_SHA256}}&egress_version_id=1',
    stream_formats_order: ['mpdp', 'hlsp', 'hls'],
};
</script></html>`

// toServer sends every request to srv, keeping path and query, so the
// resolver can use real MediaVitrina URLs against a local fake.
type toServer struct{ srv *httptest.Server }

func (t toServer) RoundTrip(r *http.Request) (*http.Response, error) {
	target, err := url.Parse(t.srv.URL)
	if err != nil {
		return nil, err
	}
	r = r.Clone(r.Context())
	r.URL.Scheme, r.URL.Host = target.Scheme, target.Host
	return http.DefaultTransport.RoundTrip(r)
}

// fakeVitrina serves the player page and a streams API that, like the real
// one, rejects requests without the player's Referer.
func fakeVitrina(t *testing.T, page string, streams func() (int, string)) *http.Client {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /karusel/test/player.html", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, page)
	})
	mux.HandleFunc("GET /balancer/v3/karusel/karusel/streams.json", func(w http.ResponseWriter, r *http.Request) {
		if r.Referer() != fakePlayerURL {
			http.Error(w, "missing referer", http.StatusConflict)
			return
		}
		q := r.URL.Query()
		if q.Get("player_referer_hostname") != "player.mediavitrina.ru" || q.Get("application_id") != "" || strings.Contains(r.URL.RawQuery, "{{") {
			http.Error(w, "unfilled placeholders: "+r.URL.RawQuery, http.StatusBadRequest)
			return
		}
		code, body := streams()
		w.WriteHeader(code)
		_, _ = io.WriteString(w, body)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return &http.Client{Transport: toServer{srv}}
}

func TestResolveMediaVitrina(t *testing.T) {
	cases := []struct {
		name    string
		page    string
		code    int
		streams string
		want    string
		wantErr string
	}{
		{
			name:    "first plain hls",
			page:    fakePlayerPage,
			code:    200,
			streams: `{"country":"RU","hls":["https://edge1.example/hls-live/karuselhd/index.m3u8?token=t1","https://edge2.example/hls-live/karuselhd/index.m3u8?token=t2"],"hlsp":["https://edge1.example/hlsp-live/karuselhd/index.m3u8?token=p"]}`,
			want:    "https://edge1.example/hls-live/karuselhd/index.m3u8?token=t1",
		},
		{
			name:    "drm only",
			page:    fakePlayerPage,
			code:    200,
			streams: `{"country":"RU","hls":[],"hlsp":["https://edge1.example/hlsp-live/karuselhd/index.m3u8?token=p"],"mpdp":["https://edge1.example/dashp-live/karuselhd/index.mpd?token=p"]}`,
			wantErr: "no unprotected HLS",
		},
		{
			name:    "geo placeholder outside Russia",
			page:    fakePlayerPage,
			code:    200,
			streams: `{"country":"XX","hls":["https://edge1.example/hls-vod/streams/georestrictions/karusel_16x9/index.m3u8"]}`,
			wantErr: "geo-restricted",
		},
		{
			name:    "api rejects request",
			page:    fakePlayerPage,
			code:    409,
			streams: `{"error":"conflict"}`,
			wantErr: "HTTP 409",
		},
		{
			name:    "player page changed",
			page:    "<html>player moved</html>",
			code:    200,
			wantErr: "streams API URL not found",
		},
		{
			name:    "insecure stream url",
			page:    fakePlayerPage,
			code:    200,
			streams: `{"country":"RU","hls":["http://edge1.example/hls-live/karuselhd/index.m3u8"]}`,
			wantErr: "not an https URL",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client := fakeVitrina(t, tc.page, func() (int, string) { return tc.code, tc.streams })
			got, err := resolveMediaVitrina(context.Background(), client, fakePlayerURL)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want containing %q (got url %q)", err, tc.wantErr, got)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("got %q, %v; want %q", got, err, tc.want)
			}
		})
	}
}

func TestIsMediaVitrinaPlayer(t *testing.T) {
	cases := map[string]bool{
		DefaultSourceURL: true,
		"https://player.mediavitrina.ru/karusel/x/y/player.html":            true,
		"https://player.mediavitrina.ru.evil.example/karusel/x/player.html": false,
		"https://edge02r.mediavitrina.ru/hls-livef7/karuselhd/index.m3u8":   false,
		"https://player.mediavitrina.ru/karusel/x/player.html.m3u8":         false,
		"https://example.invalid/live?x=1;echo-injection":                   false,
	}
	for raw, want := range cases {
		if got := isMediaVitrinaPlayer(raw); got != want {
			t.Errorf("isMediaVitrinaPlayer(%q) = %v, want %v", raw, got, want)
		}
	}
}

func TestLiveResolvesFreshMediaVitrinaStreamPerAttempt(t *testing.T) {
	var calls atomic.Int32
	client := fakeVitrina(t, fakePlayerPage, func() (int, string) {
		return 200, fmt.Sprintf(`{"country":"RU","hls":["https://edge1.example/hls-live/karuselhd/index.m3u8?token=%d"]}`, calls.Add(1))
	})
	dir := t.TempDir()
	// $4 is the input argument after "--stdout --loglevel warning".
	cfg := testConfig(t, countingScript(dir, `if [ "$n" -lt 2 ]; then printf '%s|' "$4"; exit 0; fi; exit 1`))
	cfg.SourceURL = fakePlayerURL
	cfg.ReconnectWindow = 300 * time.Millisecond
	srv := newRestartServer(t, context.Background(), cfg, func(h *Handler) { h.client = client })

	resp, err := srv.Client().Get(srv.URL + "/live.ts")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	want := "hls://https://edge1.example/hls-live/karuselhd/index.m3u8?token=1|" +
		"hls://https://edge1.example/hls-live/karuselhd/index.m3u8?token=2|"
	if string(body) != want {
		t.Fatalf("body = %q, want %q", body, want)
	}
}

func TestLiveResolveCountsTowardsStartupTimeout(t *testing.T) {
	release := make(chan struct{})
	mux := http.NewServeMux()
	mux.HandleFunc("GET /karusel/test/player.html", func(_ http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	})
	fake := httptest.NewServer(mux)
	t.Cleanup(fake.Close)
	t.Cleanup(func() { close(release) }) // runs before fake.Close

	dir := t.TempDir()
	cfg := testConfig(t, countingScript(dir, "printf video"))
	cfg.SourceURL = fakePlayerURL
	cfg.StartupTimeout = 100 * time.Millisecond
	srv := newRestartServer(t, context.Background(), cfg, func(h *Handler) {
		h.client = &http.Client{Transport: toServer{fake}}
	})

	started := time.Now()
	resp, err := srv.Client().Get(srv.URL + "/live.ts")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", resp.StatusCode)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("stalled player page held the request for %s", elapsed)
	}
	if n := runs(t, dir); n != 0 {
		t.Fatalf("streamlink ran %d times without a resolved stream", n)
	}
}
