package relay

import (
	"encoding/json"
	"net/http"

	"example.com/karusel-plex-relay/internal/epg"
)

// deviceID must stay stable across restarts; a new ID looks like a new
// tuner to Plex and drops its DVR setup.
const deviceID = "4B415258"

// HDHomeRun HTTP API subset that Plex uses when a tuner is added by address.
// UDP discovery is intentionally absent: the tuner is entered manually.

func (h *Handler) discover(w http.ResponseWriter, _ *http.Request) {
	base := h.cfg.PublicBaseURL
	writeJSON(w, struct {
		FriendlyName    string
		Manufacturer    string
		ModelNumber     string
		FirmwareName    string
		FirmwareVersion string
		DeviceID        string
		DeviceAuth      string
		BaseURL         string
		LineupURL       string
		TunerCount      int
	}{
		FriendlyName:    "Karusel Relay",
		Manufacturer:    "Silicondust",
		ModelNumber:     "HDTC-2US",
		FirmwareName:    "hdhomeruntc_atsc",
		FirmwareVersion: "20200101",
		DeviceID:        deviceID,
		DeviceAuth:      "karusel-relay",
		BaseURL:         base,
		LineupURL:       base + "/lineup.json",
		TunerCount:      h.cfg.MaxViewers,
	})
}

func (h *Handler) lineupStatus(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, struct {
		ScanInProgress int
		ScanPossible   int
		Source         string
		SourceList     []string
	}{ScanInProgress: 0, ScanPossible: 1, Source: "Cable", SourceList: []string{"Cable"}})
}

func (h *Handler) lineup(w http.ResponseWriter, _ *http.Request) {
	type channel struct {
		GuideNumber string
		GuideName   string
		URL         string
	}
	writeJSON(w, []channel{{
		GuideNumber: epg.ChannelID,
		GuideName:   epg.ChannelName,
		URL:         h.cfg.PublicBaseURL + "/live.ts",
	}})
}

// lineupPost accepts channel scan requests; the lineup is fixed, so there
// is nothing to scan.
func (h *Handler) lineupPost(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(v)
}
