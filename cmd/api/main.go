package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Fairdose/enteksis_backend/internal/config"
	"github.com/Fairdose/enteksis_backend/internal/httpapi"
	"github.com/Fairdose/enteksis_backend/internal/postgres"
	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	cfg := config.Load()
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if cfg.DatabaseURL == "" {
		logger.Error("DATABASE_URL is required")
		os.Exit(1)
	}

	startupContext, cancelStartup := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelStartup()

	poolConfig, err := pgxpool.ParseConfig(cfg.DatabaseURL)
	if err != nil {
		logger.Error("database configuration is invalid", "error", err)
		os.Exit(1)
	}
	poolConfig.MaxConns = 10
	poolConfig.MinConns = 1
	poolConfig.MaxConnIdleTime = 10 * time.Minute
	pool, err := pgxpool.NewWithConfig(startupContext, poolConfig)
	if err != nil {
		logger.Error("database pool could not be created", "error", err)
		os.Exit(1)
	}
	defer pool.Close()
	if err := pool.Ping(startupContext); err != nil {
		logger.Error("database is unreachable", "error", err)
		os.Exit(1)
	}

	server := &http.Server{
		Addr: cfg.HTTPAddr,
		Handler: httpapi.NewRouter(httpapi.Dependencies{
			Logger:         logger,
			RequestStore:   postgres.NewRequestStore(pool),
			AllowedOrigins: cfg.AllowedOrigins,
		}),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	go func() {
		logger.Info("HTTP server started", "address", cfg.HTTPAddr, "environment", cfg.AppEnv)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("HTTP server failed", "error", err)
			os.Exit(1)
		}
	}()

	shutdownSignal, stop := signal.NotifyContext(
		context.Background(),
		syscall.SIGINT,
		syscall.SIGTERM,
	)
	defer stop()
	<-shutdownSignal.Done()

	shutdownContext, cancelShutdown := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelShutdown()
	if err := server.Shutdown(shutdownContext); err != nil {
		logger.Error("HTTP server shutdown failed", "error", err)
	}
}
