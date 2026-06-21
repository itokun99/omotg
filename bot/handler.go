package bot

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"
	"unicode/utf8"
)

// telegramMaxMessageLen is the Telegram sendMessage character limit (safe margin).
const telegramMaxMessageLen = 4000

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
	persona     *BotPersona
}

// NewBot creates a new Bot handler.
func NewBot(cfg *BotConfig, ocClient *OCClient, sessions *SessionMap, topicClient *TopicClient) *Bot {
	if len(cfg.AllowedChatIDs) == 0 {
		slog.Warn("NewBot: no AllowedChatIDs configured — ALL chats are allowed")
	}
	bot := &Bot{
		config:      cfg,
		ocClient:    ocClient,
		sessions:    sessions,
		topicClient: topicClient,
		httpClient:  &http.Client{Timeout: 10 * time.Second},
	}
	// Fetch bot persona at startup; non-fatal if it fails
	if p, err := topicClient.GetBotPersona(); err == nil {
		bot.persona = p
		slog.Info("bot persona loaded", "name", p.FirstName, "has_description", p.Description != "")
	} else {
		slog.Warn("failed to fetch bot persona", "error", err)
	}
	return bot
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

	slog.Info("webhook: message received",
		"chat_id", chatID,
		"chat_type", update.Message.Chat.Type,
		"text_len", len(text),
	)

	// Whitelist check
	if !b.isChatAllowed(chatID) {
		slog.Warn("webhook: chat not allowed", "chat_id", chatID)
		b.sendTelegram(chatID, 0, "Maaf, kamu tidak punya akses.")
		w.WriteHeader(http.StatusOK)
		return
	}

	// Parse command
	cmd := ParseMessage(text)

	// Determine chat context
	threadID := update.Message.MessageThreadID
	chatType := update.Message.Chat.Type
	isForum := (chatType == "supergroup" || chatType == "group")
	isPrivate := (chatType == "private")

	// Handle system commands locally
	switch cmd.Type {
	case CmdStart:
		b.sendTelegram(chatID, threadID, "🤖 Halo! Saya OMOTG, jembatan Telegram ke OpenCode. Kirim /help untuk bantuan.")
		w.WriteHeader(http.StatusOK)
		return

	case CmdHelp:
		b.sendTelegram(chatID, threadID, HelpText())
		w.WriteHeader(http.StatusOK)
		return

	case CmdSession:
		b.handleSessionCommand(chatID, threadID, cmd)
		w.WriteHeader(http.StatusOK)
		return

	case CmdTopic:
		b.handleTopicCommand(chatID, threadID, cmd)
		w.WriteHeader(http.StatusOK)
		return

	case CmdUnknown:
		b.sendTelegram(chatID, threadID, "Perintah tidak dikenal. Kirim /help untuk bantuan.")
		w.WriteHeader(http.StatusOK)
		return
	}

	// Resolve session ID — reuse existing or create new
	sessionID, isNew, resolvedThreadID, err := b.resolveSession(r.Context(), chatID, threadID, isForum, isPrivate)
	if err != nil {
		slog.Error("webhook: resolve session", "error", err)
		b.sendTelegram(chatID, threadID, fmt.Sprintf("❌ Gagal buat session: %s", err))
		w.WriteHeader(http.StatusOK)
		return
	}

	// Use the resolved threadID — may differ from original when a new forum topic was auto-created
	threadID = resolvedThreadID

	// Send acknowledgment
	var ackText string
	switch {
	case isNew && threadID > 0:
		ackText = fmt.Sprintf("⏳ Memproses... (topic: %d, session: `%s`)", threadID, sessionID)
	case isNew:
		ackText = fmt.Sprintf("⏳ Memproses... (session baru: `%s`)", sessionID)
	case threadID > 0:
		ackText = fmt.Sprintf("⏳ Lanjut session: `%s`", sessionID)
	default:
		ackText = fmt.Sprintf("⏳ Lanjut session: `%s`", sessionID)
	}
	if cmd.Type == CmdDeploy && isNew {
		ackText = fmt.Sprintf("🚀 Menjalankan: `%s`", cmd.RawText)
	}
	b.sendTelegram(chatID, threadID, ackText)

	// Send message to OpenCode (async — don't block webhook response)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), b.config.SessionTimeout)
		defer cancel()

		// Update last-used timestamp
		b.sessions.Renew(sessionID)

		responseText, err := b.ocClient.SendMessage(ctx, sessionID, cmd.Prompt)
		if err != nil {
			slog.Error("webhook: send message to OpenCode", "error", err, "session_id", sessionID)
			b.sendTelegram(chatID, threadID, fmt.Sprintf("❌ Gagal: %s", err))
			return
		}

		if responseText != "" {
			b.sendTelegram(chatID, threadID, responseText)
		}
		b.sendTelegram(chatID, threadID, fmt.Sprintf("✅ Selesai! (session: `%s`)", sessionID))
	}()

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "accepted"})
}

