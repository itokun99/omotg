package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/itokun99/omotg/bot"
	"github.com/itokun99/omotg/mcp"
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

	// Create the single shared OpenCode client — all bots speak to the same server.
	ocClient := bot.NewOCClient(cfg.OpenCodeURL, cfg.OpenCodePassword)

	// Per-bot handlers and webhook registrations.
	type botHandler struct {
		key       string
		path      string
		handlerFn http.HandlerFunc
	}
	var handlers []botHandler

	for _, bc := range cfg.Bots {
		sessions := bot.NewSessionMap()
		topicClient := bot.NewTopicClient(bc.BotToken)
		botCfg := &bot.BotConfig{
			SecretToken:    cfg.SecretToken,
			AllowedChatIDs: cfg.AllowedChatIDs,
			SessionTimeout: time.Duration(cfg.SessionTimeout) * time.Second,
			BotToken:       bc.BotToken,
			Agent:          bc.Agent,
		}
		bh := bot.NewBot(botCfg, ocClient, sessions, topicClient)

		// Primary keeps /webhook; auxiliaries get /webhook/<key>.
		path := "/webhook"
		if bc.Key != "primary" {
			path = "/webhook/" + bc.Key
		}
		handlers = append(handlers, botHandler{
			key:       bc.Key,
			path:      path,
			handlerFn: bh.HandleWebhook,
		})
		slog.Info("bot registered",
			"key", bc.Key,
			"webhook_path", path,
			"agent", bc.Agent,
		)
	}

	if len(cfg.AllowedChatIDs) == 0 {
		slog.Warn("no AllowedChatIDs configured — ALL chats are allowed")
	}

	// Register Telegram webhooks on startup (one per bot).
	for _, bc := range cfg.Bots {
		if err := registerWebhook(bc.BotToken, bc.WebhookURL, cfg.SecretToken, cfg.TLSCertFile); err != nil {
			slog.Warn("webhook registration failed",
				"bot_key", bc.Key,
				"error", err,
			)
		} else {
			slog.Info("webhook registered",
				"bot_key", bc.Key,
				"url", bc.WebhookURL,
			)
		}
	}

	// Create the shared MCP server — still uses the primary bot token.
	primaryToken := cfg.TelegramBotToken
	mcpBaseURL := "http://127.0.0.1:" + cfg.MCPPort
	mcpServer := mcp.New(mcpBaseURL)
	telegramSender := mcp.NewTelegramSender(primaryToken)
	mcp.RegisterTelegramTools(mcpServer, telegramSender)

	// SessionMap.StartCleanup is a no-op today; keep one call for forward-compat.
	go bot.NewSessionMap().StartCleanup(context.Background(), 5*time.Minute)

	// --- Webhook HTTP server ---
	webhookMux := http.NewServeMux()
	for _, h := range handlers {
		// Capture loop variable by-value to avoid the classic Go closure bug.
		hh := h
		webhookMux.HandleFunc("POST "+hh.path, hh.handlerFn)
	}
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
		if cfg.TLSCertFile != "" && cfg.TLSKeyFile != "" {
			slog.Info("webhook server listening (TLS)", "addr", whServer.Addr)
			if err := whServer.ListenAndServeTLS(cfg.TLSCertFile, cfg.TLSKeyFile); err != nil && err != http.ErrServerClosed {
				errCh <- fmt.Errorf("webhook: %w", err)
			}
		} else {
			slog.Info("webhook server listening (plain HTTP, behind reverse proxy)", "addr", whServer.Addr)
			if err := whServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				errCh <- fmt.Errorf("webhook: %w", err)
			}
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

func registerWebhook(token, url, secret, certFile string) error {
	apiURL := fmt.Sprintf("https://api.telegram.org/bot%s/setWebhook", token)

	var body io.Reader
	var contentType string

	if certFile != "" {
		var buf bytes.Buffer
		w := multipart.NewWriter(&buf)
		w.WriteField("url", url)
		w.WriteField("allowed_updates", `["message","channel_post"]`)
		if secret != "" {
			w.WriteField("secret_token", secret)
		}
		certPath, _ := filepath.Abs(certFile)
		f, err := os.Open(certPath)
		if err != nil {
			return fmt.Errorf("open cert %s: %w", certPath, err)
		}
		defer f.Close()
		fw, err := w.CreateFormFile("certificate", filepath.Base(certPath))
		if err != nil {
			return fmt.Errorf("create form file: %w", err)
		}
		if _, err := io.Copy(fw, f); err != nil {
			return fmt.Errorf("copy cert: %w", err)
		}
		w.Close()
		body = &buf
		contentType = w.FormDataContentType()
	} else {
		payload := map[string]interface{}{
			"url":            url,
			"allowed_updates": []string{"message", "channel_post"},
		}
		if secret != "" {
			payload["secret_token"] = secret
		}
		b, _ := json.Marshal(payload)
		body = bytes.NewReader(b)
		contentType = "application/json"
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Post(apiURL, contentType, body)
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
