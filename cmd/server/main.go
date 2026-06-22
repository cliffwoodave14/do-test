// Command server is the entrypoint for the ingestion REST API.
//
// It wires configuration, logging, storage, the async ingest pipeline, and the
// HTTP server, then blocks until a termination signal triggers a graceful
// shutdown that drains in-flight work.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"

	"github.com/example/ingest-service/internal/api"
	"github.com/example/ingest-service/internal/config"
	"github.com/example/ingest-service/internal/ingest"
	"github.com/example/ingest-service/internal/store"
	"github.com/prometheus/client_golang/prometheus"
)

func main() {
	if err := run(); err != nil {
		slog.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	log := newLogger(cfg.LogLevel)
	log.Info("starting", "port", cfg.Port, "workers", cfg.WorkerCount, "queue_size", cfg.QueueSize)

	st := store.NewMemory()
	svc := ingest.New(st, log, cfg.WorkerCount, cfg.QueueSize)
	svc.Start()

	// Readiness flips to true once the server is listening and flips back to
	// false during shutdown so load balancers stop sending new traffic.
	var ready atomic.Bool
	h := api.NewHandler(svc, st, log, ready.Load)
	router := api.NewRouter(h, log, prometheus.NewRegistry())

	srv := &http.Server{
		Addr:         ":" + cfg.Port,
		Handler:      router,
		ReadTimeout:  cfg.ReadTimeout,
		WriteTimeout: cfg.WriteTimeout,
	}

	// Run the server in the background so main can wait for signals.
	serverErr := make(chan error, 1)
	go func() {
		ready.Store(true)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErr <- err
		}
	}()

	// Block until interrupted or the server fails to start.
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	select {
	case err := <-serverErr:
		return err
	case sig := <-stop:
		log.Info("shutdown signal received", "signal", sig.String())
	}

	// Graceful shutdown: stop accepting, drain HTTP, then drain workers.
	ready.Store(false)
	ctx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		log.Error("http shutdown error", "err", err)
	}
	if err := svc.Stop(ctx); err != nil {
		log.Error("worker drain error", "err", err)
	}
	log.Info("shutdown complete")
	return nil
}

func newLogger(level string) *slog.Logger {
	var lvl slog.Level
	if err := lvl.UnmarshalText([]byte(level)); err != nil {
		lvl = slog.LevelInfo
	}
	h := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: lvl})
	return slog.New(h)
}
