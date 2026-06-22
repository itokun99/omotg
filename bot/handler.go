package bot

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"
)

// telegramMaxMessageLen is the Telegram sendMessage character limit (safe margin).
const telegramMaxMessageLen = 4000

// omotgMarker is prepended to all prompts sent to OpenCode so the agent
// can detect the request came via Telegram (OMOTG) vs OpenCode TUI.
const omotgMarker = "[OMOTG]"

type msgResult struct {
	text string
	err  error
}

var htmlEscaper = strings.NewReplacer(
	"&", "&amp;",
	"<", "&lt;",
	">", "&gt;",
	"\"", "&quot;",
)

// safeTagRestorer restores known HTML tags after escaping, so Sisyphus can
// use <pre>, <code>, <b>, <i>, etc. in responses for Telegram rendering.
var safeTagRestorer = strings.NewReplacer(
	"&lt;pre&gt;", "<pre>",
	"&lt;/pre&gt;", "</pre>",
	"&lt;code&gt;", "<code>",
	"&lt;/code&gt;", "</code>",
	"&lt;b&gt;", "<b>",
	"&lt;/b&gt;", "</b>",
	"&lt;i&gt;", "<i>",
	"&lt;/i&gt;", "</i>",
	"&lt;strong&gt;", "<strong>",
	"&lt;/strong&gt;", "</strong>",
	"&lt;em&gt;", "<em>",
	"&lt;/em&gt;", "</em>",
	"&lt;u&gt;", "<u>",
	"&lt;/u&gt;", "</u>",
	"&lt;s&gt;", "<s>",
	"&lt;/s&gt;", "</s>",
	"&lt;blockquote&gt;", "<blockquote>",
	"&lt;/blockquote&gt;", "</blockquote>",
)

// escapeTelegramHTML escapes HTML special characters in s while preserving
// known safe tags (<pre>, <code>, <b>, <i>, <strong>, <em>, <u>, <s>, <blockquote>).
// This prevents raw <> from breaking Telegram's HTML parse mode.
func escapeTelegramHTML(s string) string {
	return safeTagRestorer.Replace(htmlEscaper.Replace(s))
}

func buildPrompt(chatID, threadID int64, sessionID, prompt string, persona *BotPersona) string {
	var b strings.Builder
	b.WriteString(prompt)
	b.WriteString("\n\nHere is the chat context for this message:\n")
	fmt.Fprintf(&b, " chat_id: %d\n", chatID)
	fmt.Fprintf(&b, " thread_id: %d\n", threadID)
	fmt.Fprintf(&b, " session_id: %s", sessionID)
	if persona != nil {
		fmt.Fprintf(&b, "\n bot_name: %s", persona.FirstName)
		if persona.Username != "" {
			fmt.Fprintf(&b, "\n bot_username: @%s", persona.Username)
		}
		if persona.Description != "" {
			fmt.Fprintf(&b, "\n bot_description: %s", persona.Description)
		}
	}
	return b.String()
}

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
			MessageID       int64           `json:"message_id"`
			MessageThreadID int64           `json:"message_thread_id,omitempty"`
			Chat            TelegramChat    `json:"chat"`
			Text            string          `json:"text,omitempty"`
			IsTopicMessage  bool            `json:"is_topic_message,omitempty"`
			ReplyToMessage  *ReplyToMessage `json:"reply_to_message,omitempty"`
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

	if !isPrivate && cmd.Type == CmdFreeChat && !isBotMentioned(text, b.persona) && !b.isReplyToBot(update.Message.ReplyToMessage) {
		slog.Debug("webhook: ignoring non-mentioned message in group", "chat_id", chatID)
		w.WriteHeader(http.StatusOK)
		return
	}

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

	// Send acknowledgment only for new sessions or deploy commands
	var ackText string
	switch {
	case isNew && threadID > 0:
		ackText = fmt.Sprintf("⏳ Memproses... (topic: %d, session: `%s`)", threadID, sessionID)
	case isNew:
		ackText = fmt.Sprintf("⏳ Session baru: `%s`", sessionID)
	case cmd.Type == CmdDeploy:
		ackText = fmt.Sprintf("🚀 Menjalankan: `%s`", cmd.RawText)
	}
	if ackText != "" {
		b.sendTelegram(chatID, threadID, ackText)
	}

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), b.config.SessionTimeout)
		b.processMessage(ctx, cancel, sessionID, chatID, threadID, cmd.Prompt)
	}()

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "accepted"})
}

