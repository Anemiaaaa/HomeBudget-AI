package telegram

import (
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
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/bot"+c.token+"/getMe", nil)
	if err != nil {
		return Bot{}, err
	}
	response, err := c.httpClient.Do(request)
	if err != nil {
		return Bot{}, fmt.Errorf("Telegram getMe: %w", err)
	}
	defer response.Body.Close()

	body, err := io.ReadAll(io.LimitReader(response.Body, 64<<10))
	if err != nil {
		return Bot{}, fmt.Errorf("read Telegram getMe response: %w", err)
	}
	if response.StatusCode != http.StatusOK {
		return Bot{}, fmt.Errorf("Telegram getMe returned HTTP %d", response.StatusCode)
	}
	var envelope struct {
		OK          bool   `json:"ok"`
		Description string `json:"description"`
		Result      Bot    `json:"result"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return Bot{}, fmt.Errorf("decode Telegram getMe response: %w", err)
	}
	if !envelope.OK {
		return Bot{}, fmt.Errorf("Telegram getMe failed: %s", envelope.Description)
	}
	if envelope.Result.ID <= 0 {
		return Bot{}, errors.New("Telegram getMe returned an invalid bot ID")
	}
	return envelope.Result, nil
}
