package relay

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/aquare11e/karusel-tuner/internal/epg"
)

// The MediaVitrina player no longer embeds the playlist URL that Streamlink's
// mediavitrina plugin scrapes (it fails with "invalid url_json" as of
// Streamlink 8.6.3). The relay asks the same streams API the official player
// uses and hands Streamlink a plain HLS URL instead.

const (
	maxPlayerPageBytes = 1 << 20 // real page is ~60 KB
	maxStreamsBytes    = 256 << 10
)

var (
	reStreamsAPI   = regexp.MustCompile(`streams_api_v2_url\s*:\s*['"]([^'"]+)['"]`)
	rePlaceholders = regexp.MustCompile(`\{\{[A-Za-z0-9_]+\}\}`)
)

// isMediaVitrinaPlayer matches official player pages, the URLs Streamlink's
// plugin accepts.
func isMediaVitrinaPlayer(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && u.Host == "player.mediavitrina.ru" && strings.HasSuffix(u.Path, "/player.html")
}

// streamInput returns Streamlink's input arguments: stream URL and quality.
// Player pages are resolved on every attempt because playlist tokens are
// short-lived.
func (h *Handler) streamInput(ctx context.Context) ([]string, error) {
	if !isMediaVitrinaPlayer(h.cfg.SourceURL) {
		return []string{h.cfg.SourceURL, "best"}, nil
	}
	stream, err := resolveMediaVitrina(ctx, h.client, h.cfg.SourceURL)
	if err != nil {
		return nil, fmt.Errorf("resolve MediaVitrina stream: %w", err)
	}
	return []string{"hls://" + stream, "best"}, nil
}

// resolveMediaVitrina returns the unprotected HLS playlist that the official
// player at playerURL would play. DRM formats (hlsp, mpdp) are never chosen:
// when only they are offered, there is nothing this relay may play. The
// returned URL carries a token; never log it.
func resolveMediaVitrina(ctx context.Context, client *http.Client, playerURL string) (string, error) {
	player, err := url.Parse(playerURL)
	if err != nil {
		return "", errors.New("invalid player URL")
	}
	page, err := fetch(ctx, client, playerURL, "", maxPlayerPageBytes)
	if err != nil {
		return "", fmt.Errorf("player page: %w", err)
	}
	m := reStreamsAPI.FindSubmatch(page)
	if m == nil {
		return "", errors.New("streams API URL not found in player page (player changed?)")
	}
	api := strings.ReplaceAll(string(m[1]), "{{PLAYER_REFERER_HOSTNAME}}", player.Hostname())
	api = rePlaceholders.ReplaceAllString(api, "")
	if !isHTTPS(api) {
		return "", errors.New("streams API URL is not an https URL")
	}
	// The API answers 409 without the player's Referer.
	body, err := fetch(ctx, client, api, playerURL, maxStreamsBytes)
	if err != nil {
		return "", fmt.Errorf("streams API: %w", err)
	}
	var streams struct {
		Country string   `json:"country"`
		HLS     []string `json:"hls"`
	}
	if err := json.Unmarshal(body, &streams); err != nil {
		return "", fmt.Errorf("streams API: %w", err)
	}
	if len(streams.HLS) == 0 {
		return "", fmt.Errorf("no unprotected HLS stream offered for country %q (DRM formats are not supported)", streams.Country)
	}
	stream := streams.HLS[0]
	// Outside the allowed countries the API offers a looping notice instead
	// of the channel; recording it would silently produce a useless file.
	if strings.Contains(stream, "/georestrictions/") {
		return "", fmt.Errorf("geo-restricted placeholder for country %q: check the VPN egress", streams.Country)
	}
	if !isHTTPS(stream) {
		return "", errors.New("stream URL is not an https URL")
	}
	return stream, nil
}

// fetch GETs raw and returns at most limit bytes of a 200 response. Errors
// omit the URL because query strings may carry tokens.
func fetch(ctx context.Context, client *http.Client, raw, referer string, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return nil, errors.New("invalid URL")
	}
	req.Header.Set("User-Agent", epg.UserAgent)
	if referer != "" {
		req.Header.Set("Referer", referer)
	}
	resp, err := client.Do(req)
	if err != nil {
		var urlErr *url.Error
		if errors.As(err, &urlErr) {
			return nil, urlErr.Err
		}
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > limit {
		return nil, fmt.Errorf("response exceeds %d bytes", limit)
	}
	return body, nil
}

func isHTTPS(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && u.Scheme == "https" && u.Host != ""
}
