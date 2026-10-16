package telegram

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGetMe(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/bottest-token/getMe" {
			t.Fatalf("path = %q", request.URL.Path)
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"ok":true,"result":{"id":123,"username":"budget_bot"}}`))
	}))
	defer server.Close()

	client, err := NewClient(server.URL, "test-token", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	bot, err := client.GetMe(context.Background())
	if err != nil {
		t.Fatalf("GetMe() error = %v", err)
	}
	if bot.ID != 123 || bot.Username != "budget_bot" {
		t.Fatalf("GetMe() = %+v", bot)
	}
}
