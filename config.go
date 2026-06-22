package main

import (
	"fmt"
	"log/slog"
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
	TLSCertFile      string
	TLSKeyFile       string
	Bots             []BotAgentConfig // ordered: [primary, ...auxiliary]
}

// BotAgentConfig is a single Telegram bot bound to one OMO agent.
type BotAgentConfig struct {
	Key        string // env var suffix key, e.g. "prometheus"
	BotToken   string
	WebhookURL string // full public URL, e.g. https://host:8443/webhook/prometheus
	Agent      string // OpenCode display name, e.g. "Prometheus - Plan Builder"
}

// agentMap maps an env-key suffix to the OpenCode agent display name.
// Keys must be lowercase ASCII identifiers (used in env var name and webhook path).
var agentMap = map[string]string{
	"primary":    "Sisyphus - ultraworker",
	"prometheus": "Prometheus - Plan Builder",
	"atlas":      "Atlas - Plan Executor",
	"hephaestus": "Hephaestus - Deep Agent",
}

func LoadConfig() (*Config, error) {
	home, _ := os.UserHomeDir()
	defaultCert := home + "/.config/omotg/webhook.crt"
	defaultKey := home + "/.config/omotg/webhook.key"

	cfg := &Config{
		OpenCodeURL:    envOrDefault("OPENCODE_SERVER_URL", "http://127.0.0.1:4096"),
		WebhookPort:    envOrDefault("OMOTG_WEBHOOK_PORT", "8443"),
		MCPPort:        envOrDefault("OMOTG_MCP_PORT", "9090"),
		SessionTimeout: 300,
		TLSCertFile:    envLookup("OMOTG_TLS_CERT_FILE", defaultCert),
		TLSKeyFile:     envLookup("OMOTG_TLS_KEY_FILE", defaultKey),
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

	// Build the Bots slice. Primary always first; auxiliaries follow in deterministic order.
	primaryURL := strings.TrimRight(cfg.WebhookURL, "/")
	cfg.Bots = append(cfg.Bots, BotAgentConfig{
		Key:        "primary",
		BotToken:   cfg.TelegramBotToken,
		WebhookURL: primaryURL, // legacy /webhook path
		Agent:      agentMap["primary"],
	})

	auxOrder := []string{"prometheus", "atlas", "hephaestus"}
	for _, key := range auxOrder {
		tok := os.Getenv("OMO_" + strings.ToUpper(key) + "_BOT_TOKEN")
		if tok == "" {
			continue
		}
		cfg.Bots = append(cfg.Bots, BotAgentConfig{
			Key:        key,
			BotToken:   tok,
			WebhookURL: primaryURL + "/" + key,
			Agent:      agentMap[key],
		})
	}
	if len(cfg.Bots) < 2 {
		slog.Info("no auxiliary bots configured (single-bot mode)")
	}

	return cfg, nil
}

func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// envLookup returns the env value if set (even if empty), otherwise the default.
func envLookup(key, def string) string {
	if v, ok := os.LookupEnv(key); ok {
		return v
	}
	return def
}
