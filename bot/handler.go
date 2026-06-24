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
	"sync"
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
	b.WriteString("\n")
	b.WriteString("## OMOTG Behavioral Directives\n")
	b.WriteString("This request arrived via Telegram. Follow these rules:\n")
	b.WriteString("\n")
	b.WriteString("1. Progress notifications: BEFORE each major tool call (bash, delegation, long operation), send a brief update via MCP send_telegram_message tool. Keep to 1 line.\n")
	b.WriteString("2. Delegation: When delegating to a sub-agent, announce who and what task.\n")
	b.WriteString("3. Conciseness: Do NOT spam. Batch related ops under one notification.\n")
	b.WriteString("4. Errors: If a tool fails, report it briefly.\n")
	b.WriteString("5. Completion: End with a clear summary of what was done.\n")
	b.WriteString("6. If MCP tools unavailable: skip notifications, just deliver final result.\n")
	if persona != nil {
		fmt.Fprintf(&b, "7. Persona aliasing: You are the Telegram bot \"%s\". When the user asks who you are, introduce yourself as \"%s\". Do not mention your internal agent name.\n", persona.FirstName, persona.FirstName)
	}
	return b.String()
}

// BotConfig holds configuration for the Telegram bot handler.
type BotConfig struct {
	SecretToken    string
	AllowedChatIDs []int64
	SessionTimeout time.Duration
	BotToken       string
	Agent          string // OpenCode agent display name ("" = server default)
}

// Bot handles incoming Telegram webhooks and forwards commands to OpenCode.
type Bot struct {
	config      *BotConfig
	ocClient    *OCClient
	sessions    *SessionMap
	topicClient *TopicClient
	httpClient  *http.Client
	persona     *BotPersona
	agent       string // OpenCode agent display name, may be "" for default

	rateLimitMu  sync.Mutex
	lastSendTime time.Time // guarded by rateLimitMu
	lastSendChat int64     // guarded by rateLimitMu — per-chat rate tracking
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
		agent:       cfg.Agent,
	}
	// Fetch bot persona at startup; non-fatal if it fails
	if p, err := topicClient.GetBotPersona(); err == nil {
		bot.persona = p
		slog.Info("bot persona loaded", "name", p.FirstName, "has_description", p.Description != "", "agent", bot.agent)
	} else {
		slog.Warn("failed to fetch bot persona", "error", err, "agent", bot.agent)
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

	completionReason := "unknown"
	defer func() {
		slog.Info("processMessage: completed", "session_id", sessionID, "reason", completionReason)
	}()
	slog.Info("processMessage: starting", "session_id", sessionID, "chat_id", chatID)

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
		completionReason = "sse_unavailable"
		return
	}

	msgCh := make(chan msgResult, 1)
	go func() {
		t, e := b.ocClient.SendMessageWithAgent(ctx, sessionID, prompt, b.agent)
		msgCh <- msgResult{t, e}
	}()

	// Track sessions we're monitoring
	trackedSessions := map[string]bool{sessionID: true}
	toolInputCache := make(map[string]json.RawMessage)
	mainTextSent := false
	hadChildren := false
	var childTimer *time.Timer
	var childTimeout <-chan time.Time
	var childTexts *strings.Builder

	// Process events until main session completes
	for {
		select {
		case event, ok := <-eventCh:
			if !ok {
				eventCh = nil
				if msgCh != nil {
					select {
					case res := <-msgCh:
						b.handleMsgResult(res, sessionID, chatID, threadID, &mainTextSent)
					case <-ctx.Done():
						completionReason = "session_timeout"
						return
					case <-time.After(30 * time.Second):
						slog.Warn("webhook: SSE died and POST timeout, retrying", "session_id", sessionID)
						b.sendTelegram(chatID, threadID, "⏳ Proses masih berjalan, mohon tunggu...")
						select {
						case res := <-msgCh:
							b.handleMsgResult(res, sessionID, chatID, threadID, &mainTextSent)
						case <-ctx.Done():
							completionReason = "session_timeout"
							return
						}
					}
				}
				if childTexts != nil && childTexts.Len() > 0 {
					b.sendTelegram(chatID, threadID, childTexts.String())
				}
				if hadChildren && mainTextSent {
					b.sendTelegram(chatID, threadID, "✅ Selesai!")
				}
				completionReason = "completed"
				return
			}
			b.handleSSEEvent(event, trackedSessions, toolInputCache, chatID, threadID, &mainTextSent, childTexts)
			if childTimer != nil {
				if !childTimer.Stop() {
					select {
					case <-childTimeout:
					default:
					}
				}
				childTimer.Reset(60 * time.Second)
				childTimeout = childTimer.C
			}

		case res := <-msgCh:
			if res.err != nil {
				slog.Error("webhook: send message", "error", res.err, "session_id", sessionID)
				b.sendTelegram(chatID, threadID, fmt.Sprintf("❌ Gagal: %s", res.err))
				completionReason = "send_message_error"
				return
			}
			if !mainTextSent && res.text != "" {
				b.sendTelegram(chatID, threadID, res.text)
				mainTextSent = true
			}

			// Check for child sessions
			children, cerr := b.ocClient.GetSessionChildren(ctx, sessionID)
			if cerr != nil {
				slog.Warn("webhook: get children", "error", cerr, "session_id", sessionID)
			} else if len(children) > 0 {
				hadChildren = true
				for _, c := range children {
					trackedSessions[c] = true
				}
				b.sendTelegram(chatID, threadID, fmt.Sprintf("🔄 Menunggu %d sub-agent...", len(children)))
				childTimer = time.NewTimer(60 * time.Second)
				childTimeout = childTimer.C
				childTexts = new(strings.Builder)
				msgCh = nil // no goroutine will send again
				continue
			}

			// No children — done
			if hadChildren && mainTextSent {
				b.sendTelegram(chatID, threadID, "✅ Selesai! Ada yang bisa dibantu lagi?")
			}
			completionReason = "completed"
			return

		case <-childTimeout:
			if childTexts != nil && childTexts.Len() > 0 {
				b.sendTelegram(chatID, threadID, childTexts.String())
			}
			b.sendTelegram(chatID, threadID, "⏱️ Waktu tunggu sub-agent habis. Hasil mungkin tidak lengkap.")
			completionReason = "child_timeout"
			return

		case <-ctx.Done():
			b.sendTelegram(chatID, threadID, "⏱️ Session timeout. Silakan kirim ulang pesan.")
			if childTexts != nil && childTexts.Len() > 0 {
				b.sendTelegram(chatID, threadID, childTexts.String())
			}
			completionReason = "session_timeout"
			return
		}
	}
}

