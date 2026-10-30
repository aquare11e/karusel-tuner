package relay

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"syscall"
	"time"
)

// attempt is one Streamlink process that has produced its first bytes.
type attempt struct {
	cmd    *exec.Cmd
	stdout io.ReadCloser
	stderr *redactingWriter
	cancel context.CancelFunc
}

// stop kills the process group, reaps it and flushes its last log line.
func (a *attempt) stop() {
	a.cancel()
	_ = a.cmd.Wait()
	a.stderr.Flush()
}

// exitState describes how the process ended. Wait's error is not used:
// stop cancels first, so a process that had already exited on its own would
// be reported as our cancellation instead of its exit status.
func (a *attempt) exitState() string {
	if a.cmd.ProcessState == nil {
		return "not reaped"
	}
	return a.cmd.ProcessState.String()
}

// startAttempt launches Streamlink and waits up to StartupTimeout for the
// first bytes. On error nothing is left running.
func (h *Handler) startAttempt(ctx context.Context) (*attempt, []byte, error) {
	actx, cancel := context.WithCancel(ctx)
	// A stalled upstream must not hang Plex indefinitely before headers. One
	// deadline covers resolving the source and reading the first bytes.
	timer := time.AfterFunc(h.cfg.StartupTimeout, cancel)
	input, err := h.streamInput(actx)
	if err != nil {
		if !timer.Stop() {
			err = fmt.Errorf("startup timeout: %w", err)
		}
		cancel()
		return nil, nil, err
	}
	// Arguments go to exec as argv; no shell interpolation.
	cmd := exec.CommandContext(actx, h.cfg.StreamlinkBin,
		append([]string{"--stdout", "--loglevel", "warning"}, input...)...)
	// Keep child helpers (if any) in a separate process group. On timeout or
	// disconnect, kill the entire group; otherwise a grandchild holding stdout
	// could keep the HTTP response open after the parent was killed.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return os.ErrProcessDone
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	cmd.WaitDelay = 2 * time.Second
	stderr := &redactingWriter{out: h.stderr}
	cmd.Stderr = stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		timer.Stop()
		cancel()
		return nil, nil, fmt.Errorf("stdout pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		timer.Stop()
		cancel()
		return nil, nil, fmt.Errorf("start: %w", err)
	}
	a := &attempt{cmd: cmd, stdout: stdout, stderr: stderr, cancel: cancel}

	first := make([]byte, 32*1024)
	n, readErr := stdout.Read(first)
	if !timer.Stop() {
		// The timer already killed the process; bytes that raced in are not
		// a usable stream.
		n = 0
	}
	if n == 0 {
		a.stop()
		return nil, nil, fmt.Errorf("no video (startup timeout or exit): read=%v; process %s", readErr, a.exitState())
	}
	return a, first[:n], nil
}

func (h *Handler) live(w http.ResponseWriter, r *http.Request) {
	select {
	case h.slots <- struct{}{}:
		h.active.Add(1)
		defer func() { <-h.slots; h.active.Add(-1) }()
	default:
		http.Error(w, "all tuner slots are busy", http.StatusServiceUnavailable)
		return
	}

	// Request cancellation and SIGTERM both kill the child process.
	ctx, cancel := context.WithCancel(r.Context())
	stop := context.AfterFunc(h.appCtx, cancel)
	defer func() { stop(); cancel() }()

	a, first, err := h.startAttempt(ctx)
	if err != nil {
		h.logger.Printf("upstream returned no video: %v", err)
		if r.Context().Err() == nil && h.appCtx.Err() == nil {
			http.Error(w, "upstream unavailable or startup timeout", http.StatusBadGateway)
		}
		return
	}

	w.Header().Set("Content-Type", "video/mp2t")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	fw := &flushingWriter{ResponseWriter: w}
	backoff := h.backoffMin
	for {
		streamed := h.pump(ctx, fw, a, first)
		if fw.err != nil || ctx.Err() != nil {
			return
		}
		// Only a run that lasted a while proves the upstream recovered; a
		// flapping source keeps the grown pause so restarts stay spaced out.
		if streamed >= h.backoffMax {
			backoff = h.backoffMin
		}
		// Upstream ended while the viewer (often a Plex recording) is still
		// connected: keep the same response and splice in a new process.
		if a, first, backoff = h.reconnect(ctx, backoff); a == nil {
			return
		}
		h.restarts.Add(1)
	}
}

// pump copies one attempt's output to the viewer, then kills and reaps it.
// It returns how long the attempt streamed.
func (h *Handler) pump(ctx context.Context, fw *flushingWriter, a *attempt, first []byte) time.Duration {
	started := time.Now()
	if _, err := fw.Write(first); err == nil {
		_, _ = io.Copy(fw, a.stdout)
	}
	a.stop()
	if fw.err == nil && ctx.Err() == nil {
		h.logger.Printf("upstream ended (%s); reconnecting for up to %s", a.exitState(), h.cfg.ReconnectWindow)
	}
	return time.Since(started)
}

// reconnect restarts Streamlink, pausing first and doubling the pause after
// every attempt; it returns the pause to start from after the next outage.
// The window counts from this outage, so every recovered stream gets a
// fresh window next time.
func (h *Handler) reconnect(ctx context.Context, backoff time.Duration) (*attempt, []byte, time.Duration) {
	deadline := time.Now().Add(h.cfg.ReconnectWindow)
	for {
		pause := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			pause.Stop()
			return nil, nil, backoff
		case <-pause.C:
		}
		if time.Now().After(deadline) {
			h.logger.Printf("upstream did not recover within %s; closing", h.cfg.ReconnectWindow)
			return nil, nil, backoff
		}
		a, first, err := h.startAttempt(ctx)
		backoff = min(2*backoff, h.backoffMax)
		if err == nil {
			h.logger.Printf("upstream restarted (restart #%d)", h.restarts.Load()+1)
			return a, first, backoff
		}
		if ctx.Err() != nil {
			return nil, nil, backoff
		}
		h.logger.Printf("upstream restart failed: %v", err)
	}
}

type flushingWriter struct {
	http.ResponseWriter
	err error // first write error: the viewer has gone
}

func (f *flushingWriter) Write(b []byte) (int, error) {
	n, err := f.ResponseWriter.Write(b)
	if err != nil && f.err == nil {
		f.err = err
	}
	flush(f.ResponseWriter)
	return n, err
}

func flush(w http.ResponseWriter) {
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
}
