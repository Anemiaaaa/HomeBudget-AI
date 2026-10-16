package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/Anemiaaaa/HomeBudget-AI/internal/llm"
	"github.com/Anemiaaaa/HomeBudget-AI/internal/telegram"
)

func main() {
	telegramCheck := flag.Bool("telegram", false, "call Telegram getMe")
	llmCheck := flag.Bool("llm", false, "require an LLM create_expense tool call")
	flag.Parse()
	if !*telegramCheck && !*llmCheck {
		fmt.Fprintln(os.Stderr, "select at least one check: -telegram or -llm")
		os.Exit(2)
	}

	timeout, err := time.ParseDuration(valueOrDefault("LLM_TIMEOUT", "30s"))
	if err != nil || timeout <= 0 {
		fmt.Fprintln(os.Stderr, "invalid LLM_TIMEOUT")
		os.Exit(2)
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	httpClient := &http.Client{Timeout: timeout}

	if *telegramCheck {
		client, err := telegram.NewClient("https://api.telegram.org", os.Getenv("TELEGRAM_BOT_TOKEN"), httpClient)
		if err != nil {
			fail(err)
		}
		bot, err := client.GetMe(ctx)
		if err != nil {
			fail(err)
		}
		fmt.Printf("telegram: ok (@%s, id=%d)\n", bot.Username, bot.ID)
	}

	if *llmCheck {
		client, err := llm.NewOpenAIClient(valueOrDefault("LLM_BASE_URL", "https://api.openai.com/v1"), os.Getenv("LLM_API_KEY"), httpClient)
		if err != nil {
			fail(err)
		}
		call, err := client.ProbeExpenseTool(ctx, os.Getenv("LLM_MODEL"))
		if err != nil {
			fail(err)
		}
		fmt.Printf("llm: ok (tool=%s)\n", call.Name)
	}
}

func valueOrDefault(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "smoke check failed:", err)
	os.Exit(1)
}
