package relay

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// This player URL is a starting point, not a guarantee that the site still serves it.
const DefaultSourceURL = "https://player.mediavitrina.ru/karusel/carusel/ao___karusel____web/67ad9799d21cf/player.html"

// The schedule's time zone is part of the path; override it if the web
// stream turns out to follow a different regional feed.
const DefaultEPGSourceURL = "https://www.karusel-tv.ru/schedule/Europe/Moscow"

type Config struct {
	ListenAddr      string
	PublicBaseURL   string
	SourceURL       string
	StreamlinkBin   string
	MaxViewers      int
	StartupTimeout  time.Duration
	ReconnectWindow time.Duration
	EPGSourceURL    string
	EPGRefresh      time.Duration
	EPGDays         int
}

func LoadConfig(getenv func(string) string) (Config, error) {
	get := func(key, fallback string) string {
		if v := strings.TrimSpace(getenv(key)); v != "" {
			return v
		}
		return fallback
	}
	cfg := Config{
		ListenAddr:    get("LISTEN_ADDR", ":8080"),
		PublicBaseURL: strings.TrimRight(get("PUBLIC_BASE_URL", "http://127.0.0.1:18080"), "/"),
		SourceURL:     get("SOURCE_URL", DefaultSourceURL),
		StreamlinkBin: get("STREAMLINK_BIN", "streamlink"),
		EPGSourceURL:  get("EPG_SOURCE_URL", DefaultEPGSourceURL),
	}
	max, err := strconv.Atoi(get("MAX_VIEWERS", "2"))
	if err != nil || max < 1 || max > 100 {
		return cfg, fmt.Errorf("MAX_VIEWERS must be an integer between 1 and 100")
	}
	cfg.MaxViewers = max
	cfg.StartupTimeout, err = time.ParseDuration(get("STARTUP_TIMEOUT", "45s"))
	if err != nil || cfg.StartupTimeout <= 0 {
		return cfg, fmt.Errorf("STARTUP_TIMEOUT must be a positive Go duration (e.g. 45s)")
	}
	cfg.ReconnectWindow, err = time.ParseDuration(get("RECONNECT_WINDOW", "5m"))
	if err != nil || cfg.ReconnectWindow <= 0 {
		return cfg, fmt.Errorf("RECONNECT_WINDOW must be a positive Go duration (e.g. 5m)")
	}
	cfg.EPGRefresh, err = time.ParseDuration(get("EPG_REFRESH", "6h"))
	if err != nil || cfg.EPGRefresh <= 0 {
		return cfg, fmt.Errorf("EPG_REFRESH must be a positive Go duration (e.g. 6h)")
	}
	cfg.EPGDays, err = strconv.Atoi(get("EPG_DAYS", "12"))
	if err != nil || cfg.EPGDays < 1 || cfg.EPGDays > 31 {
		return cfg, fmt.Errorf("EPG_DAYS must be an integer between 1 and 31")
	}
	for name, val := range map[string]string{"PUBLIC_BASE_URL": cfg.PublicBaseURL, "SOURCE_URL": cfg.SourceURL, "EPG_SOURCE_URL": cfg.EPGSourceURL} {
		parsed, err := url.Parse(val)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
			return cfg, fmt.Errorf("%s must be an absolute http(s) URL", name)
		}
	}
	if strings.ContainsAny(cfg.PublicBaseURL, "\r\n") {
		return cfg, fmt.Errorf("PUBLIC_BASE_URL contains invalid characters")
	}
	return cfg, nil
}
