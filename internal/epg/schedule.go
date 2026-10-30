// Package epg downloads the Karusel broadcast schedule and renders it as XMLTV.
package epg

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"
)

// Moscow is the schedule's broadcast time zone. A fixed offset avoids
// depending on tzdata inside the container.
var Moscow = time.FixedZone("MSK", 3*60*60)

const UserAgent = "karusel-tuner/1 (personal use)"

// maxScheduleBytes bounds one day's response; real days are ~20 KB.
const maxScheduleBytes = 1 << 20

type Programme struct {
	Title    string
	Announce string
	Link     string // site-relative, e.g. /announce/8451-smeshariki
	Start    time.Time
	Stop     time.Time
}

type rawProgramme struct {
	Link     string `json:"link"`
	Title    string `json:"title"`
	Announce string `json:"announce"`
	StartAt  int64  `json:"start_at"`
	EndAt    int64  `json:"end_at"`
}

// FetchDay downloads one broadcast day. An empty slice means the day is
// beyond the published horizon.
func FetchDay(ctx context.Context, client *http.Client, baseURL string, day time.Time) ([]Programme, error) {
	endpoint := strings.TrimRight(baseURL, "/") + day.In(Moscow).Format("/2006/01/02") + "/nearest_events.json"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", UserAgent)
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("schedule %s: HTTP %d", day.In(Moscow).Format(time.DateOnly), resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxScheduleBytes+1))
	if err != nil {
		return nil, err
	}
	if len(body) > maxScheduleBytes {
		return nil, fmt.Errorf("schedule %s: response exceeds %d bytes", day.In(Moscow).Format(time.DateOnly), maxScheduleBytes)
	}
	var raw []rawProgramme
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("schedule %s: %w", day.In(Moscow).Format(time.DateOnly), err)
	}
	progs := make([]Programme, 0, len(raw))
	for _, r := range raw {
		title := strings.TrimSpace(r.Title)
		if title == "" || r.EndAt <= r.StartAt {
			continue
		}
		progs = append(progs, Programme{
			Title:    title,
			Announce: strings.TrimSpace(r.Announce),
			Link:     r.Link,
			Start:    time.Unix(r.StartAt, 0),
			Stop:     time.Unix(r.EndAt, 0),
		})
	}
	return progs, nil
}

// Merge joins per-day schedules into one timeline. Adjacent days can repeat
// the programme that crosses the day boundary, so a programme starting at the
// same time as the previous one is dropped. Any other overlap clips the
// earlier programme: one malformed long entry must not swallow the rest.
func Merge(days ...[]Programme) []Programme {
	var all []Programme
	for _, d := range days {
		all = append(all, d...)
	}
	sort.SliceStable(all, func(i, j int) bool { return all[i].Start.Before(all[j].Start) })
	merged := make([]Programme, 0, len(all))
	for _, p := range all {
		if n := len(merged); n > 0 {
			last := &merged[n-1]
			if p.Start.Equal(last.Start) {
				continue
			}
			if p.Start.Before(last.Stop) {
				last.Stop = p.Start
			}
		}
		merged = append(merged, p)
	}
	return merged
}
