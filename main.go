package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"omotg/bot"
	"omotg/mcp"
)

func main() {
	cfg, err := LoadConfig()
	if err != nil {
		slog.Error("config", "error", err)
		os.Exit(1)
	}

	slog.Info("omotg starting",
		"webhook_port", cfg.WebhookPort,
		"mcp_port", cfg.MCPPort,
		"opencode_url", cfg.OpenCodeURL,
		"session_timeout", cfg.SessionTimeout,
	)

	// Create OpenCode client.
	ocClient := bot.NewOCClient(cfg.OpenCodeURL, cfg.OpenCodePassword)

	// Create session map.
	sessions := bot.NewSessionMap()

	// Create bot handler.
	botCfg := &bot.BotConfig{
		SecretToken:    cfg.SecretToken,
		AllowedChatIDs: cfg.AllowedChatIDs,
		SessionTimeout: time.Duration(cfg.SessionTimeout) * time.Second,
		BotToken:       cfg.TelegramBotToken,
	}
	bh := bot.NewBot(botCfg, ocClient, sessions)

	if len(cfg.AllowedChatIDs) == 0 {
		slog.Warn("no AllowedChatIDs configured — ALL chats are allowed")
	}

	// Register Telegram webhook on startup.
	if err := registerWebhook(cfg.TelegramBotToken, cfg.WebhookURL, cfg.SecretToken); err != nil {
		slog.Warn("webhook registration failed (will retry)", "error", err)
	} else {
		slog.Info("webhook registered", "url", cfg.WebhookURL)
	}

	// Create MCP server and register Telegram tools.
	mcpBaseURL := "http://127.0.0.1:" + cfg.MCPPort
	mcpServer := mcp.New(mcpBaseURL)
	telegramSender := mcp.NewTelegramSender(cfg.TelegramBotToken)
	mcp.RegisterTelegramTools(mcpServer, telegramSender)

	// Start session cleanup goroutine.
	go sessions.StartCleanup(context.Background(), 5*time.Minute)

	// --- Webhook HTTP server ---
	webhookMux := http.NewServeMux()
	webhookMux.HandleFunc("POST /webhook", bh.HandleWebhook)
	webhookMux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	})

	whServer := &http.Server{
		Addr:         ":" + cfg.WebhookPort,
		Handler:      webhookMux,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// --- MCP SSE server ---
	// The MCP Server.Handler() returns a mux serving GET /mcp/sse and POST /mcp/message.
	mcpServer_ := &http.Server{
		Addr:         "127.0.0.1:" + cfg.MCPPort,
		Handler:      mcpServer.Handler(),
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 0, // SSE long-lived connection
		IdleTimeout:  60 * time.Second,
	}

	// Start servers.
	errCh := make(chan error, 2)

	go func() {
		slog.Info("webhook server listening", "addr", whServer.Addr)
		if err := whServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- fmt.Errorf("webhook: %w", err)
		}
	}()

	go func() {
		slog.Info("MCP server listening", "addr", mcpServer_.Addr)
		if err := mcpServer_.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- fmt.Errorf("mcp: %w", err)
		}
	}()

	// Wait for interrupt or server error.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	select {
	case sig := <-sigCh:
		slog.Info("shutting down", "signal", sig)
	case err := <-errCh:
		slog.Error("server error", "error", err)
	}

	// Graceful shutdown with 5s timeout.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := whServer.Shutdown(ctx); err != nil {
		slog.Error("webhook shutdown", "error", err)
	}
	if err := mcpServer_.Shutdown(ctx); err != nil {
		slog.Error("MCP shutdown", "error", err)
	}
	slog.Info("omotg stopped")
}

// registerWebhook calls Telegram setWebhook API to register the webhook URL.
func registerWebhook(token, url, secret string) error {
	payload := map[string]string{
		"url": url,
	}
	if secret != "" {
		payload["secret_token"] = secret
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal payload: %w", err)
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Post(
		fmt.Sprintf("https://api.telegram.org/bot%s/setWebhook", token),
		"application/json",
		bytes.NewReader(body),
	)
	if err != nil {
		return fmt.Errorf("http call: %w", err)
	}
	defer resp.Body.Close()

	var result struct {
		Ok          bool   `json:"ok"`
		Description string `json:"description,omitempty"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	if !result.Ok {
		return fmt.Errorf("telegram error: %s", result.Description)
	}
	return nil
}
