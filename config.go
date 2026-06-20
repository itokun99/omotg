package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

type Config struct {
	TelegramBotToken string
	WebhookURL       string
	SecretToken      string
	OpenCodeURL      string
	OpenCodePassword string
	WebhookPort      string
	MCPPort          string
	AllowedChatIDs   []int64
	SessionTimeout   int // seconds
}

func LoadConfig() (*Config, error) {
	cfg := &Config{
		OpenCodeURL:    envOrDefault("OPENCODE_SERVER_URL", "http://127.0.0.1:4096"),
		WebhookPort:    envOrDefault("OMOTG_WEBHOOK_PORT", "8443"),
		MCPPort:        envOrDefault("OMOTG_MCP_PORT", "9090"),
		SessionTimeout: 300,
	}

	var missing []string
	if cfg.TelegramBotToken = os.Getenv("TELEGRAM_BOT_TOKEN"); cfg.TelegramBotToken == "" {
		missing = append(missing, "TELEGRAM_BOT_TOKEN")
	}
	if cfg.WebhookURL = os.Getenv("TELEGRAM_WEBHOOK_URL"); cfg.WebhookURL == "" {
		missing = append(missing, "TELEGRAM_WEBHOOK_URL")
	}
	if cfg.SecretToken = os.Getenv("TELEGRAM_SECRET_TOKEN"); cfg.SecretToken == "" {
		missing = append(missing, "TELEGRAM_SECRET_TOKEN")
	}
	if cfg.OpenCodePassword = os.Getenv("OPENCODE_SERVER_PASSWORD"); cfg.OpenCodePassword == "" {
		missing = append(missing, "OPENCODE_SERVER_PASSWORD")
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("missing required env vars: %s", strings.Join(missing, ", "))
	}

	if ids := os.Getenv("OMOTG_ALLOWED_CHAT_IDS"); ids != "" {
		parts := strings.Split(ids, ",")
		cfg.AllowedChatIDs = make([]int64, 0, len(parts))
		for _, p := range parts {
			id, err := strconv.ParseInt(strings.TrimSpace(p), 10, 64)
			if err != nil {
				return nil, fmt.Errorf("invalid chat ID %q: %w", p, err)
			}
			cfg.AllowedChatIDs = append(cfg.AllowedChatIDs, id)
		}
	}

	if t := os.Getenv("OMOTG_SESSION_TIMEOUT"); t != "" {
		val, err := strconv.Atoi(t)
		if err != nil || val <= 0 {
			return nil, fmt.Errorf("invalid OMOTG_SESSION_TIMEOUT: %q", t)
		}
		cfg.SessionTimeout = val
	}

	return cfg, nil
}

func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
