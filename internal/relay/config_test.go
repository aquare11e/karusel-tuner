package relay

import (
	"strings"
	"testing"
	"time"
)

func envWith(overrides map[string]string) func(string) string {
	return func(k string) string { return overrides[k] }
}

func TestLoadConfigDefaults(t *testing.T) {
	cfg, err := LoadConfig(envWith(nil))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MaxViewers != 2 || cfg.StartupTimeout != 45*time.Second {
		t.Fatalf("unexpected config: %+v", cfg)
	}
	if !strings.HasPrefix(cfg.SourceURL, "https://player.mediavitrina.ru/") {
		t.Fatalf("unexpected default source: %q", cfg.SourceURL)
	}
	if cfg.EPGSourceURL != "https://www.karusel-tv.ru/schedule/Europe/Moscow" || cfg.EPGRefresh != 6*time.Hour || cfg.EPGDays != 12 {
		t.Fatalf("unexpected EPG defaults: %+v", cfg)
	}
	if cfg.ReconnectWindow != 5*time.Minute {
		t.Fatalf("unexpected reconnect window: %s", cfg.ReconnectWindow)
	}
}

func TestLoadConfigValidation(t *testing.T) {
	cases := []map[string]string{
		{"MAX_VIEWERS": "zero"},
		{"MAX_VIEWERS": "0"},
		{"STARTUP_TIMEOUT": "0s"},
		{"STARTUP_TIMEOUT": "many"},
		{"PUBLIC_BASE_URL": "example.org"},
		{"SOURCE_URL": "file:///etc/passwd"},
		{"EPG_SOURCE_URL": "ftp://example.org"},
		{"EPG_REFRESH": "0s"},
		{"EPG_REFRESH": "often"},
		{"EPG_DAYS": "0"},
		{"EPG_DAYS": "32"},
		{"EPG_DAYS": "many"},
		{"RECONNECT_WINDOW": "0s"},
		{"RECONNECT_WINDOW": "soon"},
	}
	for _, overrides := range cases {
		if _, err := LoadConfig(envWith(overrides)); err == nil {
			t.Errorf("LoadConfig(%v) should fail", overrides)
		}
	}
}

func TestLoadConfigOverrides(t *testing.T) {
	cfg, err := LoadConfig(envWith(map[string]string{
		"MAX_VIEWERS": "2", "STARTUP_TIMEOUT": "250ms",
		"PUBLIC_BASE_URL": "http://localhost:9999/", "STREAMLINK_BIN": "/tmp/streamlink",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MaxViewers != 2 || cfg.StartupTimeout != 250*time.Millisecond || cfg.PublicBaseURL != "http://localhost:9999" {
		t.Fatalf("unexpected config: %+v", cfg)
	}
}
