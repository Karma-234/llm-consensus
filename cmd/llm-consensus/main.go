package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/karma-234/llm-consensus/internal/config"
	"github.com/karma-234/llm-consensus/internal/handler"
	"github.com/karma-234/llm-consensus/internal/store"
	"github.com/karma-234/llm-consensus/internal/telemetry"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, nil)))

	cfg, err := config.LoadConfig("config.yaml")
	if err != nil {
		slog.Error("failed to load config", "error", err)
		os.Exit(1)
	}

	ts, stopStore := store.NewTranscriptStore(time.Hour)
	defer stopStore()

	shutdownTracer, err := telemetry.InitTracer(cfg.OTel.Endpoint)
	if err != nil {
		slog.Error("failed to init OTel tracer", "error", err)
		os.Exit(1)
	}
	defer shutdownTracer()

	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, `{"status":"ok"}`)
	})

	mux.HandleFunc("GET /metrics", promhttp.Handler().ServeHTTP)

	mux.HandleFunc("GET /v1/models", func(w http.ResponseWriter, r *http.Request) {
		handler.HandleModels(w, r, cfg)
	})

	mux.HandleFunc("GET /v1/debate/{id}/transcript", func(w http.ResponseWriter, r *http.Request) {
		handler.HandleTranscript(w, r, ts)
	})

	mux.HandleFunc("POST /v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		handler.HandleChatCompletions(w, r, cfg, ts)
	})
	newServer := &http.Server{
		Addr:    fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port),
		Handler: mux,
	}

	done := make(chan os.Signal, 1)
	signal.Notify(done, os.Interrupt, syscall.SIGTERM, syscall.SIGINT)

	go func() {
		slog.Info("starting server", "host", cfg.Server.Host, "port", cfg.Server.Port)
		if err := newServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("server failed", "error", err)
			os.Exit(1)
		}
	}()
	<-done
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	slog.Info("shutting down server")
	if err := newServer.Shutdown(ctx); err != nil {
		slog.Error("failed to shut down server", "error", err)
		os.Exit(1)
	}
	slog.Info("server stopped gracefully")
}
