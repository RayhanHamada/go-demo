// Package app wires the application together: it builds the adapters, runs
// the HTTP server and shuts everything down in order. main() stays a thin
// entrypoint so the composition is testable and readable.
package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/rs/zerolog"

	"gin-clean-arch-demo/internal/adapter/cache"
	"gin-clean-arch-demo/internal/adapter/httpapi"
	"gin-clean-arch-demo/internal/adapter/sqlite"
	"gin-clean-arch-demo/internal/config"
	"gin-clean-arch-demo/internal/logger"
	"gin-clean-arch-demo/internal/usecase"
)

// App holds the running application's resources so they can be released in
// reverse order of construction.
type App struct {
	log    zerolog.Logger
	server *http.Server
	store  *sqlite.Store
	cache  *cache.RedisCache
}

// New builds every dependency from cfg. The returned App is ready to Run.
func New(ctx context.Context, cfg config.Config, log zerolog.Logger) (*App, error) {
	log.Info().
		Str("http_addr", cfg.HTTPAddr).
		Str("database_path", cfg.Database.Path).
		Str("cache_addr", cfg.Cache.Addr).
		Str("log_format", string(cfg.LogFormat)).
		Msg("starting server")

	if err := os.MkdirAll(filepath.Dir(cfg.Database.Path), 0o750); err != nil {
		return nil, fmt.Errorf("create database directory: %w", err)
	}

	store, err := sqlite.Open(ctx, sqlite.Config{
		Path:         cfg.Database.Path,
		MaxOpenConns: cfg.Database.MaxOpenConns,
		BusyTimeout:  cfg.Database.BusyTimeout,
		LogSQL:       cfg.Database.LogSQL,
		Logger:       log,
	})
	if err != nil {
		return nil, err
	}

	cacheStore, err := cache.New(ctx, cache.Config{
		Addr:     cfg.Cache.Addr,
		Password: cfg.Cache.Password,
		DB:       cfg.Cache.DB,
		Timeout:  cfg.Cache.Timeout,
		Logger:   log,
	})
	if err != nil {
		_ = store.Close()
		return nil, err
	}

	userRepo := sqlite.NewUserRepository(store)
	users := usecase.NewUserService(userRepo, cacheStore, log, cfg.Cache.TTL)
	orders := usecase.NewOrderService(sqlite.NewOrderRepository(store), userRepo, cacheStore, log, cfg.Cache.TTL)
	server := httpapi.NewServer(users, orders, store, cacheStore, log).NewHTTPServer(cfg.HTTPAddr)

	return &App{log: log, server: server, store: store, cache: cacheStore}, nil
}

// Run serves until ctx is cancelled or the server fails, then shuts down
// gracefully.
func (a *App) Run(ctx context.Context, shutdownTimeout time.Duration) error {
	errCh := make(chan error, 1)
	go func() {
		a.log.Info().Str("addr", a.server.Addr).Msg("http server listening")
		if err := a.server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case err := <-errCh:
		return fmt.Errorf("http server: %w", err)
	case <-ctx.Done():
		a.log.Info().Msg("shutdown signal received")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := a.server.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("graceful shutdown: %w", err)
	}
	a.log.Info().Msg("server stopped")
	return nil
}

// Close releases the cache and database, logging (not returning) close errors
// so a cleanup failure cannot mask the reason the process is exiting.
func (a *App) Close() {
	if a.cache != nil {
		if err := a.cache.Close(); err != nil {
			a.log.Error().Err(err).Msg("cache close failed")
		}
	}
	if a.store != nil {
		if err := a.store.Close(); err != nil {
			a.log.Error().Err(err).Msg("database close failed")
		}
	}
}

// Main is the process entrypoint: it loads configuration, builds the
// application, runs it and exits non-zero on failure.
func Main() int {
	// A provisional logger covers configuration and startup failures, which
	// happen before the configured level and format are known.
	log := logger.New(os.Getenv("LOG_LEVEL"), logger.FormatJSON, os.Stdout)

	cfg, err := config.Parse()
	if err != nil {
		log.Error().Err(err).Msg("invalid configuration")
		return 1
	}
	log = logger.New(cfg.LogLevel, cfg.LogFormat, os.Stdout)

	ctx, stop := signalContext()
	defer stop()

	application, err := New(ctx, cfg, log)
	if err != nil {
		log.Error().Err(err).Msg("startup failed")
		return 1
	}
	defer application.Close()

	if err := application.Run(ctx, cfg.ShutdownTimeout); err != nil {
		log.Error().Err(err).Msg("server error")
		return 1
	}
	return 0
}
