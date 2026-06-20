package main

import (
	"os"
	"testing"
)

func TestLoadConfig(t *testing.T) {
	t.Setenv("TELEGRAM_BOT_TOKEN", "test:token")
	t.Setenv("TELEGRAM_WEBHOOK_URL", "https://example.com/webhook")
	t.Setenv("TELEGRAM_SECRET_TOKEN", "sekret")
	t.Setenv("OPENCODE_SERVER_PASSWORD", "pass123")
	t.Setenv("OMOTG_ALLOWED_CHAT_IDS", "123,456")
	t.Setenv("OMOTG_SESSION_TIMEOUT", "600")

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig() error: %v", err)
	}
	if cfg.TelegramBotToken != "test:token" {
		t.Errorf("TelegramBotToken = %q, want %q", cfg.TelegramBotToken, "test:token")
	}
	if cfg.WebhookURL != "https://example.com/webhook" {
		t.Errorf("WebhookURL = %q", cfg.WebhookURL)
	}
	if cfg.SecretToken != "sekret" {
		t.Errorf("SecretToken = %q", cfg.SecretToken)
	}
	if cfg.OpenCodePassword != "pass123" {
		t.Errorf("OpenCodePassword = %q", cfg.OpenCodePassword)
	}
	if len(cfg.AllowedChatIDs) != 2 || cfg.AllowedChatIDs[0] != 123 || cfg.AllowedChatIDs[1] != 456 {
		t.Errorf("AllowedChatIDs = %v", cfg.AllowedChatIDs)
	}
	if cfg.SessionTimeout != 600 {
		t.Errorf("SessionTimeout = %d, want 600", cfg.SessionTimeout)
	}
}

func TestLoadConfig_MissingRequired(t *testing.T) {
	for _, k := range []string{"TELEGRAM_BOT_TOKEN", "TELEGRAM_WEBHOOK_URL", "TELEGRAM_SECRET_TOKEN", "OPENCODE_SERVER_PASSWORD"} {
		os.Unsetenv(k)
	}
	_, err := LoadConfig()
	if err == nil {
		t.Fatal("LoadConfig() expected error for missing required fields")
	}
}

func TestLoadConfig_Defaults(t *testing.T) {
	t.Setenv("TELEGRAM_BOT_TOKEN", "tok")
	t.Setenv("TELEGRAM_WEBHOOK_URL", "https://x.com/w")
	t.Setenv("TELEGRAM_SECRET_TOKEN", "s")
	t.Setenv("OPENCODE_SERVER_PASSWORD", "p")

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig() error: %v", err)
	}
	if cfg.OpenCodeURL != "http://127.0.0.1:4096" {
		t.Errorf("default OpenCodeURL = %q", cfg.OpenCodeURL)
	}
	if cfg.WebhookPort != "8443" {
		t.Errorf("default WebhookPort = %q", cfg.WebhookPort)
	}
	if cfg.MCPPort != "9090" {
		t.Errorf("default MCPPort = %q", cfg.MCPPort)
	}
	if cfg.SessionTimeout != 300 {
		t.Errorf("default SessionTimeout = %d", cfg.SessionTimeout)
	}
	if cfg.AllowedChatIDs != nil {
		t.Errorf("default AllowedChatIDs should be nil")
	}
}

func TestLoadConfig_InvalidChatID(t *testing.T) {
	t.Setenv("TELEGRAM_BOT_TOKEN", "tok")
	t.Setenv("TELEGRAM_WEBHOOK_URL", "https://x.com/w")
	t.Setenv("TELEGRAM_SECRET_TOKEN", "s")
	t.Setenv("OPENCODE_SERVER_PASSWORD", "p")
	t.Setenv("OMOTG_ALLOWED_CHAT_IDS", "abc")

	_, err := LoadConfig()
	if err == nil {
		t.Fatal("expected error for invalid chat ID")
	}
}