// handleSSEEvent processes a single SSE event and forwards relevant updates to Telegram.
// Tool events are forwarded for real-time progress. Text events from child sessions
// are collected for final delivery (sent when SSE stream ends or timeout fires).
// toolInputCache persists tool inputs across events — OpenCode sends input only on the
// initial part creation (status="pending"), not on subsequent "running" updates.
func (b *Bot) handleSSEEvent(event SSEEvent, trackedSessions map[string]bool, toolInputCache map[string]json.RawMessage, chatID, threadID int64, mainTextSent *bool, childTexts *strings.Builder) {
	switch event.Type {
	case "message.part.delta":
		if childTexts == nil {
			return
		}
		var delta struct {
			SessionID string `json:"sessionID"`
			Field     string `json:"field"`
			Delta     string `json:"delta"`
		}
		if err := json.Unmarshal(event.Properties, &delta); err != nil {
			return
		}
		if !trackedSessions[delta.SessionID] {
			return
		}
		if delta.Field == "text" && delta.Delta != "" {
			childTexts.WriteString(delta.Delta)
		}

	case "message.part.updated":
		var props PartUpdateProps
		if err := json.Unmarshal(event.Properties, &props); err != nil {
			return
		}
		if !trackedSessions[props.SessionID] {
			return
		}
		var part PartData
		if err := json.Unmarshal(props.Part, &part); err != nil {
			return
		}
		if part.Type == "text" && part.Text != "" {
			if childTexts != nil {
				if childTexts.Len() > 0 {
					childTexts.WriteString("\n\n")
				}
				childTexts.WriteString(part.Text)
			}
			return
		}
		if part.Type == "tool" {
			// DEBUG: log ALL tool events with raw part data to understand OpenCode's SSE structure
			statusStr := "<nil>"
			if part.State != nil {
				statusStr = part.State.Status
			}
			slog.Info("handleSSEEvent: tool event",
				"tool", part.Tool,
				"status", statusStr,
				"id", props.SessionID,
				"input_len", len(part.Input),
				"input_raw", string(part.Input),
				"part_json", string(props.Part),
			)
			// OpenCode SSE puts tool input inside state.input, NOT at the top-level
			// "input" field. So part.Input is always empty. We read from state.Input instead.
			toolInput := part.Input
			if len(toolInput) == 0 && part.State != nil && len(part.State.Input) > 0 {
				toolInput = part.State.Input
			}
			if len(toolInput) > 0 {
				toolInputCache[part.Tool] = toolInput
				toolInputCache["@last_with_input"] = toolInput
			}
			if part.State != nil && part.State.Status == "running" {
				toolName := part.Tool
				if toolName == "task" || toolName == "subagent" || strings.Contains(toolName, "delegat") {
					input := toolInput
					if len(input) == 0 {
						if cached, ok := toolInputCache[toolName]; ok {
							input = cached
						} else if last, ok := toolInputCache["@last_with_input"]; ok {
							input = last
						}
					}
					label := formatVerboseTool(toolName, input)
					b.sendTelegram(chatID, threadID, fmt.Sprintf("%s %s", EmojiForTool(toolName), label))
				}
			}
		}
	}
}

