package relay

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"sync/atomic"
	"time"

	"github.com/aquare11e/karusel-tuner/internal/epg"
)

// GuideSource is the cached programme guide served to Plex.
type GuideSource interface {
	Programmes() []epg.Programme
	Status() epg.Status
}

type Handler struct {
	appCtx  context.Context
	cfg     Config
	guide   GuideSource
	logger  *log.Logger
	slots   chan struct{}
	active  atomic.Int64
	started time.Time
	client  *http.Client
	stderr  io.Writer // Streamlink diagnostics, after token redaction

	restarts   atomic.Int64
	backoffMin time.Duration
	backoffMax time.Duration
}

func NewHandler(ctx context.Context, cfg Config, guide GuideSource, logger *log.Logger) http.Handler {
	return newHandler(ctx, cfg, guide, logger).routes()
}

func newHandler(ctx context.Context, cfg Config, guide GuideSource, logger *log.Logger) *Handler {
	return &Handler{
		appCtx:  ctx,
		cfg:     cfg,
		guide:   guide,
		logger:  logger,
		slots:   make(chan struct{}, cfg.MaxViewers),
		started: time.Now(),
		// Source resolution is bounded by STARTUP_TIMEOUT via the context.
		client:     &http.Client{},
		stderr:     os.Stderr,
		backoffMin: time.Second,
		backoffMax: 30 * time.Second,
	}
}

func (h *Handler) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", h.health)
	mux.HandleFunc("GET /status", h.status)
	mux.HandleFunc("GET /playlist.m3u", h.playlist)
	mux.HandleFunc("GET /epg.xml", h.epgXML)
	mux.HandleFunc("GET /discover.json", h.discover)
	mux.HandleFunc("GET /lineup_status.json", h.lineupStatus)
	mux.HandleFunc("GET /lineup.json", h.lineup)
	mux.HandleFunc("POST /lineup.post", h.lineupPost)
	mux.HandleFunc("HEAD /live.ts", h.liveHead)
	mux.HandleFunc("GET /live.ts", h.live)
	return mux
}

func (h *Handler) health(w http.ResponseWriter, _ *http.Request) {
	// Liveness only. This does not prove VPN connectivity or upstream availability.
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = io.WriteString(w, "ok\n")
}

func (h *Handler) status(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	guide := h.guide.Status()
	lastSuccess := ""
	if !guide.LastSuccess.IsZero() {
		lastSuccess = guide.LastSuccess.UTC().Format(time.RFC3339)
	}
	_ = json.NewEncoder(w).Encode(struct {
		ActiveViewers    int64     `json:"active_viewers"`
		MaxViewers       int       `json:"max_viewers"`
		UptimeSeconds    int64     `json:"uptime_seconds"`
		UpstreamRestarts int64     `json:"upstream_restarts"`
		EPG              epgStatus `json:"epg"`
	}{
		ActiveViewers:    h.active.Load(),
		MaxViewers:       h.cfg.MaxViewers,
		UptimeSeconds:    int64(time.Since(h.started).Seconds()),
		UpstreamRestarts: h.restarts.Load(),
		EPG: epgStatus{
			Programmes:  guide.Programmes,
			LastSuccess: lastSuccess,
			LastError:   guide.LastError,
		},
	})
}

type epgStatus struct {
	Programmes  int    `json:"programmes"`
	LastSuccess string `json:"last_success"`
	LastError   string `json:"last_error"`
}

func (h *Handler) epgXML(w http.ResponseWriter, _ *http.Request) {
	progs := h.guide.Programmes()
	if len(progs) == 0 {
		// An empty but valid guide could wipe Plex's existing guide data;
		// ask it to come back once the first download has succeeded.
		w.Header().Set("Retry-After", "60")
		http.Error(w, "programme guide not loaded yet", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if err := epg.WriteXMLTV(w, h.cfg.EPGSourceURL, progs); err != nil {
		h.logger.Printf("write xmltv: %v", err)
	}
}

func (h *Handler) playlist(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "audio/x-mpegurl; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = fmt.Fprintf(w,
		"#EXTM3U\n#EXTINF:-1 tvg-id=\"Karusel.ru\" tvg-name=\"Карусель\" group-title=\"Детские\",Карусель\n%s/live.ts\n",
		h.cfg.PublicBaseURL,
	)
}

func (h *Handler) liveHead(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "video/mp2t")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
}
