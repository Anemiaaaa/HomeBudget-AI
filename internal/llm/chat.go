package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Anemiaaaa/HomeBudget-AI/internal/agent"
)

type Message struct {
	Role       string
	Content    string
	ToolCalls  []ToolCall
	ToolCallID string
}

type ChatRequest struct {
	Model     string
	Messages  []Message
	Tools     []agent.Definition
	MaxTokens int
}

type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

type ChatResponse struct {
	Content   string
	ToolCalls []ToolCall
	Usage     Usage
}

type wireMessage struct {
	Role       string         `json:"role"`
	Content    string         `json:"content,omitempty"`
	ToolCalls  []wireToolCall `json:"tool_calls,omitempty"`
	ToolCallID string         `json:"tool_call_id,omitempty"`
}

type wireToolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function wireFunction `json:"function"`
}

type wireFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

func (c *OpenAIClient) Chat(ctx context.Context, input ChatRequest) (ChatResponse, error) {
	if strings.TrimSpace(input.Model) == "" || len(input.Messages) == 0 || len(input.Tools) == 0 {
		return ChatResponse{}, errors.New("LLM chat requires model, messages and tools")
	}
	if input.MaxTokens == 0 {
		input.MaxTokens = 800
	}
	if input.MaxTokens < 1 || input.MaxTokens > 4096 {
		return ChatResponse{}, errors.New("LLM max tokens is out of range")
	}
	messages := make([]wireMessage, 0, len(input.Messages))
	for _, message := range input.Messages {
		wire := wireMessage{Role: message.Role, Content: message.Content, ToolCallID: message.ToolCallID}
		for _, call := range message.ToolCalls {
			wire.ToolCalls = append(wire.ToolCalls, wireToolCall{
				ID: call.ID, Type: "function", Function: wireFunction{Name: call.Name, Arguments: string(call.Arguments)},
			})
		}
		messages = append(messages, wire)
	}
	payload := map[string]any{
		"model": input.Model, "messages": messages, "tools": input.Tools,
		"tool_choice": "auto", "temperature": 0, "max_tokens": input.MaxTokens,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return ChatResponse{}, fmt.Errorf("encode LLM chat request: %w", err)
	}

	for attempt := 0; attempt < 2; attempt++ {
		response, retryAfter, retryable, err := c.chatAttempt(ctx, body)
		if err == nil {
			return response, nil
		}
		if !retryable || attempt == 1 {
			return ChatResponse{}, err
		}
		if err := waitRetry(ctx, retryAfter); err != nil {
			return ChatResponse{}, err
		}
	}
	return ChatResponse{}, errors.New("LLM chat retry exhausted")
}

func (c *OpenAIClient) chatAttempt(ctx context.Context, body []byte) (ChatResponse, time.Duration, bool, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return ChatResponse{}, 0, false, err
	}
	request.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		request.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
	response, err := c.httpClient.Do(request)
	if err != nil {
		return ChatResponse{}, 0, false, errors.New("LLM chat request failed")
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, 2<<20))
	if err != nil {
		return ChatResponse{}, 0, false, fmt.Errorf("read LLM chat response: %w", err)
	}
	if response.StatusCode != http.StatusOK {
		retryable := response.StatusCode == http.StatusTooManyRequests || response.StatusCode >= 500
		return ChatResponse{}, retryDelay(response.Header.Get("Retry-After")), retryable,
			fmt.Errorf("LLM chat returned HTTP %d: %s", response.StatusCode, compactError(responseBody))
	}

	var envelope struct {
		Choices []struct {
			Message struct {
				Content   string         `json:"content"`
				ToolCalls []wireToolCall `json:"tool_calls"`
			} `json:"message"`
		} `json:"choices"`
		Usage Usage `json:"usage"`
	}
	if err := json.Unmarshal(responseBody, &envelope); err != nil {
		return ChatResponse{}, 0, false, fmt.Errorf("decode LLM chat response: %w", err)
	}
	if len(envelope.Choices) == 0 {
		return ChatResponse{}, 0, false, errors.New("LLM chat response has no choices")
	}
	message := envelope.Choices[0].Message
	result := ChatResponse{Content: strings.TrimSpace(message.Content), Usage: envelope.Usage}
	for _, call := range message.ToolCalls {
		arguments := json.RawMessage(call.Function.Arguments)
		if call.ID == "" || call.Type != "function" || call.Function.Name == "" || !json.Valid(arguments) {
			return ChatResponse{}, 0, false, errors.New("LLM chat returned an invalid tool call")
		}
		result.ToolCalls = append(result.ToolCalls, ToolCall{ID: call.ID, Name: call.Function.Name, Arguments: arguments})
	}
	return result, 0, false, nil
}

func retryDelay(value string) time.Duration {
	seconds, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || seconds < 0 {
		return 250 * time.Millisecond
	}
	delay := time.Duration(seconds) * time.Second
	if delay > 5*time.Second {
		return 5 * time.Second
	}
	return delay
}

func waitRetry(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
