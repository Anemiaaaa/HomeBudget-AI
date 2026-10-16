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

	"github.com/Anemiaaaa/HomeBudget-AI/internal/config"
	"github.com/Anemiaaaa/HomeBudget-AI/internal/expense"
	"github.com/Anemiaaaa/HomeBudget-AI/internal/httpapi"
	postgresstore "github.com/Anemiaaaa/HomeBudget-AI/internal/storage/postgres"
	"github.com/Anemiaaaa/HomeBudget-AI/migrations"
	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	if err := run(); err != nil {
		slog.Error("application stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	configureLogger(cfg.LogLevel)

	rootContext, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	pool, err := pgxpool.New(rootContext, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	connectContext, cancelConnect := context.WithTimeout(rootContext, 15*time.Second)
	defer cancelConnect()
	if err := pool.Ping(connectContext); err != nil {
		return err
	}
	if err := postgresstore.Migrate(connectContext, pool, migrations.FS); err != nil {
		return err
	}

	repository := postgresstore.NewExpenseRepository(pool)
	adminService := expense.NewAdminService(repository)

	mux := http.NewServeMux()
	httpapi.NewHealthHandler(pool).Register(mux)
	httpapi.NewAPI(adminService, cfg.Location).Register(mux)
	httpapi.RegisterWeb(mux, cfg.WebDir)
	server := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           httpapi.Middleware(mux),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	serverError := make(chan error, 1)
	go func() {
		slog.Info("HTTP server started", "address", cfg.HTTPAddr, "timezone", cfg.Timezone)
		serverError <- server.ListenAndServe()
	}()

	select {
	case <-rootContext.Done():
		shutdownContext, cancelShutdown := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancelShutdown()
		return server.Shutdown(shutdownContext)
	case err := <-serverError:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

func configureLogger(levelText string) {
	var level slog.Level
	if err := level.UnmarshalText([]byte(levelText)); err != nil {
		level = slog.LevelInfo
	}
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level})))
}
