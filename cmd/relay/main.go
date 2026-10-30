package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"example.com/karusel-plex-relay/internal/epg"
	"example.com/karusel-plex-relay/internal/relay"
)

func main() {
	cfg, err := relay.LoadConfig(os.Getenv)
	if err != nil {
		log.Fatalf("invalid configuration: %v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Schedule requests leave through the same VPN namespace as the stream.
	guide := epg.NewGuide(&http.Client{Timeout: 20 * time.Second}, cfg.EPGSourceURL, cfg.EPGDays, log.Default())
	go guide.Run(ctx, cfg.EPGRefresh)

	srv := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           relay.NewHandler(ctx, cfg, guide, log.Default()),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       30 * time.Second,
		// Live TV responses can run for hours; a global WriteTimeout would kill them.
	}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			log.Printf("http shutdown: %v", err)
		}
	}()
	log.Printf("relay listening on %s (max viewers=%d)", cfg.ListenAddr, cfg.MaxViewers)
	err = srv.ListenAndServe()
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}
