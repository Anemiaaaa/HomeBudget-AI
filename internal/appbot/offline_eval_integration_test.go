//go:build integration

package appbot

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/Anemiaaaa/HomeBudget-AI/internal/bot"
	"github.com/Anemiaaaa/HomeBudget-AI/internal/expense"
	"github.com/Anemiaaaa/HomeBudget-AI/internal/llm"
	postgresstore "github.com/Anemiaaaa/HomeBudget-AI/internal/storage/postgres"
	"github.com/jackc/pgx/v5/pgxpool"
)

type offlineEvalCase struct {
	ID            string               `json:"id"`
	Initial       []offlineEvalExpense `json:"initial"`
	Steps         []offlineEvalStep    `json:"steps"`
	OwnerExpenses []offlineEvalExpense `json:"owner_expenses"`
}

type offlineEvalExpense struct {
	OwnerID  int64            `json:"owner_id"`
	Amount   string           `json:"amount"`
	Category expense.Category `json:"category"`
	SpentAt  string           `json:"spent_at"`
	Comment  string           `json:"comment"`
}

type offlineEvalStep struct {
	Input         string                `json:"input"`
	Responses     []offlineEvalResponse `json:"responses"`
	TotalCount    int                   `json:"total_count"`
	ReplyContains []string              `json:"reply_contains"`
	ReplyExcludes []string              `json:"reply_excludes"`
}

type offlineEvalResponse struct {
	Content string            `json:"content"`
	Tools   []offlineEvalTool `json:"tools"`
}

type offlineEvalTool struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

func TestOfflineAgentEval(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not configured")
	}
	cases := loadOfflineEvalCases(t)
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)

	for _, testCase := range cases {
		t.Run(testCase.ID, func(t *testing.T) {
			resetOfflineEval(t, ctx, pool)
			store := postgresstore.NewBotRepository(pool)
			reader := expense.NewOwnerService(postgresstore.NewExpenseRepository(pool))
			provider := &scriptedProvider{}
			processor := NewProcessor(store, provider, reader, &senderStub{}, "scripted")
			processor.now = func() time.Time { return time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC) }
			reference := mustIntegrationDate(t, "2026-10-15")

			for _, item := range testCase.Initial {
				ownerID := item.OwnerID
				if ownerID == 0 {
					ownerID = 101
				}
				if _, err := reader.Create(ctx, ownerID, expense.CreateFields{
					Amount: item.Amount, Category: item.Category, SpentAt: item.SpentAt, Comment: stringPointer(item.Comment),
				}, reference); err != nil {
					t.Fatalf("create initial expense: %v", err)
				}
			}

			for index, step := range testCase.Steps {
				for _, response := range step.Responses {
					converted := llm.ChatResponse{Content: response.Content}
					for _, call := range response.Tools {
						converted.ToolCalls = append(converted.ToolCalls, llm.ToolCall{ID: call.ID, Name: call.Name, Arguments: call.Arguments})
					}
					provider.responses = append(provider.responses, converted)
				}
				updateID := int64(index + 1)
				ingestMessage(t, ctx, store, updateID, 101, 501, step.Input, "2026-10-15")
				if err := processor.ProcessOne(ctx); err != nil {
					t.Fatalf("ProcessOne(%q): %v", step.Input, err)
				}
				stored, err := store.Get(ctx, updateID)
				if err != nil || stored.Status != bot.StatusReadyToSend || stored.ReplyText == nil {
					t.Fatalf("stored update = %+v, %v", stored, err)
				}
				for _, expected := range step.ReplyContains {
					if !strings.Contains(*stored.ReplyText, expected) {
						t.Errorf("reply %q does not contain %q", *stored.ReplyText, expected)
					}
				}
				for _, forbidden := range step.ReplyExcludes {
					if strings.Contains(*stored.ReplyText, forbidden) {
						t.Errorf("reply %q contains forbidden %q", *stored.ReplyText, forbidden)
					}
				}
				var total int
				if err := pool.QueryRow(ctx, "SELECT count(*) FROM expenses").Scan(&total); err != nil || total != step.TotalCount {
					t.Errorf("expense count = %d, %v; want %d", total, err, step.TotalCount)
				}
			}
			if len(provider.responses) != 0 {
				t.Fatalf("%d scripted responses were not consumed", len(provider.responses))
			}
			assertOfflineExpenses(t, ctx, reader, testCase.OwnerExpenses)
		})
	}
}

func loadOfflineEvalCases(t *testing.T) []offlineEvalCase {
	t.Helper()
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate offline eval fixtures")
	}
	data, err := os.ReadFile(filepath.Join(filepath.Dir(currentFile), "..", "..", "evals", "offline_cases.json"))
	if err != nil {
		t.Fatal(err)
	}
	var cases []offlineEvalCase
	if err := json.Unmarshal(data, &cases); err != nil {
		t.Fatal(err)
	}
	if len(cases) == 0 {
		t.Fatal("offline eval fixture is empty")
	}
	return cases
}

func resetOfflineEval(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	if _, err := pool.Exec(ctx, `TRUNCATE bot_updates, bot_sessions, expenses, users RESTART IDENTITY CASCADE;
		UPDATE bot_polling_state SET next_offset = 0, updated_at = now() WHERE singleton = TRUE`); err != nil {
		t.Fatal(err)
	}
}

func assertOfflineExpenses(t *testing.T, ctx context.Context, reader *expense.OwnerService, expected []offlineEvalExpense) {
	t.Helper()
	result, err := reader.List(ctx, 101, expense.ListFilter{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	actual := make([]string, 0, len(result.Items))
	for _, item := range result.Items {
		comment := ""
		if item.Comment != nil {
			comment = *item.Comment
		}
		actual = append(actual, fmt.Sprintf("%s|%s|%s|%s", item.Amount.String(), item.Category, item.SpentAt.String(), comment))
	}
	want := make([]string, 0, len(expected))
	for _, item := range expected {
		want = append(want, fmt.Sprintf("%s|%s|%s|%s", item.Amount, item.Category, item.SpentAt, item.Comment))
	}
	sort.Strings(actual)
	sort.Strings(want)
	if strings.Join(actual, "\n") != strings.Join(want, "\n") {
		t.Fatalf("owner expenses = %v, want %v", actual, want)
	}
}
