package llm

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
	payload := map[string]any{
		"model": model,
		"messages": []map[string]string{
			{"role": "system", "content": "Always call create_expense for a valid expense. Do not answer with prose."},
			{"role": "user", "content": "кофе 350, дата 2026-10-15"},
		},
		"tools": []map[string]any{{
			"type": "function",
			"function": map[string]any{
				"name":        "create_expense",
				"description": "Save one expense for the authenticated user",
				"parameters": map[string]any{
					"type":                 "object",
					"additionalProperties": false,
					"properties": map[string]any{
						"amount":   map[string]any{"type": "string"},
						"category": map[string]any{"type": "string", "enum": []string{"food", "transport", "housing", "entertainment", "health", "shopping", "other"}},
						"spent_at": map[string]any{"type": "string"},
						"comment":  map[string]any{"type": "string"},
					},
					"required": []string{"amount", "category", "spent_at"},
				},
			},
		}},
		"tool_choice": map[string]any{"type": "function", "function": map[string]string{"name": "create_expense"}},
		"temperature": 0,
	}
	applyChatCompatibility(payload, model)
	body, err := json.Marshal(payload)
	if err != nil {
		return ToolCall{}, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return ToolCall{}, err
	}
	request.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		request.Header.Set("Authorization", "Bearer "+c.apiKey)
	}

	response, err := c.httpClient.Do(request)
	if err != nil {
		return ToolCall{}, fmt.Errorf("LLM tool probe: %w", err)
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return ToolCall{}, fmt.Errorf("read LLM response: %w", err)
	}
	if response.StatusCode != http.StatusOK {
		return ToolCall{}, fmt.Errorf("LLM tool probe returned HTTP %d: %s", response.StatusCode, compactError(responseBody))
	}

	var envelope struct {
		Choices []struct {
			Message struct {
				ToolCalls []struct {
					ID       string `json:"id"`
					Function struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(responseBody, &envelope); err != nil {
		return ToolCall{}, fmt.Errorf("decode LLM response: %w", err)
	}
	if len(envelope.Choices) == 0 || len(envelope.Choices[0].Message.ToolCalls) == 0 {
		return ToolCall{}, errors.New("LLM response did not contain a tool call")
	}
	first := envelope.Choices[0].Message.ToolCalls[0]
	arguments := json.RawMessage(first.Function.Arguments)
	if first.Function.Name != "create_expense" || !json.Valid(arguments) {
		return ToolCall{}, errors.New("LLM response contained an invalid create_expense call")
	}
	return ToolCall{ID: first.ID, Name: first.Function.Name, Arguments: arguments}, nil
}

func applyChatCompatibility(payload map[string]any, model string) {
	normalized := strings.ToLower(strings.TrimSpace(model))
	if strings.Contains(normalized, "gpt-5.6-") {
		payload["reasoning_effort"] = "none"
	}
}

func compactError(body []byte) string {
	value := strings.Join(strings.Fields(string(body)), " ")
	if len(value) > 300 {
		return value[:300]
	}
	return value
}
