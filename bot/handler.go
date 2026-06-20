package bot

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

// BotConfig holds configuration for the Telegram bot handler.
type BotConfig struct {
	SecretToken    string
	AllowedChatIDs []int64
	SessionTimeout time.Duration
	BotToken       string
}

// Bot handles incoming Telegram webhooks and forwards commands to OpenCode.
type Bot struct {
	config      *BotConfig
	ocClient    *OCClient
	sessions    *SessionMap
	topicClient *TopicClient
	httpClient  *http.Client
}

// NewBot creates a new Bot handler.
func NewBot(cfg *BotConfig, ocClient *OCClient, sessions *SessionMap, topicClient *TopicClient) *Bot {
	if len(cfg.AllowedChatIDs) == 0 {
		slog.Warn("NewBot: no AllowedChatIDs configured — ALL chats are allowed")
	}
	return &Bot{
		config:      cfg,
		ocClient:    ocClient,
		sessions:    sessions,
		topicClient: topicClient,
		httpClient:  &http.Client{Timeout: 10 * time.Second},
	}
}

// HandleWebhook is the HTTP handler for POST /webhook.
func (b *Bot) HandleWebhook(w http.ResponseWriter, r *http.Request) {
	// Verify secret token
	if r.Header.Get("X-Telegram-Bot-Api-Secret-Token") != b.config.SecretToken {
		slog.Warn("webhook: invalid secret token")
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	var update struct {
		UpdateID int `json:"update_id"`
		Message  *struct {
			MessageID       int64        `json:"message_id"`
			MessageThreadID int64        `json:"message_thread_id,omitempty"`
			Chat            TelegramChat `json:"chat"`
			Text            string       `json:"text,omitempty"`
			IsTopicMessage  bool         `json:"is_topic_message,omitempty"`
		} `json:"message,omitempty"`
	}

	if err := json.NewDecoder(r.Body).Decode(&update); err != nil {
		slog.Error("webhook: decode update", "error", err)
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	if update.Message == nil || update.Message.Text == "" {
		w.WriteHeader(http.StatusOK)
		return
	}

	chatID := update.Message.Chat.ID
	text := update.Message.Text

	slog.Info("webhook: message received", "chat_id", chatID, "text_len", len(text))

	// Whitelist check
	if !b.isChatAllowed(chatID) {
		slog.Warn("webhook: chat not allowed", "chat_id", chatID)
		b.sendTelegram(chatID, 0, "Maaf, kamu tidak punya akses.")
		w.WriteHeader(http.StatusOK)
		return
	}

	// Parse command
	cmd := ParseMessage(text)

	// Handle system commands locally
	switch cmd.Type {
	case CmdStart:
		b.sendTelegram(chatID, 0, "🤖 Halo! Saya OMOTG, jembatan Telegram ke OpenCode. Kirim /help untuk bantuan.")
		w.WriteHeader(http.StatusOK)
		return

	case CmdHelp:
		b.sendTelegram(chatID, 0, HelpText())
		w.WriteHeader(http.StatusOK)
		return

	case CmdUnknown:
		b.sendTelegram(chatID, 0, "Perintah tidak dikenal. Chat bebas juga bisa loh. Kirim /help untuk bantuan.")
		w.WriteHeader(http.StatusOK)
		return
	}

	// Create OpenCode session
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	sessionID, err := b.ocClient.CreateSession(ctx)
	if err != nil {
		slog.Error("webhook: create session", "error", err)
		b.sendTelegram(chatID, 0, "❌ OpenCode server sedang tidak tersedia. Coba lagi nanti.")
		w.WriteHeader(http.StatusOK)
		return
	}

	// Determine thread ID (forum topic) for this session
	threadID := update.Message.MessageThreadID
	chatType := update.Message.Chat.Type
	isForum := (chatType == "supergroup" || chatType == "group")

	// If message is in a group with forum topics but NOT in a topic yet, create one
	if isForum && threadID == 0 {
		topicName := fmt.Sprintf("OMOTG-%.6s", sessionID)
		createdID, err := b.topicClient.CreateForumTopic(chatID, topicName)
		if err != nil {
			slog.Error("webhook: create forum topic", "error", err, "chat_id", chatID)
			b.sendTelegram(chatID, 0, "⚠️ Tidak bisa membuat topic baru. Kirim pesan di topic yang sudah ada.")
			w.WriteHeader(http.StatusOK)
			return
		}
		threadID = createdID
		slog.Info("webhook: created forum topic", "chat_id", chatID, "topic_id", threadID, "topic_name", topicName)
	}

	// Store session-chat-topic association
	b.sessions.Store(sessionID, chatID, threadID, b.config.SessionTimeout)

	// Send acknowledgment
	ackText := fmt.Sprintf("⏳ Memproses... (session: `%s`)", sessionID)
	if threadID > 0 {
		ackText = fmt.Sprintf("⏳ Memproses... (topic: %d, session: `%s`)", threadID, sessionID)
	}
	if cmd.Type == CmdDeploy {
		ackText = fmt.Sprintf("🚀 Menjalankan: `%s`", cmd.RawText)
	}
	b.sendTelegram(chatID, threadID, ackText)

	// Send message to OpenCode (async — don't block webhook response)
	go func() {
		if err := b.ocClient.SendMessage(context.Background(), sessionID, cmd.Prompt); err != nil {
			slog.Error("webhook: send message to OpenCode", "error", err, "session_id", sessionID)
			b.sendTelegram(chatID, threadID, fmt.Sprintf("❌ Gagal mengirim perintah ke OpenCode: %s", err))
			b.sessions.Delete(sessionID)
			return
		}
	}()

	// Start streaming events in background
	go b.streamSessionEvents(sessionID, chatID, threadID)

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "accepted"})
}

