package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

type Client struct {
	baseURL    string
	token      string
	httpClient *http.Client
}

type Bot struct {
	ID       int64  `json:"id"`
	Username string `json:"username"`
}

type User struct {
	ID        int64  `json:"id"`
	FirstName string `json:"first_name"`
	Username  string `json:"username"`
}

type Chat struct {
	ID   int64  `json:"id"`
	Type string `json:"type"`
}

type Message struct {
	MessageID int64  `json:"message_id"`
	From      *User  `json:"from"`
	Chat      Chat   `json:"chat"`
	Date      int64  `json:"date"`
	Text      string `json:"text"`
}

type Update struct {
	UpdateID int64    `json:"update_id"`
	Message  *Message `json:"message"`
}

type WebhookInfo struct {
	URL string `json:"url"`
}

type SentMessage struct {
	MessageID int64 `json:"message_id"`
}

func NewClient(baseURL, token string, httpClient *http.Client) (*Client, error) {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	token = strings.TrimSpace(token)
	if baseURL == "" || token == "" {
		return nil, errors.New("Telegram base URL and bot token are required")
	}
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return nil, errors.New("Telegram base URL is invalid")
	}
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return &Client{baseURL: baseURL, token: token, httpClient: httpClient}, nil
}

func (c *Client) GetMe(ctx context.Context) (Bot, error) {
	result, err := request[Bot](ctx, c, "getMe", struct{}{})
	if err != nil {
		return Bot{}, err
	}
	if result.ID <= 0 {
		return Bot{}, errors.New("Telegram getMe returned an invalid bot ID")
	}
	return result, nil
}

func (c *Client) GetWebhookInfo(ctx context.Context) (WebhookInfo, error) {
	return request[WebhookInfo](ctx, c, "getWebhookInfo", struct{}{})
}

func (c *Client) GetUpdates(ctx context.Context, offset int64, timeoutSeconds int) ([]Update, error) {
	if offset < 0 || timeoutSeconds < 0 || timeoutSeconds > 50 {
		return nil, errors.New("invalid Telegram polling arguments")
	}
	return request[[]Update](ctx, c, "getUpdates", map[string]any{
		"offset": offset, "timeout": timeoutSeconds, "allowed_updates": []string{"message"},
	})
}

func (c *Client) SendMessage(ctx context.Context, chatID int64, text string) (SentMessage, error) {
	text = strings.TrimSpace(text)
	if chatID == 0 || text == "" || len([]rune(text)) > 4096 {
		return SentMessage{}, errors.New("invalid Telegram message")
	}
	return request[SentMessage](ctx, c, "sendMessage", map[string]any{"chat_id": chatID, "text": text})
}

func request[T any](ctx context.Context, client *Client, method string, payload any) (T, error) {
	var zero T
	body, err := json.Marshal(payload)
	if err != nil {
		return zero, fmt.Errorf("encode Telegram %s request: %w", method, err)
	}
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, client.baseURL+"/bot"+client.token+"/"+method, bytes.NewReader(body))
	if err != nil {
		return zero, fmt.Errorf("create Telegram %s request: %w", method, err)
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	response, err := client.httpClient.Do(httpRequest)
	if err != nil {
		return zero, fmt.Errorf("Telegram %s request failed", method)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return zero, fmt.Errorf("Telegram %s returned HTTP %d", method, response.StatusCode)
	}
	var envelope struct {
		OK          bool   `json:"ok"`
		Description string `json:"description"`
		Result      T      `json:"result"`
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, 4<<20))
	if err := decoder.Decode(&envelope); err != nil {
		return zero, fmt.Errorf("decode Telegram %s response: %w", method, err)
	}
	if !envelope.OK {
		return zero, fmt.Errorf("Telegram %s failed: %s", method, envelope.Description)
	}
	return envelope.Result, nil
}
