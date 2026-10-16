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

	"github.com/Anemiaaaa/HomeBudget-AI/internal/appbot"
	botdomain "github.com/Anemiaaaa/HomeBudget-AI/internal/bot"
	"github.com/Anemiaaaa/HomeBudget-AI/internal/config"
	"github.com/Anemiaaaa/HomeBudget-AI/internal/expense"
	"github.com/Anemiaaaa/HomeBudget-AI/internal/httpapi"
	"github.com/Anemiaaaa/HomeBudget-AI/internal/llm"
	postgresstore "github.com/Anemiaaaa/HomeBudget-AI/internal/storage/postgres"
	"github.com/Anemiaaaa/HomeBudget-AI/internal/telegram"
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
	ownerService := expense.NewOwnerService(repository)
	botError := make(chan error, 1)
	var botLock *postgresstore.BotLock
	if cfg.BotEnabled {
		botRepository := postgresstore.NewBotRepository(pool)
		botLock, err = botRepository.AcquireLock(rootContext)
		if err != nil {
			return err
		}
		defer func() {
			releaseContext, cancelRelease := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancelRelease()
			botLock.Release(releaseContext)
		}()

		telegramClient, err := telegram.NewClient("https://api.telegram.org", cfg.TelegramBotToken, &http.Client{Timeout: 35 * time.Second})
		if err != nil {
			return err
		}
		llmClient, err := llm.NewOpenAIClient(cfg.LLMBaseURL, cfg.LLMAPIKey, &http.Client{Timeout: cfg.LLMTimeout})
		if err != nil {
			return err
		}
		poller := botdomain.NewPoller(telegramClient, botRepository, cfg.Location, cfg.FamilyUserIDs)
		processor := appbot.NewProcessor(botRepository, llmClient, ownerService, telegramClient, cfg.LLMModel, appbot.ProcessorOptions{
			MaxLLMCalls: cfg.AgentMaxLLMCalls, MaxToolCalls: cfg.AgentMaxToolCalls, MaxContextBytes: cfg.AgentMaxContextBytes,
		})
		go func() {
			if err := appbot.Run(rootContext, poller, processor, botRepository, botLock, cfg.AgentTurnTimeout); err != nil {
				botError <- err
			}
		}()
	}

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
	case err := <-botError:
		shutdownContext, cancelShutdown := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancelShutdown()
		_ = server.Shutdown(shutdownContext)
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