// streamSessionEvents subscribes to the OpenCode SSE stream and forwards
// matching session events to the Telegram chat.
func (b *Bot) streamSessionEvents(sessionID string, chatID int64, threadID int64) {
	ctx, cancel := context.WithTimeout(context.Background(), b.config.SessionTimeout)
	defer cancel()
	defer b.sessions.Delete(sessionID)

	events, err := b.ocClient.SubscribeEvents(ctx)
	if err != nil {
		slog.Error("stream: subscribe error", "session_id", sessionID, "error", err)
		b.sendTelegram(chatID, threadID, "❌ Gagal subscribe ke event stream OpenCode.")
		return
	}

	var (
		buf          strings.Builder
		seenDelta    bool
		fallbackText string // last "text" event content (assistant response, captured after first delta)
	)

	for ev := range events {
		if ev.SessionID != sessionID {
			continue
		}

		switch ev.Type {
		case "delta":
			seenDelta = true
			buf.WriteString(ev.Content)
		case "text":
			// OpenCode echoes the user's message as a "text" event before any
			// delta events. Skip those. After the first delta, "text" events
			// are the assistant's final part — use as fallback.
			if seenDelta {
				fallbackText = ev.Content
			}
		case "error":
			msg := ev.Content
			if msg == "" {
				msg = "Unknown error"
			}
			b.sendTelegram(chatID, threadID, fmt.Sprintf("❌ Error: %s", msg))
		case "done":
			accumulated := strings.TrimSpace(buf.String())
			if accumulated != "" {
				b.sendTelegram(chatID, threadID, accumulated)
			} else if fallbackText != "" {
				b.sendTelegram(chatID, threadID, fallbackText)
			}
			b.sendTelegram(chatID, threadID, "✅ Selesai!")
			return
		}
	}
}

// isChatAllowed checks if a chat ID is in the whitelist.
// Empty whitelist means all chats are allowed.
func (b *Bot) isChatAllowed(chatID int64) bool {
	if len(b.config.AllowedChatIDs) == 0 {
		return true
	}
	for _, id := range b.config.AllowedChatIDs {
		if id == chatID {
			return true
		}
	}
	return false
}

// TelegramChat represents a chat entity from Telegram updates.
type TelegramChat struct {
	ID   int64  `json:"id"`
	Type string `json:"type,omitempty"` // "private", "group", "supergroup"
}

// sendTelegram sends a text message to a Telegram chat via the Bot API.
// If threadID > 0, the message is sent to that forum topic.
func (b *Bot) sendTelegram(chatID int64, threadID int64, text string) {
	if b.config.BotToken == "" {
		slog.Error("sendTelegram: bot token not set")
		return
	}

	payload := map[string]interface{}{
		"chat_id": chatID,
		"text":    text,
	}
	if threadID > 0 {
		payload["message_thread_id"] = threadID
	}
	body, err := json.Marshal(payload)
	if err != nil {
		slog.Error("sendTelegram: marshal payload", "error", err)
		return
	}

	url := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", b.config.BotToken)
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		slog.Error("sendTelegram: create request", "error", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := b.httpClient.Do(req)
	if err != nil {
		slog.Error("sendTelegram: HTTP call", "error", err, "chat_id", chatID)
		return
	}
	defer resp.Body.Close()

	var tgResp struct {
		Ok          bool   `json:"ok"`
		Description string `json:"description,omitempty"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tgResp); err != nil {
		slog.Error("sendTelegram: decode response", "error", err)
		return
	}
	if !tgResp.Ok {
		slog.Warn("sendTelegram: telegram error",
			"description", tgResp.Description,
			"chat_id", chatID,
		)
	}
}
