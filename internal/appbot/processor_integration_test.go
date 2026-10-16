//go:build integration

package appbot

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Anemiaaaa/HomeBudget-AI/internal/bot"
	"github.com/Anemiaaaa/HomeBudget-AI/internal/expense"
	"github.com/Anemiaaaa/HomeBudget-AI/internal/llm"
	postgresstore "github.com/Anemiaaaa/HomeBudget-AI/internal/storage/postgres"
	"github.com/Anemiaaaa/HomeBudget-AI/internal/telegram"
	"github.com/jackc/pgx/v5/pgxpool"
)

type scriptedProvider struct {
	responses []llm.ChatResponse
	calls     []llm.ChatRequest
}

func (p *scriptedProvider) Chat(_ context.Context, request llm.ChatRequest) (llm.ChatResponse, error) {
	p.calls = append(p.calls, request)
	if len(p.responses) == 0 {
		panic("unexpected provider call")
	}
	response := p.responses[0]
	p.responses = p.responses[1:]
	return response, nil
}

type senderStub struct{ messages []string }

func (s *senderStub) SendMessage(_ context.Context, _ int64, text string) (telegram.SentMessage, error) {
	s.messages = append(s.messages, text)
	return telegram.SentMessage{MessageID: int64(len(s.messages))}, nil
}

func TestProcessorCreateDeleteConfirmationAndDelivery(t *testing.T) {
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
			t.Errorf("clean processor fixtures: %v", cleanupErr)
		}
	})
	if _, err := pool.Exec(ctx, `TRUNCATE bot_updates, bot_sessions, expenses, users RESTART IDENTITY CASCADE;
		UPDATE bot_polling_state SET next_offset = 0, updated_at = now() WHERE singleton = TRUE`); err != nil {
		t.Fatal(err)
	}

	store := postgresstore.NewBotRepository(pool)
	reader := expense.NewOwnerService(postgresstore.NewExpenseRepository(pool))
	provider := &scriptedProvider{responses: []llm.ChatResponse{{ToolCalls: []llm.ToolCall{{
		ID: "create_1", Name: "create_expense", Arguments: json.RawMessage(`{"amount":"350","category":"food","spent_at":"сегодня","comment":"кофе"}`),
	}}}}}
	sender := &senderStub{}
	processor := NewProcessor(store, provider, reader, sender, "scripted")
	processor.now = func() time.Time { return time.Date(2026, 10, 15, 15, 0, 0, 0, time.UTC) }

	ingestMessage(t, ctx, store, 1, 101, 501, "кофе 350", "2026-10-15")
	if err := processor.ProcessOne(ctx); err != nil {
		t.Fatalf("ProcessOne(create) error = %v", err)
	}
	assertExpenseCount(t, ctx, pool, 1)
	createdUpdate, err := store.Get(ctx, 1)
	if err != nil || createdUpdate.Status != bot.StatusReadyToSend || createdUpdate.ReplyText == nil || !strings.Contains(*createdUpdate.ReplyText, "350,00 ₽") {
		t.Fatalf("created update = %+v, %v", createdUpdate, err)
	}
	if err := processor.DeliverOne(ctx); err != nil {
		t.Fatalf("DeliverOne(create) error = %v", err)
	}
	if len(sender.messages) != 1 || !strings.Contains(sender.messages[0], "Добавлено") {
		t.Fatalf("sent create messages = %v", sender.messages)
	}

	provider.responses = append(provider.responses,
		llm.ChatResponse{ToolCalls: []llm.ToolCall{{ID: "read_1", Name: "list_expenses", Arguments: json.RawMessage(`{"query":"кофе"}`)}}},
		llm.ChatResponse{ToolCalls: []llm.ToolCall{{ID: "delete_1", Name: "delete_expense", Arguments: json.RawMessage(`{"expense_id":"1","expected_version":"1"}`)}}},
	)
	ingestMessage(t, ctx, store, 2, 101, 501, "удали кофе", "2026-10-15")
	if err := processor.ProcessOne(ctx); err != nil {
		t.Fatalf("ProcessOne(delete preparation) error = %v", err)
	}
	assertExpenseCount(t, ctx, pool, 1)
	session, err := store.LoadSession(ctx, 101, 501)
	if err != nil || !strings.Contains(string(session.PendingAction), "confirm_delete") {
		t.Fatalf("delete session = %+v, %v", session, err)
	}
	if err := processor.DeliverOne(ctx); err != nil {
		t.Fatalf("DeliverOne(delete preparation) error = %v", err)
	}

	ingestMessage(t, ctx, store, 3, 101, 501, "да, удали", "2026-10-15")
	providerCallsBeforeConfirmation := len(provider.calls)
	if err := processor.ProcessOne(ctx); err != nil {
		t.Fatalf("ProcessOne(confirm delete) error = %v", err)
	}
	if len(provider.calls) != providerCallsBeforeConfirmation {
		t.Fatal("confirmation unexpectedly called the LLM")
	}
	assertExpenseCount(t, ctx, pool, 0)
	session, err = store.LoadSession(ctx, 101, 501)
	if err != nil || session.PendingAction != nil {
		t.Fatalf("session after delete = %+v, %v", session, err)
	}
	if err := processor.DeliverOne(ctx); err != nil {
		t.Fatalf("DeliverOne(delete) error = %v", err)
	}
	if len(sender.messages) != 3 || sender.messages[2] != "Расход №1 удалён." {
		t.Fatalf("sent messages = %v", sender.messages)
	}

	for _, call := range provider.calls {
		for _, message := range call.Messages {
			if strings.Contains(message.Content, "Telegram ID") || strings.Contains(message.Content, "101") {
				t.Fatalf("trusted owner leaked into prompt: %+v", call.Messages)
			}
		}
	}
}

func ingestMessage(t *testing.T, ctx context.Context, store *postgresstore.BotRepository, updateID, userID, chatID int64, text, dateText string) {
	t.Helper()
	reference, err := expense.ParseDate(dateText)
	if err != nil {
		t.Fatal(err)
	}
	messageID, messageAt := updateID, time.Date(2026, 10, 15, 15, 0, 0, 0, time.UTC)
	_, err = store.Ingest(ctx, []bot.IncomingUpdate{{
		UpdateID: updateID, UserID: &userID, ChatID: &chatID, MessageID: &messageID,
		MessageAt: &messageAt, ReferenceDate: &reference, Text: &text, Status: bot.StatusPending,
	}}, updateID+1)
	if err != nil {
		t.Fatal(err)
	}
}

func assertExpenseCount(t *testing.T, ctx context.Context, pool *pgxpool.Pool, want int) {
	t.Helper()
	var count int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM expenses").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != want {
		t.Fatalf("expense count = %d, want %d", count, want)
	}
}
