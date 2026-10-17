package llm

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestProbeExpenseTool(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v1/chat/completions" {
			t.Fatalf("path = %q", request.URL.Path)
		}
		if got := request.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Fatalf("Authorization = %q", got)
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"choices":[{"message":{"tool_calls":[{"id":"call_1","function":{"name":"create_expense","arguments":"{\"amount\":\"350.00\",\"category\":\"food\",\"spent_at\":\"2026-10-15\",\"comment\":\"кофе\"}"}}]}}]}`))
	}))
	defer server.Close()

	client, err := NewOpenAIClient(server.URL+"/v1", "test-key", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	call, err := client.ProbeExpenseTool(context.Background(), "test-model")
	if err != nil {
		t.Fatalf("ProbeExpenseTool() error = %v", err)
	}
	if call.Name != "create_expense" {
		t.Fatalf("tool name = %q", call.Name)
	}
}

func TestOpenAIClientRejectsRemotePlainHTTP(t *testing.T) {
	t.Parallel()
	if _, err := NewOpenAIClient("http://provider.example/v1", "", nil); err == nil {
		t.Fatal("remote plain HTTP URL was accepted")
	}
	if _, err := NewOpenAIClient("http://localhost:11434/v1", "", nil); err != nil {
		t.Fatalf("loopback HTTP URL was rejected: %v", err)
	}
}
