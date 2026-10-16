package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	DatabaseURL          string
	HTTPAddr             string
	WebDir               string
	Timezone             string
	Location             *time.Location
	LogLevel             string
	BotEnabled           bool
	TelegramBotToken     string
	FamilyUserIDs        map[int64]struct{}
	LLMBaseURL           string
	LLMModel             string
	LLMAPIKey            string
	LLMTimeout           time.Duration
	AgentMaxLLMCalls     int
	AgentMaxToolCalls    int
	AgentMaxContextBytes int
	AgentTurnTimeout     time.Duration
}

func Load() (Config, error) {
	botEnabled, err := boolEnv("BOT_ENABLED", false)
	if err != nil {
		return Config{}, err
	}

	timezone := valueOrDefault("APP_TIMEZONE", "Europe/Moscow")
	location, err := time.LoadLocation(timezone)
	if err != nil {
		return Config{}, fmt.Errorf("APP_TIMEZONE: %w", err)
	}

	llmTimeout, err := durationEnv("LLM_TIMEOUT", 30*time.Second)
	if err != nil {
		return Config{}, err
	}
	if llmTimeout <= 0 {
		return Config{}, errors.New("LLM_TIMEOUT must be positive")
	}
	agentTurnTimeout, err := durationEnv("AGENT_TURN_TIMEOUT", time.Minute)
	if err != nil || agentTurnTimeout <= 0 {
		return Config{}, errors.New("AGENT_TURN_TIMEOUT must be a positive duration")
	}
	agentMaxLLMCalls, err := intEnv("AGENT_MAX_LLM_CALLS", 4, 1, 8)
	if err != nil {
		return Config{}, err
	}
	agentMaxToolCalls, err := intEnv("AGENT_MAX_TOOL_CALLS", 6, 1, 12)
	if err != nil {
		return Config{}, err
	}
	agentMaxContextBytes, err := intEnv("AGENT_MAX_CONTEXT_BYTES", 32<<10, 4096, 1<<20)
	if err != nil {
		return Config{}, err
	}

	familyUserIDs, err := parseIDs(os.Getenv("FAMILY_USER_IDS"))
	if err != nil {
		return Config{}, err
	}

	config := Config{
		DatabaseURL:          strings.TrimSpace(os.Getenv("DATABASE_URL")),
		HTTPAddr:             valueOrDefault("HTTP_ADDR", ":8080"),
		WebDir:               valueOrDefault("WEB_DIR", "web/dist"),
		Timezone:             timezone,
		Location:             location,
		LogLevel:             valueOrDefault("LOG_LEVEL", "info"),
		BotEnabled:           botEnabled,
		TelegramBotToken:     strings.TrimSpace(os.Getenv("TELEGRAM_BOT_TOKEN")),
		FamilyUserIDs:        familyUserIDs,
		LLMBaseURL:           strings.TrimRight(valueOrDefault("LLM_BASE_URL", "https://api.openai.com/v1"), "/"),
		LLMModel:             strings.TrimSpace(os.Getenv("LLM_MODEL")),
		LLMAPIKey:            strings.TrimSpace(os.Getenv("LLM_API_KEY")),
		LLMTimeout:           llmTimeout,
		AgentMaxLLMCalls:     agentMaxLLMCalls,
		AgentMaxToolCalls:    agentMaxToolCalls,
		AgentMaxContextBytes: agentMaxContextBytes,
		AgentTurnTimeout:     agentTurnTimeout,
	}

	if config.DatabaseURL == "" {
		return Config{}, errors.New("DATABASE_URL is required")
	}
	if config.BotEnabled {
		if config.TelegramBotToken == "" {
			return Config{}, errors.New("TELEGRAM_BOT_TOKEN is required when BOT_ENABLED=true")
		}
		if config.LLMModel == "" {
			return Config{}, errors.New("LLM_MODEL is required when BOT_ENABLED=true")
		}
	}
	if config.LLMBaseURL == "" {
		return Config{}, errors.New("LLM_BASE_URL must not be empty")
	}

	return config, nil
}

func valueOrDefault(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

func boolEnv(name string, fallback bool) (bool, error) {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return false, fmt.Errorf("%s: %w", name, err)
	}
	return parsed, nil
}

func durationEnv(name string, fallback time.Duration) (time.Duration, error) {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback, nil
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", name, err)
	}
	return parsed, nil
}

func intEnv(name string, fallback, minimum, maximum int) (int, error) {
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

func parseIDs(raw string) (map[int64]struct{}, error) {
	result := make(map[int64]struct{})
	if strings.TrimSpace(raw) == "" {
		return result, nil
	}
	for _, item := range strings.Split(raw, ",") {
		id, err := strconv.ParseInt(strings.TrimSpace(item), 10, 64)
		if err != nil || id <= 0 {
			return nil, fmt.Errorf("FAMILY_USER_IDS contains invalid Telegram ID %q", item)
		}
		result[id] = struct{}{}
	}
	return result, nil
}