// resolveSession returns an existing session ID for reuse, or creates a new one.
// For private free-chat: reuses current session.
// For group topics: reuses topic-bound session.
// For General group chat: reuses the group's General session.
func (b *Bot) resolveSession(reqCtx context.Context, chatID, threadID int64, isForum, isPrivate bool) (sessionID string, isNew bool, resolvedThreadID int64, err error) {
	// Try to reuse existing session
	if isPrivate || (isForum && threadID == 0) {
		if entry := b.sessions.GetCurrentSession(chatID); entry != nil {
			return entry.SessionID, false, entry.ThreadID, nil
		}
	}
	if isForum && threadID > 0 {
		if entry := b.sessions.GetTopicSession(chatID, threadID); entry != nil {
			return entry.SessionID, false, entry.ThreadID, nil
		}
	}

	// Create new session
	ctx, cancel := context.WithTimeout(reqCtx, 10*time.Second)
	defer cancel()

	newID, err := b.ocClient.CreateSession(ctx)
	if err != nil {
		return "", false, 0, fmt.Errorf("create session: %w", err)
	}

	// Store the session with the original threadID — no auto-create.
	// Bot replies in the same context (General or topic) as the user's message.
	b.sessions.Store(newID, chatID, threadID, 0)
	return newID, true, threadID, nil
}

