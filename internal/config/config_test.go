package config

import (
	"testing"
	"time"
)

func TestLoadDefaults(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://example")
	t.Setenv("BOT_ENABLED", "false")
	t.Setenv("APP_TIMEZONE", "Europe/Moscow")
	t.Setenv("FAMILY_USER_IDS", "101, 202,101")

	got, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got.HTTPAddr != ":8080" || got.LLMTimeout != 30*time.Second {
		t.Fatalf("unexpected defaults: %+v", got)
	}
	if len(got.FamilyUserIDs) != 2 {
		t.Fatalf("FamilyUserIDs len = %d, want 2", len(got.FamilyUserIDs))
	}
}

func TestLoadRequiresBotCredentials(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://example")
	t.Setenv("BOT_ENABLED", "true")
	t.Setenv("TELEGRAM_BOT_TOKEN", "")
	t.Setenv("LLM_MODEL", "")

	if _, err := Load(); err == nil {
		t.Fatal("Load() error = nil, want missing Telegram token error")
	}
}

func TestLoadRejectsInvalidTimezone(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://example")
	t.Setenv("APP_TIMEZONE", "Mars/Olympus")

	if _, err := Load(); err == nil {
		t.Fatal("Load() error = nil, want invalid timezone error")
	}
}
