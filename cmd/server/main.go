// Command server runs the web page analyzer.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/pprof"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"web-analyzer/internal/analyzer"
	"web-analyzer/internal/metrics"
	"web-analyzer/internal/web"
)

const shutdownTimeout = 15 * time.Second

func main() {
	cfg := loadConfig(os.Getenv)
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: cfg.logLevel}))
	slog.SetDefault(logger)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, cfg, logger); err != nil {
		logger.Error("server stopped", slog.Any("error", err))
		stop()
		os.Exit(1)
	}
}

// run starts the public and admin servers and blocks until ctx is cancelled
// or a server fails, then shuts both down gracefully.
func run(ctx context.Context, cfg config, logger *slog.Logger) error {
	reg := prometheus.NewRegistry()
	reg.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	)

	client := &http.Client{
		// Per request timeouts are set by the analyzer through the context.
		Transport: &http.Transport{
			Proxy:               http.ProxyFromEnvironment,
			MaxIdleConns:        100,
			MaxIdleConnsPerHost: 10,
			IdleConnTimeout:     90 * time.Second,
			TLSHandshakeTimeout: 10 * time.Second,
		},
	}
	a := analyzer.New(client, analyzer.Config{})

	h, err := web.NewHandler(a, metrics.New(reg), logger)
	if err != nil {
		return err
	}

	app := &http.Server{
		Addr:              cfg.addr,
		Handler:           h.Routes(promhttp.HandlerFor(reg, promhttp.HandlerOpts{})),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      90 * time.Second, // analysis can take up to ~45s
		IdleTimeout:       120 * time.Second,
		ErrorLog:          slog.NewLogLogger(logger.Handler(), slog.LevelWarn),
	}
	admin := &http.Server{
		Addr:              cfg.adminAddr,
		Handler:           pprofMux(),
		ReadHeaderTimeout: 5 * time.Second,
	}

	errCh := make(chan error, 2)
	for _, srv := range []*http.Server{app, admin} {
		go func() {
			logger.Info("listening", slog.String("addr", srv.Addr))
			if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				errCh <- fmt.Errorf("listen on %s: %w", srv.Addr, err)
			}
		}()
	}

	// If one server fails, still shut the other one down cleanly.
	var serveErr error
	select {
	case serveErr = <-errCh:
	case <-ctx.Done():
		logger.Info("shutting down")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	return errors.Join(serveErr, app.Shutdown(shutdownCtx), admin.Shutdown(shutdownCtx))
}

// pprofMux serves the profiling endpoints. It is kept off the public port.
func pprofMux() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/debug/pprof/", pprof.Index)
	mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("/debug/pprof/trace", pprof.Trace)
	return mux
}