// handleSessionCommand processes /session sub-commands.
func (b *Bot) handleSessionCommand(chatID, threadID int64, cmd ParsedCommand) {
	switch cmd.SessionAct {
	case SessList:
		sessions := b.sessions.ListChatSessions(chatID)
		if len(sessions) == 0 {
			b.sendTelegram(chatID, threadID, "Belum ada session. Kirim pesan untuk memulai session baru.")
			return
		}
		var lines []string
		for i, s := range sessions {
			current := ""
			if cur := b.sessions.GetCurrentSession(chatID); cur != nil && cur.SessionID == s.SessionID {
				current = " ◀️ (aktif)"
			}
			created := s.CreatedAt.Format("15:04:05")
			preview := s.SessionID
			if len(preview) > 20 {
				preview = preview[:20] + "..."
			}
			lines = append(lines, fmt.Sprintf("%d. `%s`%s — %s", i+1, preview, current, created))
		}
		b.sendTelegram(chatID, threadID, "📋 Session:\n"+joinLines(lines))

	case SessSwitch:
		if cmd.SessionArg == "" {
			b.sendTelegram(chatID, threadID, "Gunakan: `/session switch <id>` — ganti session.")
			return
		}
		entry, ok := b.sessions.Load(cmd.SessionArg)
		if !ok {
			b.sendTelegram(chatID, threadID, fmt.Sprintf("Session `%s` tidak ditemukan.", cmd.SessionArg))
			return
		}
		if entry.ChatID != chatID {
			b.sendTelegram(chatID, threadID, "Session itu bukan milik chat ini.")
			return
		}
		b.sessions.SetCurrentSession(chatID, cmd.SessionArg)
		b.sendTelegram(chatID, threadID, fmt.Sprintf("✅ Beralih ke session: `%s`", cmd.SessionArg))

	case SessDelete:
		if cmd.SessionArg == "" {
			b.sendTelegram(chatID, threadID, "Gunakan: `/session delete <id>` — hapus session.")
			return
		}
		entry, ok := b.sessions.Load(cmd.SessionArg)
		if !ok {
			b.sendTelegram(chatID, threadID, fmt.Sprintf("Session `%s` tidak ditemukan.", cmd.SessionArg))
			return
		}
		if entry.ChatID != chatID {
			b.sendTelegram(chatID, threadID, "Session itu bukan milik chat ini.")
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := b.ocClient.DeleteSession(ctx, cmd.SessionArg); err != nil {
			slog.Warn("session delete: failed to delete on OpenCode server", "error", err, "session_id", cmd.SessionArg)
		}
		b.sessions.Delete(cmd.SessionArg)
		b.sendTelegram(chatID, threadID, fmt.Sprintf("🗑️ Session `%s` dihapus.", cmd.SessionArg))

	case SessNew:
		// Create new session
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		sessionID, err := b.ocClient.CreateSession(ctx)
		if err != nil {
			b.sendTelegram(chatID, threadID, fmt.Sprintf("❌ Gagal buat session baru: %s", err))
			return
		}
		b.sessions.Store(sessionID, chatID, threadID, 0)

		ackText := fmt.Sprintf("🆕 Session baru: `%s`", sessionID)
		if cmd.Prompt != "" {
			ackText += "\n⏳ Memproses..."
			b.sendTelegram(chatID, threadID, ackText)

			// Send first message to the new session
			go func() {
				ctx2, cancel2 := context.WithTimeout(context.Background(), b.config.SessionTimeout)
				defer cancel2()
				resp, err := b.ocClient.SendMessage(ctx2, sessionID, cmd.Prompt)
				if err != nil {
					b.sendTelegram(chatID, threadID, fmt.Sprintf("❌ Gagal: %s", err))
					return
				}
				if resp != "" {
					b.sendTelegram(chatID, threadID, resp)
				}
				b.sendTelegram(chatID, threadID, fmt.Sprintf("✅ Selesai! (session: `%s`)", sessionID))
			}()
		} else {
			b.sendTelegram(chatID, threadID, ackText)
		}

	default:
		// /session with no args: show current session info
		entry := b.sessions.GetCurrentSession(chatID)
		if entry == nil {
			b.sendTelegram(chatID, threadID, "Tidak ada session aktif. Kirim pesan untuk memulai.")
			return
		}
		elapsed := time.Since(entry.LastUsed).Truncate(time.Second)
		b.sendTelegram(chatID, threadID,
			fmt.Sprintf("📌 Session aktif: `%s`\nDibuat: %s\nTerakhir: %s (%s lalu)",
				entry.SessionID,
				entry.CreatedAt.Format("02 Jan 15:04:05"),
				entry.LastUsed.Format("15:04:05"),
				elapsed,
			))
	}
}

// handleTopicCommand processes /topic sub-commands.
func (b *Bot) handleTopicCommand(chatID, threadID int64, cmd ParsedCommand) {
	switch cmd.TopicAct {
	case TopicNew:
		if cmd.TopicName == "" {
			b.sendTelegram(chatID, threadID, "Gunakan: `/topic new <nama>` — buat topic forum baru.")
			return
		}
		b.handleTopicNew(chatID, threadID, cmd.TopicName)

	case TopicClose:
		if threadID == 0 {
			b.sendTelegram(chatID, threadID, "Kirim perintah ini dari dalam topic yang mau ditutup.")
			return
		}
		b.handleTopicClose(chatID, threadID)

	case TopicDelete:
		if threadID == 0 {
			b.sendTelegram(chatID, threadID, "Kirim perintah ini dari dalam topic yang mau dihapus.")
			return
		}
		b.handleTopicDelete(chatID, threadID)

	default:
		b.sendTelegram(chatID, threadID, "Gunakan: `/topic new <nama>` / `/topic close` / `/topic delete`")
	}
}

func (b *Bot) handleTopicNew(chatID, threadID int64, topicName string) {
	createdID, err := b.topicClient.CreateForumTopic(chatID, topicName)
	if err != nil {
		b.sendTelegram(chatID, threadID, fmt.Sprintf("❌ Gagal buat topic: %s", err))
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	sessionID, err := b.ocClient.CreateSession(ctx)
	if err != nil {
		b.sendTelegram(chatID, threadID, fmt.Sprintf("❌ Topic dibuat tapi gagal buat session: %s", err))
		return
	}
	b.sessions.Store(sessionID, chatID, createdID, 0)

	b.sendTelegram(chatID, threadID,
		fmt.Sprintf("✅ Topic `%s` berhasil dibuat! Session: `%s`", topicName, sessionID))

	welcome := "📂 Topic: **" + topicName + "**\n🆔 Session: `" + sessionID + "`"
	if b.persona != nil {
		welcome = b.persona.WelcomeMessage(topicName) + "\n🆔 Session: `" + sessionID + "`"
	}
	b.sendTelegram(chatID, createdID, welcome)

	slog.Info("webhook: topic created by command", "chat_id", chatID, "topic_id", createdID, "topic_name", topicName)
}

func (b *Bot) handleTopicClose(chatID, threadID int64) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var sessionID string
	if entry := b.sessions.GetTopicSession(chatID, threadID); entry != nil {
		sessionID = entry.SessionID
		if err := b.ocClient.DeleteSession(ctx, sessionID); err != nil {
			slog.Warn("webhook: delete session on topic close", "error", err, "session_id", sessionID)
		}
	}

	err := b.topicClient.CloseForumTopic(chatID, threadID)
	if err != nil {
		b.sendTelegram(chatID, threadID, fmt.Sprintf("❌ Gagal nutup topic: %s", err))
		return
	}

	if sessionID != "" {
		b.sessions.Delete(sessionID)
	}
	b.sessions.DeleteTopicBinding(chatID, threadID)
	b.sendTelegram(chatID, threadID, "🔒 Topic ditutup. Session OpenCode juga dihapus.")
	slog.Info("webhook: topic closed", "chat_id", chatID, "topic_id", threadID)
}

func (b *Bot) handleTopicDelete(chatID, threadID int64) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var sessionID string
	if entry := b.sessions.GetTopicSession(chatID, threadID); entry != nil {
		sessionID = entry.SessionID
		if err := b.ocClient.DeleteSession(ctx, sessionID); err != nil {
			slog.Warn("webhook: delete session on topic delete", "error", err, "session_id", sessionID)
		}
	}

	err := b.topicClient.DeleteForumTopic(chatID, threadID)
	if err != nil {
		b.sendTelegram(chatID, threadID, fmt.Sprintf("❌ Gagal hapus topic: %s", err))
		return
	}

	if sessionID != "" {
		b.sessions.Delete(sessionID)
	}
	b.sessions.DeleteTopicBinding(chatID, threadID)
	slog.Info("webhook: topic deleted", "chat_id", chatID, "topic_id", threadID)
}

