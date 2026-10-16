package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Anemiaaaa/HomeBudget-AI/internal/agent"
)

func TestChatRetriesAndDecodesToolCall(t *testing.T) {
	t.Parallel()
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		attempts++
		if attempts == 1 {
			writer.Header().Set("Retry-After", "0")
			http.Error(writer, "temporary", http.StatusTooManyRequests)
			return
		}
		var body struct {
			Model     string             `json:"model"`
			Messages  []wireMessage      `json:"messages"`
			Tools     []agent.Definition `json:"tools"`
			MaxTokens int                `json:"max_tokens"`
		}
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body.Model != "test-model" || len(body.Messages) != 2 || len(body.Tools) != 6 || body.MaxTokens != 800 {
			t.Errorf("chat request = %+v", body)
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{
			"choices":[{"message":{"content":null,"tool_calls":[{"id":"call_1","type":"function","function":{"name":"create_expense","arguments":"{\"amount\":\"350\",\"category\":\"food\",\"spent_at\":\"сегодня\"}"}}]}}],
			"usage":{"prompt_tokens":100,"completion_tokens":20,"total_tokens":120}
		}`))
	}))
	defer server.Close()

	client, err := NewOpenAIClient(server.URL, "", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.Chat(context.Background(), ChatRequest{
		Model: "test-model", Tools: agent.Definitions(),
		Messages: []Message{{Role: "system", Content: "rules"}, {Role: "user", Content: "кофе 350"}},
	})
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}
	if attempts != 2 || len(response.ToolCalls) != 1 || response.ToolCalls[0].Name != "create_expense" || response.Usage.TotalTokens != 120 {
		t.Fatalf("Chat() = %+v, attempts = %d", response, attempts)
	}
}

func TestChatRejectsMalformedToolArguments(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"choices":[{"message":{"tool_calls":[{"id":"1","type":"function","function":{"name":"create_expense","arguments":"{"}}]}}]}`))
	}))
	defer server.Close()
	client, _ := NewOpenAIClient(server.URL, "", server.Client())
	_, err := client.Chat(context.Background(), ChatRequest{Model: "model", Tools: agent.Definitions(), Messages: []Message{{Role: "user", Content: "test"}}})
	if err == nil {
		t.Fatal("Chat() error = nil")
	}
}