// processMessage sends a prompt to an OpenCode session, processes SSE events
// for real-time progress, tracks child sessions, and forwards output to Telegram.
func (b *Bot) processMessage(parentCtx context.Context, cancel context.CancelFunc, sessionID string, chatID, threadID int64, promptText string) {
	defer cancel()

	ctx, cancelTimeout := context.WithTimeout(parentCtx, b.config.SessionTimeout)
	defer cancelTimeout()

	b.sessions.Renew(sessionID)
	prompt := buildPrompt(chatID, threadID, sessionID, promptText, b.persona)

	// Start Telegram typing indicator (auto-cancelled when ctx ends)
	typingCtx, typingCancel := context.WithCancel(ctx)
	defer typingCancel()
	b.startTyping(typingCtx, chatID, threadID)

	// Try SSE event stream first for real-time progress
	eventCtx, eventCancel := context.WithCancel(ctx)
	defer eventCancel()

	eventCh, sseErr := b.ocClient.ConnectEventStream(eventCtx)
	if sseErr != nil {
		slog.Warn("webhook: SSE stream unavailable, falling back to sync", "error", sseErr)
		b.sendSyncMessage(ctx, sessionID, chatID, threadID, prompt)
		return
	}

	msgCh := make(chan msgResult, 1)
	go func() {
		t, e := b.ocClient.SendMessage(ctx, sessionID, prompt)
		msgCh <- msgResult{t, e}
	}()

	// Track sessions we're monitoring
	trackedSessions := map[string]bool{sessionID: true}
	mainTextSent := false
	var childTimeout <-chan time.Time

	// Process events until main session completes
	for {
		select {
		case event, ok := <-eventCh:
			if !ok {
				eventCh = nil
				// Stream died; wait for POST response with timeout
				select {
				case res := <-msgCh:
					b.handleMsgResult(res, sessionID, chatID, threadID, &mainTextSent)
				case <-time.After(30 * time.Second):
					slog.Warn("webhook: SSE died and POST timeout", "session_id", sessionID)
				}
				return
			}
			b.handleSSEEvent(event, trackedSessions, chatID, threadID, &mainTextSent)

		case res := <-msgCh:
			if res.err != nil {
				slog.Error("webhook: send message", "error", res.err, "session_id", sessionID)
				b.sendTelegram(chatID, threadID, fmt.Sprintf("❌ Gagal: %s", res.err))
				return
			}
			if !mainTextSent && res.text != "" {
				b.sendTelegram(chatID, threadID, res.text)
			}

			// Check for child sessions
			children, cerr := b.ocClient.GetSessionChildren(ctx, sessionID)
			if cerr != nil {
				slog.Warn("webhook: get children", "error", cerr, "session_id", sessionID)
			} else if len(children) > 0 {
				for _, c := range children {
					trackedSessions[c] = true
				}
				b.sendTelegram(chatID, threadID, fmt.Sprintf("🔄 Menunggu %d sub-agent...", len(children)))
				childTimeout = time.After(60 * time.Second)
				// Continue processing to capture child events
				continue
			}

			// No children — done
			return

		case <-childTimeout:
			// Child sessions timeout — consider done
			return

		case <-ctx.Done():
			// Timeout or context cancelled
			return
		}
	}
}

// handleSSEEvent processes a single SSE event and forwards relevant updates to Telegram.
// Only tool events are forwarded for real-time progress; text events are skipped
// because the SSE stream echoes the user's own input and we rely on the POST
// response for the assistant's actual text output.
func (b *Bot) handleSSEEvent(event SSEEvent, trackedSessions map[string]bool, chatID, threadID int64, mainTextSent *bool) {
	if event.Type != "message.part.updated" {
		return
	}
	var props struct {
		SessionID string          `json:"sessionID"`
		Part      json.RawMessage `json:"part"`
	}
	if err := json.Unmarshal(event.Properties, &props); err != nil {
		return
	}
	if !trackedSessions[props.SessionID] {
		return
	}
	var part struct {
		Type  string          `json:"type"`
		Tool  string          `json:"tool,omitempty"`
		Input json.RawMessage `json:"input,omitempty"`
		State *PartState      `json:"state,omitempty"`
	}
	if err := json.Unmarshal(props.Part, &part); err != nil {
		return
	}
	if part.Type == "tool" && part.State != nil && part.State.Status == "running" {
		label := formatVerboseTool(part.Tool, part.Input)
		b.sendTelegram(chatID, threadID, fmt.Sprintf("%s %s", EmojiForTool(part.Tool), label))
	}
}

