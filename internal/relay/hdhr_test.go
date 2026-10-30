package relay

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/aquare11e/karusel-tuner/internal/epg"
)

func newTunerServer(t *testing.T) *httptest.Server {
	t.Helper()
	cfg, err := LoadConfig(envWith(map[string]string{"MAX_VIEWERS": "2"}))
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(NewHandler(context.Background(), cfg, fakeGuide{}, log.New(io.Discard, "", 0)))
	t.Cleanup(srv.Close)
	return srv
}

func getJSON(t *testing.T, srv *httptest.Server, path string, v any) {
	t.Helper()
	resp, err := srv.Client().Get(srv.URL + path)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("%s: status %d", path, resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(v); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
}

func TestDiscoverJSON(t *testing.T) {
	var d struct {
		FriendlyName, Manufacturer, ModelNumber, FirmwareName, DeviceID, BaseURL, LineupURL string
		TunerCount                                                                          int
	}
	getJSON(t, newTunerServer(t), "/discover.json", &d)
	if d.FriendlyName != "Karusel Tuner" || d.Manufacturer != "Silicondust" || d.ModelNumber != "HDTC-2US" ||
		d.FirmwareName != "hdhomeruntc_atsc" || d.DeviceID != "4B415258" {
		t.Errorf("device identity = %+v", d)
	}
	if d.BaseURL != "http://127.0.0.1:18080" || d.LineupURL != "http://127.0.0.1:18080/lineup.json" || d.TunerCount != 2 {
		t.Errorf("device urls/tuners = %+v", d)
	}
}

func TestLineupJSON(t *testing.T) {
	var lineup []struct{ GuideNumber, GuideName, URL string }
	getJSON(t, newTunerServer(t), "/lineup.json", &lineup)
	if len(lineup) != 1 {
		t.Fatalf("lineup = %+v", lineup)
	}
	ch := lineup[0]
	if ch.GuideNumber != epg.ChannelID || ch.GuideName != epg.ChannelName || ch.URL != "http://127.0.0.1:18080/live.ts" {
		t.Errorf("channel = %+v", ch)
	}
}

func TestLineupStatusJSON(t *testing.T) {
	var st struct {
		ScanInProgress, ScanPossible int
		Source                       string
		SourceList                   []string
	}
	getJSON(t, newTunerServer(t), "/lineup_status.json", &st)
	if st.ScanInProgress != 0 || st.ScanPossible != 1 || st.Source != "Cable" || len(st.SourceList) != 1 || st.SourceList[0] != "Cable" {
		t.Errorf("lineup status = %+v", st)
	}
}

func TestLineupPostAccepted(t *testing.T) {
	srv := newTunerServer(t)
	for _, scan := range []string{"start", "abort"} {
		resp, err := srv.Client().Post(srv.URL+"/lineup.post?scan="+scan, "text/plain", nil)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("scan=%s: status %d", scan, resp.StatusCode)
		}
	}
}
