package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// TelegramSender sends messages via the Telegram Bot API.
type TelegramSender struct {
	BotToken   string
	HTTPClient *http.Client
}

// NewTelegramSender creates a TelegramSender with a 10-second HTTP timeout.
func NewTelegramSender(token string) *TelegramSender {
	return &TelegramSender{
		BotToken:   token,
		HTTPClient: &http.Client{Timeout: 10 * time.Second},
	}
}

// RegisterTelegramTools registers send_telegram_message and
// send_telegram_notification tools on the given MCP server.
func RegisterTelegramTools(s *Server, sender *TelegramSender) {
	s.RegisterTool(
		ToolDefinition{
			Name:        "send_telegram_message",
			Description: "Send a text message to a Telegram chat",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]PropertySchema{
					"chat_id":           {Type: "string", Description: "Telegram chat ID"},
					"text":              {Type: "string", Description: "Message text"},
					"parse_mode":        {Type: "string", Description: "Parse mode: HTML or MarkdownV2"},
					"message_thread_id": {Type: "string", Description: "Forum topic thread ID (optional)"},
				},
				Required: []string{"chat_id", "text"},
			},
		},
		func(ctx context.Context, args json.RawMessage) (string, error) {
			var params struct {
				ChatID          string `json:"chat_id"`
				Text            string `json:"text"`
				ParseMode       string `json:"parse_mode,omitempty"`
				MessageThreadID string `json:"message_thread_id,omitempty"`
			}
			if err := json.Unmarshal(args, &params); err != nil {
				return "", fmt.Errorf("invalid arguments: %w", err)
			}
			return sender.sendTelegram(ctx, params.ChatID, params.Text, params.ParseMode, params.MessageThreadID)
		},
	)

	s.RegisterTool(
		ToolDefinition{
			Name:        "send_telegram_notification",
			Description: "Send a formatted notification (emoji + status + message) to a Telegram chat",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]PropertySchema{
					"chat_id":           {Type: "string", Description: "Telegram chat ID"},
					"status":            {Type: "string", Description: "Notification status: success, error, info, or warning"},
					"message":           {Type: "string", Description: "Notification message text"},
					"message_thread_id": {Type: "string", Description: "Forum topic thread ID (optional)"},
				},
				Required: []string{"chat_id", "status", "message"},
			},
		},
		func(ctx context.Context, args json.RawMessage) (string, error) {
			var params struct {
				ChatID          string `json:"chat_id"`
				Status          string `json:"status"`
				Message         string `json:"message"`
				MessageThreadID string `json:"message_thread_id,omitempty"`
			}
			if err := json.Unmarshal(args, &params); err != nil {
				return "", fmt.Errorf("invalid arguments: %w", err)
			}

			emoji := notificationEmoji(params.Status)
			status := strings.ToUpper(params.Status[:1]) + params.Status[1:]

			// Try MarkdownV2 first.
			escaped := escapeMarkdownV2(params.Message)
			formatted := fmt.Sprintf("%s *%s*\n%s", emoji, status, escaped)
			result, err := sender.sendTelegram(ctx, params.ChatID, formatted, "MarkdownV2", params.MessageThreadID)
			if err == nil {
				return result, nil
			}

			// Fall back to plain text if MarkdownV2 fails.
			plain := fmt.Sprintf("%s %s\n%s", emoji, status, params.Message)
			result, err = sender.sendTelegram(ctx, params.ChatID, plain, "", params.MessageThreadID)
			if err != nil {
				return "", err
			}
			return result, nil
		},
	)
}

// sendTelegram posts a message to the Telegram Bot API and checks the response.
func (s *TelegramSender) sendTelegram(ctx context.Context, chatID, text, parseMode, threadID string) (string, error) {
	body := map[string]interface{}{
		"chat_id": chatID,
		"text":    text,
	}
	if parseMode != "" {
		body["parse_mode"] = parseMode
	}
	if threadID != "" {
		body["message_thread_id"] = threadID
	}

	jsonBody, err := json.Marshal(body)
	if err != nil {
		return "", fmt.Errorf("marshal request: %w", err)
	}

	url := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", s.BotToken)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(jsonBody))
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.HTTPClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("telegram api call: %w", err)
	}
	defer resp.Body.Close()

	var tgResp struct {
		Ok          bool   `json:"ok"`
		Description string `json:"description,omitempty"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tgResp); err != nil {
		return "", fmt.Errorf("decode telegram response: %w", err)
	}

	if !tgResp.Ok {
		return "", fmt.Errorf("telegram error: %s", tgResp.Description)
	}

	return fmt.Sprintf("Message sent to chat %s", chatID), nil
}

// notificationEmoji returns the emoji for a given notification status.
func notificationEmoji(status string) string {
	switch strings.ToLower(status) {
	case "success":
		return "✅"
	case "error":
		return "❌"
	case "warning":
		return "⚠️"
	default:
		return "ℹ️"
	}
}

// escapeMarkdownV2 escapes Telegram MarkdownV2 special characters in s.
//
// Reserved characters: _ * [ ] ( ) ~ ` > # + - = | { } . !
func escapeMarkdownV2(s string) string {
	var b strings.Builder
	b.Grow(len(s) + 10)
	for _, r := range s {
		switch r {
		case '_', '*', '[', ']', '(', ')', '~', '`', '>', '#', '+', '-', '=', '|', '{', '}', '.', '!':
			b.WriteRune('\\')
			b.WriteRune(r)
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}
