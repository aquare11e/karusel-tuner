package epg

import (
	"context"
	"errors"
	"log"
	"net/http"
	"sync"
	"time"
)

type Status struct {
	Programmes  int
	LastSuccess time.Time
	LastError   string
}

// Guide keeps the last successfully downloaded schedule in memory. HTTP
// handlers read it; only Refresh talks to the network.
type Guide struct {
	client  *http.Client
	baseURL string
	days    int
	logger  *log.Logger

	retryMin time.Duration
	now      func() time.Time

	mu     sync.RWMutex
	progs  []Programme
	status Status
}

func NewGuide(client *http.Client, baseURL string, days int, logger *log.Logger) *Guide {
	return &Guide{
		client:   client,
		baseURL:  baseURL,
		days:     days,
		logger:   logger,
		retryMin: time.Minute,
		now:      time.Now,
	}
}

// Refresh downloads yesterday through the published horizon. On any error
// the previous schedule stays in place.
func (g *Guide) Refresh(ctx context.Context, now time.Time) error {
	y, m, d := now.In(Moscow).Date()
	today := time.Date(y, m, d, 0, 0, 0, 0, Moscow)
	var days [][]Programme
	for i := -1; i < g.days; i++ {
		progs, err := FetchDay(ctx, g.client, g.baseURL, today.AddDate(0, 0, i))
		if err != nil {
			return g.fail(err)
		}
		if len(progs) == 0 && i > 0 {
			break
		}
		days = append(days, progs)
	}
	merged := Merge(days...)
	if len(merged) == 0 {
		return g.fail(errors.New("empty schedule"))
	}
	g.mu.Lock()
	g.progs = merged
	g.status = Status{Programmes: len(merged), LastSuccess: now}
	g.mu.Unlock()
	return nil
}

func (g *Guide) fail(err error) error {
	g.mu.Lock()
	g.status.LastError = err.Error()
	g.mu.Unlock()
	g.logger.Printf("epg refresh failed: %v", err)
	return err
}

// Run refreshes immediately, then every interval. After failures (e.g. the
// VPN is not up yet) it retries sooner, backing off up to maxFailureRetry.
func (g *Guide) Run(ctx context.Context, every time.Duration) {
	failures := 0
	for {
		if err := g.Refresh(ctx, g.now()); err != nil {
			failures++
		} else {
			failures = 0
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(g.nextDelay(every, failures)):
		}
	}
}

// maxFailureRetry bounds the pause between failed refreshes, so the guide
// appears soon after a long VPN outage ends regardless of the interval.
const maxFailureRetry = 30 * time.Minute

func (g *Guide) nextDelay(every time.Duration, failures int) time.Duration {
	if failures == 0 {
		return every
	}
	return retryDelay(g.retryMin, min(every, maxFailureRetry), failures)
}

// retryDelay doubles from base per consecutive failure, capped at limit.
// Doubling stops at the cap, so long outages cannot overflow into a hot loop.
func retryDelay(base, limit time.Duration, failures int) time.Duration {
	delay := base
	for i := 1; i < failures && delay < limit; i++ {
		delay *= 2
	}
	return min(delay, limit)
}

// Programmes returns the cached schedule. Callers must not modify it.
func (g *Guide) Programmes() []Programme {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.progs
}

func (g *Guide) Status() Status {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.status
}