// formatVerboseTool extracts meaningful arguments from a tool's input JSON
// for display in Telegram. Falls back to "tool..." if input is empty.
func formatVerboseTool(tool string, input json.RawMessage) string {
	if len(input) == 0 || string(input) == "null" || string(input) == "{}" {
		return tool + "..."
	}
	var args struct {
		FilePath string `json:"filePath"`
		Path     string `json:"path"`
		Pattern  string `json:"pattern"`
		Command  string `json:"command"`
		Query    string `json:"query"`
		URL      string `json:"url"`
		Text     string `json:"text"`
		Prompt   string `json:"prompt"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return tool + "..."
	}
	s := ""
	switch {
	case args.Pattern != "":
		s = args.Pattern
	case args.FilePath != "":
		s = args.FilePath
	case args.Path != "":
		s = args.Path
	case args.Command != "":
		s = args.Command
	case args.Query != "":
		s = args.Query
	case args.URL != "":
		s = args.URL
	case args.Prompt != "":
		s = args.Prompt
	case args.Text != "":
		s = args.Text
	}
	if s == "" {
		return tool + "..."
	}
	// Trim very long arguments to fit Telegram message
	runes := []rune(s)
	if len(runes) > 80 {
		s = string(runes[:77]) + "..."
	}
	return tool + ": " + s
}

// handleMsgResult processes the result from SendMessage after SSE stream dies.
func (b *Bot) handleMsgResult(res msgResult, sessionID string, chatID, threadID int64, mainTextSent *bool) {
	if res.err != nil {
		slog.Error("webhook: send message", "error", res.err, "session_id", sessionID)
		b.sendTelegram(chatID, threadID, fmt.Sprintf("❌ Gagal: %s", res.err))
		return
	}
	if !*mainTextSent && res.text != "" {
		b.sendTelegram(chatID, threadID, res.text)
	}
}

// sendSyncMessage is the fallback used when SSE stream cannot be established.
func (b *Bot) sendSyncMessage(ctx context.Context, sessionID string, chatID, threadID int64, prompt string) {
	responseText, err := b.ocClient.SendMessage(ctx, sessionID, prompt)
	if err != nil {
		slog.Error("webhook: send message to OpenCode", "error", err, "session_id", sessionID)
		b.sendTelegram(chatID, threadID, fmt.Sprintf("❌ Gagal: %s", err))
		return
	}
	if responseText != "" {
		b.sendTelegram(chatID, threadID, responseText)
	}
}

// sendChatAction sends a Telegram chat action (e.g. "typing") to show bot activity.
func (b *Bot) sendChatAction(chatID, threadID int64, action string) {
	payload := map[string]interface{}{
		"chat_id": chatID,
		"action":  action,
	}
	if threadID > 0 {
		payload["message_thread_id"] = threadID
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return
	}
	url := fmt.Sprintf("https://api.telegram.org/bot%s/sendChatAction", b.config.BotToken)
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := b.httpClient.Do(req)
	if err != nil {
		return
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
}

// startTyping periodically sends the typing indicator until ctx is cancelled.
func (b *Bot) startTyping(ctx context.Context, chatID, threadID int64) {
	go func() {
		b.sendChatAction(chatID, threadID, "typing")
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				b.sendChatAction(chatID, threadID, "typing")
			case <-ctx.Done():
				return
			}
		}
	}()
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
				resp, err := b.ocClient.SendMessage(ctx2, sessionID, buildPrompt(chatID, threadID, sessionID, cmd.Prompt, b.persona))
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

func isBotMentioned(text string, persona *BotPersona) bool {
	if persona == nil || persona.Username == "" {
		return false
	}
	return strings.Contains(text, "@"+persona.Username)
}

// isReplyToBot checks if the message is a reply to one of the bot's own messages.
func (b *Bot) isReplyToBot(replyTo *ReplyToMessage) bool {
	if replyTo == nil || replyTo.From == nil || b.persona == nil {
		return false
	}
	return replyTo.From.ID == b.persona.ID
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

type ReplyToMessage struct {
	From *struct {
		ID int64 `json:"id"`
	} `json:"from"`
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
		"chat_id":    chatID,
		"text":       escapeTelegramHTML(text),
		"parse_mode": "HTML",
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
