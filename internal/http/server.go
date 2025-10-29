// Package http provides HTTP server functionality with Prometheus metrics and health endpoints.
package http

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.uber.org/zap"

	"djalgorhythm/internal/core"
)

//go:embed web/static
var staticFiles embed.FS

const homePageHTML = `<!DOCTYPE html>
<html>
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>DJAlgoRhythm</title>
    <link rel="stylesheet" href="/static/fontawesome/css/all.min.css">
    <style>
        body { font-family: Arial, sans-serif; margin: 40px; }
        .header { color: #333; }
        .header i { margin-right: 10px; color: #1DB954; }
        .endpoint { margin: 10px 0; }
        .endpoint i { margin-right: 8px; width: 20px; display: inline-block; text-align: center; }
        .endpoint a { text-decoration: none; color: #0066cc; }
        .endpoint a:hover { text-decoration: underline; }
    </style>
</head>
<body>
    <h1 class="header"><i class="fas fa-music"></i>DJAlgoRhythm</h1>
    <p>Live Chat → Spotify DJ Service</p>

    <h2>Endpoints</h2>
    <div class="endpoint"><i class="fas fa-chart-bar"></i><a href="/metrics">Metrics</a> - Prometheus metrics</div>
    <div class="endpoint"><i class="fas fa-heartbeat"></i><a href="/healthz">Health</a> - Health check</div>
    <div class="endpoint"><i class="fas fa-check-circle"></i><a href="/readyz">Ready</a> - Readiness check</div>
</body>
</html>`

const (
	// ShutdownTimeoutSeconds is the timeout for graceful server shutdown.
	ShutdownTimeoutSeconds = 10
)

// Server represents an HTTP server with metrics and health endpoints.
type Server struct {
	config  *core.ServerConfig
	logger  *zap.Logger
	server  *http.Server
	metrics *Metrics
}

// Metrics holds Prometheus metrics for the HTTP server.
type Metrics struct {
	PlaylistSize prometheus.Gauge
}

// NewServer creates a new HTTP server with metrics and health endpoints.
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
		PlaylistSize: prometheus.NewGauge(
			prometheus.GaugeOpts{
				Name: "djalgorhythm_playlist_size",
				Help: "Current number of tracks in playlist",
			},
		),
	}

	prometheus.MustRegister(
		metrics.PlaylistSize,
	)

	return metrics
}

func setupRoutes(logger *zap.Logger) *http.ServeMux {
	mux := http.NewServeMux()

	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		if _, err := w.Write([]byte(`{"status":"ok","service":"djalgorhythm"}`)); err != nil {
			logger.Warn("Failed to write health response", zap.Error(err))
		}
	})

	mux.HandleFunc("/readyz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		if _, err := w.Write([]byte(`{"status":"ready","service":"djalgorhythm"}`)); err != nil {
			logger.Warn("Failed to write ready response", zap.Error(err))
		}
	})

	// Serve static files (Font Awesome, etc.).
	staticFS, err := fs.Sub(staticFiles, "web/static")
	if err != nil {
		logger.Fatal("Failed to create static file system", zap.Error(err))
	}
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.FS(staticFS))))

	mux.Handle("/metrics", promhttp.Handler())
	mux.HandleFunc("/", homeHandler(logger))

	return mux
}

func homeHandler(logger *zap.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
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

// Start starts the HTTP server and handles graceful shutdown on context cancellation.
func (s *Server) Start(ctx context.Context) error {
	s.logger.Info("Starting HTTP server",
		zap.String("addr", s.server.Addr))

	go func() {
		<-ctx.Done()
		s.logger.Info("Shutting down HTTP server")

		shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), ShutdownTimeoutSeconds*time.Second)
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
