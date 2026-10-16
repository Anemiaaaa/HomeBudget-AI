package appbot

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/Anemiaaaa/HomeBudget-AI/internal/bot"
	postgresstore "github.com/Anemiaaaa/HomeBudget-AI/internal/storage/postgres"
)

type ProcessingRecovery interface {
	RecoverProcessing(context.Context) (int64, error)
}

func Run(ctx context.Context, poller *bot.Poller, processor *Processor, recovery ProcessingRecovery, lock *postgresstore.BotLock, turnTimeout time.Duration) error {
	if turnTimeout <= 0 {
		turnTimeout = time.Minute
	}
	if err := poller.CheckWebhook(ctx); err != nil {
		return err
	}
	recovered, err := recovery.RecoverProcessing(ctx)
	if err != nil {
		return err
	}
	if recovered > 0 {
		slog.Info("recovered bot updates", "count", recovered)
	}

	runContext, cancel := context.WithCancel(ctx)
	defer cancel()
	fatal := make(chan error, 1)
	go pollLoop(runContext, poller)
	go workLoop(runContext, processor, recovery, turnTimeout)
	go monitorLock(runContext, lock, fatal)

	select {
	case <-ctx.Done():
		return nil
	case err := <-fatal:
		return err
	}
}

func pollLoop(ctx context.Context, poller *bot.Poller) {
	for ctx.Err() == nil {
		if _, err := poller.PollOnce(ctx); err != nil && !errors.Is(err, context.Canceled) {
			slog.Error("Telegram polling failed", "error", err)
			wait(ctx, 2*time.Second)
		}
	}
}

func workLoop(ctx context.Context, processor *Processor, recovery ProcessingRecovery, turnTimeout time.Duration) {
	for ctx.Err() == nil {
		didWork := false
		turnContext, cancel := context.WithTimeout(ctx, turnTimeout)
		err := processor.ProcessOne(turnContext)
		cancel()
		switch {
		case err == nil:
			didWork = true
		case errors.Is(err, postgresstore.ErrNoBotWork):
		case errors.Is(err, context.Canceled):
			return
		default:
			slog.Error("bot update processing failed", "error", err)
			if _, recoveryErr := recovery.RecoverProcessing(ctx); recoveryErr != nil {
				slog.Error("bot update recovery failed", "error", recoveryErr)
			}
		}

		err = processor.DeliverOne(ctx)
		switch {
		case err == nil:
			didWork = true
		case errors.Is(err, postgresstore.ErrNoBotWork):
		case errors.Is(err, context.Canceled):
			return
		default:
			slog.Error("bot reply delivery failed", "error", err)
		}
		if !didWork {
			wait(ctx, 250*time.Millisecond)
		}
	}
}

func monitorLock(ctx context.Context, lock *postgresstore.BotLock, fatal chan<- error) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			pingContext, cancel := context.WithTimeout(ctx, 3*time.Second)
			err := lock.Ping(pingContext)
			cancel()
			if err != nil {
				select {
				case fatal <- err:
				default:
				}
				return
			}
		}
	}
}

func wait(ctx context.Context, duration time.Duration) {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
	case <-timer.C:
	}
}
