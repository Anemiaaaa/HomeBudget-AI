//go:build integration

package liveeval

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/Anemiaaaa/HomeBudget-AI/internal/llm"
)

type forbiddenProvider struct{ calls int }

func (p *forbiddenProvider) Chat(context.Context, llm.ChatRequest) (llm.ChatResponse, error) {
	p.calls++
	return llm.ChatResponse{}, errors.New("provider must not be called")
}

type createProvider struct{ calls int }

func (p *createProvider) Chat(context.Context, llm.ChatRequest) (llm.ChatResponse, error) {
	p.calls++
	return llm.ChatResponse{
		ToolCalls: []llm.ToolCall{{
			ID:        "create-1",
			Name:      "create_expense",
			Arguments: []byte(`{"amount":"350","category":"food","spent_at":"2026-10-15","comment":"кофе"}`),
		}},
		Usage: llm.Usage{PromptTokens: 100, CompletionTokens: 20, TotalTokens: 120},
	}, nil
}

func TestRunnerProducesReportWithoutTelegram(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not configured")
	}
	path := filepath.Join(t.TempDir(), "cases.json")
	fixture := `[{
		"id":"E12","intent":"confirmation_guard","reference_date":"2026-10-15",
		"steps":[{"input":"да","count_a":0,"count_b":0,"pending":"none","reply_contains":["Нет действия"],"max_tool_calls":0}]
	}]`
	if err := os.WriteFile(path, []byte(fixture), 0o600); err != nil {
		t.Fatal(err)
	}
	provider := &forbiddenProvider{}
	report, err := Run(context.Background(), Options{
		DatabaseURL: databaseURL, ProviderURL: "https://provider.example/v1", Model: "test",
		Commit: "abc123", Repeats: 2, CasesPath: path, Provider: provider,
	})
	if err != nil {
		t.Fatal(err)
	}
	if provider.calls != 0 || report.TotalRuns != 2 || report.PassedRuns != 2 || report.PassRate != 1 {
		t.Fatalf("provider calls = %d, report = %+v", provider.calls, report)
	}
	if report.Provider != "provider.example" || report.Commit != "abc123" || report.PromptVersion == "" {
		t.Fatalf("report metadata = %+v", report)
	}
}

func TestRunnerMeasuresToolCallsAndUsage(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not configured")
	}
	path := filepath.Join(t.TempDir(), "cases.json")
	fixture := `[{
		"id":"E01","intent":"create","reference_date":"2026-10-15",
		"steps":[{
			"input":"кофе 350","count_a":1,"count_b":0,"pending":"none","max_tool_calls":1,
			"reply_contains":["Добавлено","350,00 ₽"],
			"expenses_a":[{"amount":"350.00","category":"food","spent_at":"2026-10-15","comment":"кофе"}]
		}]
	}]`
	if err := os.WriteFile(path, []byte(fixture), 0o600); err != nil {
		t.Fatal(err)
	}
	provider := &createProvider{}
	report, err := Run(context.Background(), Options{
		DatabaseURL: databaseURL, ProviderURL: "https://provider.example/v1", Model: "test",
		Commit: "abc123", Repeats: 2, CasesPath: path, Provider: provider,
	})
	if err != nil {
		t.Fatal(err)
	}
	if provider.calls != 2 || report.TotalRuns != 2 || report.PassedRuns != 2 || report.PassRate != 1 {
		t.Fatalf("provider calls = %d, report = %+v", provider.calls, report)
	}
	if report.Usage != (llm.Usage{PromptTokens: 200, CompletionTokens: 40, TotalTokens: 240}) {
		t.Fatalf("usage = %+v", report.Usage)
	}
	for _, result := range report.Cases {
		if result.LLMCalls != 1 || len(result.ToolCalls) != 1 || result.ToolCalls[0] != "create_expense" {
			t.Fatalf("case measurements = %+v", result)
		}
	}
}
