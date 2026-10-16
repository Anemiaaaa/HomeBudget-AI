package telegram

import (
	"context"
	"encoding/json"
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

func TestPollingWebhookAndSendMessage(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost || request.Header.Get("Content-Type") != "application/json" {
			t.Errorf("request = %s, Content-Type = %q", request.Method, request.Header.Get("Content-Type"))
		}
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/bottest-token/getWebhookInfo":
			_, _ = writer.Write([]byte(`{"ok":true,"result":{"url":""}}`))
		case "/bottest-token/getUpdates":
			var body struct {
				Offset         int64    `json:"offset"`
				Timeout        int      `json:"timeout"`
				AllowedUpdates []string `json:"allowed_updates"`
			}
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if body.Offset != 42 || body.Timeout != 25 || len(body.AllowedUpdates) != 1 || body.AllowedUpdates[0] != "message" {
				t.Errorf("getUpdates body = %+v", body)
			}
			_, _ = writer.Write([]byte(`{"ok":true,"result":[{"update_id":42,"message":{"message_id":5,"from":{"id":101,"first_name":"Amir"},"chat":{"id":501,"type":"private"},"date":1792076400,"text":"кофе 350"}}]}`))
		case "/bottest-token/sendMessage":
			var body struct {
				ChatID int64  `json:"chat_id"`
				Text   string `json:"text"`
			}
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if body.ChatID != 501 || body.Text != "Готово" {
				t.Errorf("sendMessage body = %+v", body)
			}
			_, _ = writer.Write([]byte(`{"ok":true,"result":{"message_id":6}}`))
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	client, err := NewClient(server.URL, "test-token", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	webhook, err := client.GetWebhookInfo(context.Background())
	if err != nil || webhook.URL != "" {
		t.Fatalf("GetWebhookInfo() = %+v, %v", webhook, err)
	}
	updates, err := client.GetUpdates(context.Background(), 42, 25)
	if err != nil || len(updates) != 1 || updates[0].Message == nil || updates[0].Message.From == nil || updates[0].Message.From.ID != 101 {
		t.Fatalf("GetUpdates() = %+v, %v", updates, err)
	}
	sent, err := client.SendMessage(context.Background(), 501, "Готово")
	if err != nil || sent.MessageID != 6 {
		t.Fatalf("SendMessage() = %+v, %v", sent, err)
	}
}

func TestClientValidatesPollingAndMessageArguments(t *testing.T) {
	t.Parallel()
	client, err := NewClient("https://api.telegram.org", "test-token", http.DefaultClient)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.GetUpdates(context.Background(), -1, 25); err == nil {
		t.Fatal("GetUpdates() error = nil")
	}
	if _, err := client.SendMessage(context.Background(), 0, ""); err == nil {
		t.Fatal("SendMessage() error = nil")
	}
}
