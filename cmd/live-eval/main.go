package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/Anemiaaaa/HomeBudget-AI/internal/liveeval"
	"github.com/Anemiaaaa/HomeBudget-AI/internal/llm"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	databaseURL := strings.TrimSpace(os.Getenv("DATABASE_URL"))
	baseURL := strings.TrimRight(strings.TrimSpace(os.Getenv("LLM_BASE_URL")), "/")
	model := strings.TrimSpace(os.Getenv("LLM_MODEL"))
	if databaseURL == "" || baseURL == "" || model == "" {
		return errors.New("DATABASE_URL, LLM_BASE_URL and LLM_MODEL are required")
	}
	repeats, err := integerEnv("LIVE_EVAL_REPEATS", 3, 1, 10)
	if err != nil {
		return err
	}
	timeout, err := durationEnv("LLM_TIMEOUT", 30*time.Second)
	if err != nil {
		return err
	}
	client, err := llm.NewOpenAIClient(baseURL, os.Getenv("LLM_API_KEY"), &http.Client{Timeout: timeout})
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	report, runErr := liveeval.Run(ctx, liveeval.Options{
		DatabaseURL: databaseURL,
		ProviderURL: baseURL,
		Model:       model,
		Commit:      strings.TrimSpace(os.Getenv("LIVE_EVAL_COMMIT")),
		Repeats:     repeats,
		CasesPath:   valueOrDefault("LIVE_EVAL_CASES", "/app/evals/live_cases.json"),
		CaseIDs:     strings.Split(os.Getenv("LIVE_EVAL_CASE_IDS"), ","),
		Provider:    client,
	})
	encoded, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	fmt.Println(string(encoded))
	if path := strings.TrimSpace(os.Getenv("LIVE_EVAL_REPORT")); path != "" {
		if err := os.WriteFile(path, append(encoded, '\n'), 0o644); err != nil {
			return fmt.Errorf("write live eval report: %w", err)
		}
	}
	return runErr
}

func valueOrDefault(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

func integerEnv(name string, fallback, minimum, maximum int) (int, error) {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed < minimum || parsed > maximum {
		return 0, fmt.Errorf("%s must be between %d and %d", name, minimum, maximum)
	}
	return parsed, nil
}

func durationEnv(name string, fallback time.Duration) (time.Duration, error) {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback, nil
	}
	parsed, err := time.ParseDuration(value)
	if err != nil || parsed <= 0 {
		return 0, fmt.Errorf("%s must be a positive duration", name)
	}
	return parsed, nil
}
