// Package http provides HTTP server functionality with Prometheus metrics and health endpoints.
package http

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.uber.org/zap"

	"whatdj/internal/core"
)

const homePageHTML = `<!DOCTYPE html>
<html>
<head>
    <title>WhatDj v2</title>
    <style>
        body { font-family: Arial, sans-serif; margin: 40px; }
        .header { color: #333; }
        .endpoint { margin: 10px 0; }
        .endpoint a { text-decoration: none; color: #0066cc; }
        .endpoint a:hover { text-decoration: underline; }
    </style>
</head>
<body>
    <h1 class="header">🎵 WhatDj v2</h1>
    <p>Live WhatsApp → Spotify DJ Service</p>

    <h2>Endpoints</h2>
    <div class="endpoint">📊 <a href="/metrics">Metrics</a> - Prometheus metrics</div>
    <div class="endpoint">💚 <a href="/healthz">Health</a> - Health check</div>
    <div class="endpoint">✅ <a href="/readyz">Ready</a> - Readiness check</div>

    <h2>Status</h2>
    <p>Service is running and ready to process WhatsApp messages.</p>
</body>
</html>`

const (
	// ShutdownTimeoutSeconds is the timeout for graceful server shutdown
	ShutdownTimeoutSeconds = 10
)

type Server struct {
	config  *core.ServerConfig
	logger  *zap.Logger
	server  *http.Server
	metrics *Metrics
}

type Metrics struct {
	MessagesTotal   *prometheus.CounterVec
	AddsTotal       *prometheus.CounterVec
	DuplicatesTotal prometheus.Counter
	LLMCallsTotal   *prometheus.CounterVec
	ErrorsTotal     *prometheus.CounterVec
	ProcessingTime  *prometheus.HistogramVec
	PlaylistSize    prometheus.Gauge
	ActiveSessions  prometheus.Gauge
}

func NewServer(config *core.ServerConfig, logger *zap.Logger) *Server {
	metrics := newMetrics()
	mux := setupRoutes(logger)
	server := createHTTPServer(config, mux)

	return &Server{
		config:  config,
		logger:  logger,
		server:  server,
		metrics: metrics,
	}
}

func newMetrics() *Metrics {
	metrics := &Metrics{
		MessagesTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "whatdj_messages_total",
				Help: "Total number of messages processed",
			},
			[]string{"type", "status"},
		),
		AddsTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "whatdj_adds_total",
				Help: "Total number of tracks added to playlist",
			},
			[]string{"source"},
		),
		DuplicatesTotal: prometheus.NewCounter(
			prometheus.CounterOpts{
				Name: "whatdj_duplicates_total",
				Help: "Total number of duplicate tracks rejected",
			},
		),
		LLMCallsTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "whatdj_llm_calls_total",
				Help: "Total number of LLM API calls",
			},
			[]string{"provider", "status"},
		),
		ErrorsTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "whatdj_errors_total",
				Help: "Total number of errors",
			},
			[]string{"component", "type"},
		),
		ProcessingTime: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "whatdj_processing_duration_seconds",
				Help:    "Time spent processing messages",
				Buckets: prometheus.DefBuckets,
			},
			[]string{"type"},
		),
		PlaylistSize: prometheus.NewGauge(
			prometheus.GaugeOpts{
				Name: "whatdj_playlist_size",
				Help: "Current number of tracks in playlist",
			},
		),
		ActiveSessions: prometheus.NewGauge(
			prometheus.GaugeOpts{
				Name: "whatdj_active_sessions",
				Help: "Number of active message processing sessions",
			},
		),
	}

	prometheus.MustRegister(
		metrics.MessagesTotal,
		metrics.AddsTotal,
		metrics.DuplicatesTotal,
		metrics.LLMCallsTotal,
		metrics.ErrorsTotal,
		metrics.ProcessingTime,
		metrics.PlaylistSize,
		metrics.ActiveSessions,
	)

	return metrics
}

func setupRoutes(logger *zap.Logger) *http.ServeMux {
	mux := http.NewServeMux()

	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if _, err := w.Write([]byte(`{"status":"ok","service":"whatdj"}`)); err != nil {
			logger.Warn("Failed to write health response", zap.Error(err))
		}
	})

	mux.HandleFunc("/readyz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if _, err := w.Write([]byte(`{"status":"ready","service":"whatdj"}`)); err != nil {
			logger.Warn("Failed to write ready response", zap.Error(err))
		}
	})

	mux.Handle("/metrics", promhttp.Handler())
	mux.HandleFunc("/", homeHandler(logger))

	return mux
}

func homeHandler(logger *zap.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusOK)
		if _, err := w.Write([]byte(homePageHTML)); err != nil {
			logger.Warn("Failed to write HTML response", zap.Error(err))
		}
	}
}

func createHTTPServer(config *core.ServerConfig, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:         fmt.Sprintf("%s:%d", config.Host, config.Port),
		Handler:      handler,
		ReadTimeout:  config.ReadTimeout,
		WriteTimeout: config.WriteTimeout,
	}
}

func (s *Server) Start(ctx context.Context) error {
	s.logger.Info("Starting HTTP server",
		zap.String("addr", s.server.Addr))

	go func() {
		<-ctx.Done()
		s.logger.Info("Shutting down HTTP server")

		shutdownCtx, cancel := context.WithTimeout(context.Background(), ShutdownTimeoutSeconds*time.Second)
		defer cancel()

		if err := s.server.Shutdown(shutdownCtx); err != nil {
			s.logger.Error("Failed to shutdown HTTP server gracefully", zap.Error(err))
		}
	}()

	if err := s.server.ListenAndServe(); err != http.ErrServerClosed {
		return fmt.Errorf("HTTP server failed: %w", err)
	}

	return nil
}

func (s *Server) GetMetrics() *Metrics {
	return s.metrics
}

func (s *Server) RecordMessage(msgType, status string) {
	s.metrics.MessagesTotal.WithLabelValues(msgType, status).Inc()
}

func (s *Server) RecordAdd(source string) {
	s.metrics.AddsTotal.WithLabelValues(source).Inc()
}

func (s *Server) RecordDuplicate() {
	s.metrics.DuplicatesTotal.Inc()
}

func (s *Server) RecordLLMCall(provider, status string) {
	s.metrics.LLMCallsTotal.WithLabelValues(provider, status).Inc()
}

func (s *Server) RecordError(component, errorType string) {
	s.metrics.ErrorsTotal.WithLabelValues(component, errorType).Inc()
}

func (s *Server) RecordProcessingTime(msgType string, duration time.Duration) {
	s.metrics.ProcessingTime.WithLabelValues(msgType).Observe(duration.Seconds())
}

func (s *Server) SetPlaylistSize(size int) {
	s.metrics.PlaylistSize.Set(float64(size))
}

func (s *Server) SetActiveSessions(count int) {
	s.metrics.ActiveSessions.Set(float64(count))
}