// joinLines joins lines with newline.
func joinLines(lines []string) string {
	b := bytes.Buffer{}
	for i, l := range lines {
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(l)
	}
	return b.String()
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

// sendTelegram sends text to a Telegram chat via the Bot API.
// If threadID > 0, the message is sent to that forum topic.
// Messages longer than telegramMaxMessageLen are split into chunks.
func (b *Bot) sendTelegram(chatID int64, threadID int64, text string) {
	if b.config.BotToken == "" {
		slog.Error("sendTelegram: bot token not set")
		return
	}
	for _, chunk := range splitMessage(text, telegramMaxMessageLen) {
		b.sendTelegramChunk(chatID, threadID, chunk)
	}
}

func (b *Bot) sendTelegramChunk(chatID int64, threadID int64, text string) {
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
	defer func() {
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}()

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

// splitMessage splits s into chunks no longer than maxLen, breaking on UTF-8
// rune boundaries. Each chunk is at most maxLen runes.
func splitMessage(s string, maxLen int) []string {
	if utf8.RuneCountInString(s) <= maxLen {
		return []string{s}
	}
	var chunks []string
	for len(s) > 0 {
		if utf8.RuneCountInString(s) <= maxLen {
			chunks = append(chunks, s)
			break
		}
		// Find the last rune boundary within maxLen bytes.
		end := maxLen
		for end > 0 && !utf8.RuneStart(s[end]) {
			end--
		}
		if end == 0 {
			end = maxLen // fallback: hard break
		}
		chunks = append(chunks, s[:end])
		s = s[end:]
	}
	return chunks
}
