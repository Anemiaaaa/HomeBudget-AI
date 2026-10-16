//go:build integration

package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/Anemiaaaa/HomeBudget-AI/internal/bot"
	"github.com/Anemiaaaa/HomeBudget-AI/internal/expense"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestBotRepositoryDurableInboxAndAtomicCompletion(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not configured")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, cleanupErr := pool.Exec(context.Background(), `TRUNCATE bot_updates, bot_sessions, expenses, users RESTART IDENTITY CASCADE;
			UPDATE bot_polling_state SET next_offset = 0, updated_at = now() WHERE singleton = TRUE`)
		pool.Close()
		if cleanupErr != nil {
			t.Errorf("clean bot integration fixtures: %v", cleanupErr)
		}
	})
	if _, err := pool.Exec(ctx, `TRUNCATE bot_updates, bot_sessions, expenses, users RESTART IDENTITY CASCADE;
		UPDATE bot_polling_state SET next_offset = 0, updated_at = now() WHERE singleton = TRUE`); err != nil {
		t.Fatal(err)
	}

	repository := NewBotRepository(pool)
	lock, err := repository.AcquireLock(ctx)
	if err != nil {
		t.Fatalf("AcquireLock() error = %v", err)
	}
	if _, err := repository.AcquireLock(ctx); !errors.Is(err, ErrBotAlreadyRunning) {
		t.Fatalf("second AcquireLock() error = %v", err)
	}
	lock.Release(ctx)
	lock, err = repository.AcquireLock(ctx)
	if err != nil {
		t.Fatalf("AcquireLock(after release) error = %v", err)
	}
	lock.Release(ctx)

	now := time.Date(2026, 10, 15, 15, 0, 0, 0, time.UTC)
	reference := integrationDate(t, "2026-10-15")
	userID, chatID, messageID, text := int64(101), int64(501), int64(11), "кофе 350"
	pending := bot.IncomingUpdate{
		UpdateID: 100, UserID: &userID, ChatID: &chatID, MessageID: &messageID,
		MessageAt: &now, ReferenceDate: &reference, Text: &text, Status: bot.StatusPending,
	}
	ignored := bot.IncomingUpdate{UpdateID: 101, Status: bot.StatusIgnored}
	inserted, err := repository.Ingest(ctx, []bot.IncomingUpdate{pending, ignored}, 102)
	if err != nil || inserted != 2 {
		t.Fatalf("Ingest() = %d, %v", inserted, err)
	}
	inserted, err = repository.Ingest(ctx, []bot.IncomingUpdate{pending}, 102)
	if err != nil || inserted != 0 {
		t.Fatalf("duplicate Ingest() = %d, %v", inserted, err)
	}
	offset, err := repository.NextOffset(ctx)
	if err != nil || offset != 102 {
		t.Fatalf("NextOffset() = %d, %v", offset, err)
	}

	claimed, err := repository.ClaimNext(ctx)
	if err != nil {
		t.Fatalf("ClaimNext() error = %v", err)
	}
	if claimed.UpdateID != 100 || claimed.Status != bot.StatusProcessing || claimed.ProcessingAttempts != 1 || claimed.UserID == nil || *claimed.UserID != 101 {
		t.Fatalf("ClaimNext() = %+v", claimed)
	}
	if _, err := repository.ClaimNext(ctx); !errors.Is(err, ErrNoBotWork) {
		t.Fatalf("second ClaimNext() error = %v", err)
	}

	err = repository.Complete(ctx, claimed.UpdateID, func(ctx context.Context, unit *CompletionUnit) (json.RawMessage, string, error) {
		created, err := unit.Expenses.Create(ctx, userID, expense.CreateFields{
			Amount: "350", Category: expense.CategoryFood, SpentAt: "сегодня", Comment: &text,
		}, reference)
		if err != nil {
			return nil, "", err
		}
		result, err := json.Marshal(map[string]any{"expense_id": created.ID, "version": created.Version})
		return result, "Добавлено: кофе — 350,00 ₽", err
	})
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	delivery, err := repository.NextDelivery(ctx)
	if err != nil || delivery.UpdateID != 100 || delivery.ChatID != chatID || delivery.Text == "" {
		t.Fatalf("NextDelivery() = %+v, %v", delivery, err)
	}
	if err := repository.MarkDeliveryFailed(ctx, delivery.UpdateID, "timeout", time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("MarkDeliveryFailed() error = %v", err)
	}
	if _, err := repository.NextDelivery(ctx); !errors.Is(err, ErrNoBotWork) {
		t.Fatalf("early retry NextDelivery() error = %v", err)
	}
	if _, err := pool.Exec(ctx, "UPDATE bot_updates SET next_attempt_at = now() WHERE update_id = 100"); err != nil {
		t.Fatal(err)
	}
	delivery, err = repository.NextDelivery(ctx)
	if err != nil || delivery.Attempts != 1 {
		t.Fatalf("retry NextDelivery() = %+v, %v", delivery, err)
	}
	if err := repository.MarkDelivered(ctx, delivery.UpdateID); err != nil {
		t.Fatalf("MarkDelivered() error = %v", err)
	}
	stored, err := repository.Get(ctx, 100)
	if err != nil || stored.Status != bot.StatusDelivered || stored.ReplyText == nil || !json.Valid(stored.Result) {
		t.Fatalf("Get(completed) = %+v, %v", stored, err)
	}
	history, err := repository.LoadHistory(ctx, userID, chatID, 102, 6)
	if err != nil || len(history) != 1 || history[0].UserText != text || history[0].AssistantText != "Добавлено: кофе — 350,00 ₽" {
		t.Fatalf("LoadHistory() = %+v, %v", history, err)
	}
	otherHistory, err := repository.LoadHistory(ctx, 202, chatID, 102, 6)
	if err != nil || len(otherHistory) != 0 {
		t.Fatalf("LoadHistory(other owner) = %+v, %v", otherHistory, err)
	}

	rollbackMessageID, rollbackText := int64(12), "чай 100"
	rollbackUpdate := bot.IncomingUpdate{
		UpdateID: 102, UserID: &userID, ChatID: &chatID, MessageID: &rollbackMessageID,
		MessageAt: &now, ReferenceDate: &reference, Text: &rollbackText, Status: bot.StatusPending,
	}
	if _, err := repository.Ingest(ctx, []bot.IncomingUpdate{rollbackUpdate}, 103); err != nil {
		t.Fatal(err)
	}
	claimed, err = repository.ClaimNext(ctx)
	if err != nil || claimed.UpdateID != 102 {
		t.Fatalf("ClaimNext(rollback) = %+v, %v", claimed, err)
	}
	syntheticFailure := errors.New("synthetic completion failure")
	err = repository.Complete(ctx, claimed.UpdateID, func(ctx context.Context, unit *CompletionUnit) (json.RawMessage, string, error) {
		if _, err := unit.Expenses.Create(ctx, userID, expense.CreateFields{
			Amount: "100", Category: expense.CategoryFood, SpentAt: "сегодня", Comment: &rollbackText,
		}, reference); err != nil {
			return nil, "", err
		}
		return nil, "", syntheticFailure
	})
	if !errors.Is(err, syntheticFailure) {
		t.Fatalf("rollback Complete() error = %v", err)
	}
	var expenseCount int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM expenses").Scan(&expenseCount); err != nil || expenseCount != 1 {
		t.Fatalf("expense count after rollback = %d, %v", expenseCount, err)
	}
	recovered, err := repository.RecoverProcessing(ctx)
	if err != nil || recovered != 1 {
		t.Fatalf("RecoverProcessing() = %d, %v", recovered, err)
	}

	pendingAction := json.RawMessage(`{"kind":"confirm_delete","expense_id":"7","expected_version":"2"}`)
	if err := repository.SaveSession(ctx, userID, chatID, pendingAction, now.Add(time.Hour)); err != nil {
		t.Fatalf("SaveSession() error = %v", err)
	}
	session, err := repository.LoadSession(ctx, userID, chatID)
	var loadedAction map[string]any
	decodeErr := json.Unmarshal(session.PendingAction, &loadedAction)
	if err != nil || decodeErr != nil || loadedAction["kind"] != "confirm_delete" || session.ExpiresAt == nil {
		t.Fatalf("LoadSession() = %+v, %v", session, err)
	}
	if err := repository.ClearSession(ctx, userID, chatID); err != nil {
		t.Fatalf("ClearSession() error = %v", err)
	}
	session, err = repository.LoadSession(ctx, userID, chatID)
	if err != nil || session.PendingAction != nil {
		t.Fatalf("LoadSession(cleared) = %+v, %v", session, err)
	}
}