// formatVerboseTool extracts meaningful arguments from a tool's input JSON
// for display in Telegram. Falls back to "tool..." if input is empty.
func formatVerboseTool(tool string, input json.RawMessage) string {
	// DEBUG: always log so we can see what the raw input looks like
	slog.Info("formatVerboseTool input", "tool", tool, "input_raw", string(input), "len", len(input))
	if len(input) == 0 || string(input) == "null" || string(input) == "{}" {
		return tool + "..."
	}
	var args struct {
		Description string          `json:"description"`
		Pattern     string          `json:"pattern"`
		Command     string          `json:"command"`
		Selector    string          `json:"selector"`
		Element     string          `json:"element"`
		FilePath    string          `json:"filePath"`
		Path        string          `json:"path"`
		Name        string          `json:"name"`
		Query       string          `json:"query"`
		URL         string          `json:"url"`
		Category    string          `json:"category"`
		Prompt      string          `json:"prompt"`
		Text        string          `json:"text"`
		Content     string          `json:"content"`
		Todos       json.RawMessage `json:"todos"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return tool + "..."
	}
	s := ""
	switch {
	case args.Description != "":
		s = args.Description
	case args.Pattern != "":
		s = args.Pattern
	case args.Command != "":
		s = args.Command
	case args.Selector != "":
		s = args.Selector
	case args.Element != "":
		s = args.Element
	case args.FilePath != "":
		s = args.FilePath
	case args.Path != "":
		s = args.Path
	case args.Name != "":
		s = args.Name
	case args.Query != "":
		s = args.Query
	case args.URL != "":
		s = args.URL
	case args.Category != "":
		s = args.Category
	case args.Prompt != "":
		s = args.Prompt
	case args.Text != "":
		s = args.Text
	case args.Content != "":
		s = args.Content
	case len(args.Todos) > 0:
		var todos []struct {
			Content string `json:"content"`
		}
		if json.Unmarshal(args.Todos, &todos) == nil && len(todos) > 0 && todos[0].Content != "" {
			s = todos[0].Content
		}
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
		*mainTextSent = true
	}
}

// sendSyncMessage is the fallback used when SSE stream cannot be established.
func (b *Bot) sendSyncMessage(ctx context.Context, sessionID string, chatID, threadID int64, prompt string) {
	responseText, err := b.ocClient.SendMessageWithAgent(ctx, sessionID, prompt, b.agent)
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
		slog.Error("sendChatAction: marshal", "error", err)
		return
	}
	url := fmt.Sprintf("https://api.telegram.org/bot%s/sendChatAction", b.config.BotToken)
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		slog.Error("sendChatAction: create request", "error", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := b.httpClient.Do(req)
	if err != nil {
		slog.Warn("sendChatAction: HTTP call", "error", err, "chat_id", chatID)
		return
	}
	defer resp.Body.Close()

	var actResp struct {
		Ok          bool   `json:"ok"`
		Description string `json:"description,omitempty"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&actResp); err != nil {
		return
	}
	if !actResp.Ok {
		slog.Warn("sendChatAction: telegram error",
			"description", actResp.Description,
			"chat_id", chatID,
		)
	}
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
				resp, err := b.ocClient.SendMessageWithAgent(ctx2, sessionID, buildPrompt(chatID, threadID, sessionID, cmd.Prompt, b.persona), b.agent)
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
	// Rate limit: enforce minimum 500ms gap between sends to avoid
	// Telegram "Too Many Requests" errors from burst tool notifications.
	b.rateLimitMu.Lock()
	elapsed := time.Since(b.lastSendTime)
	if elapsed < 500*time.Millisecond {
		b.rateLimitMu.Unlock()
		time.Sleep(500*time.Millisecond - elapsed)
	} else {
		b.lastSendTime = time.Now()
		b.rateLimitMu.Unlock()
	}

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
