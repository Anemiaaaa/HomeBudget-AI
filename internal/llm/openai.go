package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/Anemiaaaa/HomeBudget-AI/internal/agent"
)

type OpenAIClient struct {
	baseURL    string
	apiKey     string
	httpClient *http.Client
}

type ToolCall struct {
	ID        string
	Name      string
	Arguments json.RawMessage
}

func NewOpenAIClient(baseURL, apiKey string, httpClient *http.Client) (*OpenAIClient, error) {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return nil, errors.New("LLM base URL is invalid")
	}
	if parsed.Scheme != "https" && !(parsed.Scheme == "http" && isLoopbackHost(parsed.Hostname())) {
		return nil, errors.New("LLM base URL must use HTTPS; HTTP is allowed only for loopback hosts")
	}
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return &OpenAIClient{baseURL: baseURL, apiKey: strings.TrimSpace(apiKey), httpClient: httpClient}, nil
}

func isLoopbackHost(host string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	return host == "localhost" || host == "127.0.0.1" || host == "::1"
}

func (c *OpenAIClient) ProbeExpenseTool(ctx context.Context, model string) (ToolCall, error) {
	if strings.TrimSpace(model) == "" {
		return ToolCall{}, errors.New("LLM model is required")
	}
	response, err := c.Chat(ctx, ChatRequest{
		Model: model,
		Messages: []Message{
			{Role: "system", Content: "Always call create_expense for a valid expense. Do not answer with prose."},
			{Role: "user", Content: "кофе 350, дата 2026-10-15"},
		},
		Tools: agent.Definitions(),
	})
	if err != nil {
		return ToolCall{}, fmt.Errorf("LLM tool probe: %w", err)
	}
	if len(response.ToolCalls) == 0 {
		return ToolCall{}, errors.New("LLM response did not contain a tool call")
	}
	first := response.ToolCalls[0]
	if first.Name != "create_expense" || !json.Valid(first.Arguments) {
		return ToolCall{}, errors.New("LLM response contained an invalid create_expense call")
	}
	return first, nil
}

func applyChatCompatibility(payload map[string]any, model string) {
	normalized := strings.ToLower(strings.TrimSpace(model))
	if strings.Contains(normalized, "gpt-5.6-") {
		payload["reasoning_effort"] = "none"
		if maxTokens, exists := payload["max_tokens"]; exists {
			delete(payload, "max_tokens")
			payload["max_completion_tokens"] = maxTokens
		}
	}
}

func compactError(body []byte) string {
	value := strings.Join(strings.Fields(string(body)), " ")
	if len(value) > 300 {
		return value[:300]
	}
	return value
}
