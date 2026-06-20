# Group + Topics Support Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Enable OMOTG to work in Telegram groups with forum topics — one OpenCode session per topic, responses routed to the correct thread.

**Architecture:** When a user messages the bot in a forum group without a topic, the bot auto-creates a topic named after the session and routes all responses there. Subsequent messages in the same topic reuse the same session. Private chat behavior is unchanged.

**Tech Stack:** Go stdlib, Telegram Bot API (createForumTopic, sendMessage with message_thread_id)

**Depends on:** Multi-session already implemented (no 1:1 chat→session constraint).

---

## File Map

| File | Action | Responsibility |
|------|--------|---------------|
| `bot/session.go` | Modify | Add `ThreadID` field to `SessionInfo` |
| `bot/telegram.go` | **Create** | Forum topic API calls (`createForumTopic`) |
| `bot/handler.go` | Modify | Parse `message_thread_id` from webhook, manage topics, route responses |
| `bot/router.go` | No change | Command parsing stays same |

---

### Task 1: Add ThreadID to Session Model

**Files:**
- Modify: `bot/session.go`

- [ ] **Step 1: Add ThreadID to SessionInfo**

```go
// SessionInfo holds metadata about an active session.
type SessionInfo struct {
	ChatID    int64
	ThreadID  int64 // Telegram forum topic ID; 0 means no topic (private chat or non-forum group)
	CreatedAt time.Time
	ExpiresAt time.Time
}
```

- [ ] **Step 2: Update Store to accept ThreadID**

```go
// Store associates a session ID with a chat ID for the given duration.
func (sm *SessionMap) Store(sessionID string, chatID int64, threadID int64, timeout time.Duration) {
	now := time.Now()
	info := SessionInfo{
		ChatID:    chatID,
		ThreadID:  threadID,
		CreatedAt: now,
		ExpiresAt: now.Add(timeout),
	}

	sm.mu.Lock()
	sm.data[sessionID] = info
	sm.mu.Unlock()

	slog.Debug("session stored",
		"session_id", sessionID,
		"chat_id", chatID,
		"thread_id", threadID,
		"expires_at", info.ExpiresAt,
	)
}
```

- [ ] **Step 3: Update StoreIfNotExists similarly** (add ThreadID parameter, store it)

```go
func (sm *SessionMap) StoreIfNotExists(sessionID string, chatID int64, threadID int64, timeout time.Duration) bool {
	now := time.Now()
	info := SessionInfo{
		ChatID:    chatID,
		ThreadID:  threadID,
		CreatedAt: now,
		ExpiresAt: now.Add(timeout),
	}
	sm.mu.Lock()
	sm.data[sessionID] = info
	sm.mu.Unlock()
	slog.Debug("session stored", "session_id", sessionID, "chat_id", chatID, "thread_id", threadID)
	return true
}
```

- [ ] **Step 4: Verify build**

```bash
cd /home/ito/.local/bin/omotg && go build ./...
```

Expected: build succeeds (callers may fail — next task fixes those).

- [ ] **Step 5: Commit**

```bash
git add bot/session.go && git commit -m "feat: add ThreadID to SessionInfo for forum topic support"
```

---

### Task 2: Add Telegram API Client for Forum Topics

**Files:**
- Create: `bot/telegram.go`

- [ ] **Step 1: Create telegram.go with TopicClient**

```go
package bot

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"
)

// TopicClient handles Telegram API calls for forum topic management.
type TopicClient struct {
	botToken   string
	httpClient *http.Client
}

// NewTopicClient creates a TopicClient.
func NewTopicClient(botToken string) *TopicClient {
	return &TopicClient{
		botToken:   botToken,
		httpClient: &http.Client{Timeout: 10 * time.Second},
	}
}

// CreateForumTopic creates a new forum topic in a group and returns the topic ID.
// API: POST /bot{token}/createForumTopic
func (tc *TopicClient) CreateForumTopic(chatID int64, name string) (int64, error) {
	payload := map[string]interface{}{
		"chat_id": chatID,
		"name":    name,
	}
	body, _ := json.Marshal(payload)

	url := fmt.Sprintf("https://api.telegram.org/bot%s/createForumTopic", tc.botToken)
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return 0, fmt.Errorf("create forum topic request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := tc.httpClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("create forum topic: %w", err)
	}
	defer resp.Body.Close()

	var result struct {
		Ok          bool `json:"ok"`
		Description string `json:"description,omitempty"`
		Result      *struct {
			MessageThreadID int64 `json:"message_thread_id"`
		} `json:"result,omitempty"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return 0, fmt.Errorf("decode create forum topic response: %w", err)
	}
	if !result.Ok {
		return 0, fmt.Errorf("create forum topic failed: %s", result.Description)
	}
	if result.Result == nil {
		return 0, fmt.Errorf("create forum topic: empty result")
	}
	return result.Result.MessageThreadID, nil
}
```

- [ ] **Step 2: Verify build**

```bash
cd /home/ito/.local/bin/omotg && go build ./...
```

Expected: build succeeds.

- [ ] **Step 3: Commit**

```bash
git add bot/telegram.go && git commit -m "feat: add TopicClient for createForumTopic API"
```

---

### Task 3: Update sendTelegram to Support Topics

**Files:**
- Modify: `bot/handler.go`

- [ ] **Step 1: Update sendTelegram to accept optional threadID**

Change signature and add `message_thread_id` to payload when threadID > 0:

```go
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
```

- [ ] **Step 2: Add HTTPClient field to Bot** (if not already present)

Already has `httpClient` — good, no change needed.

- [ ] **Step 3: Verify build**

```bash
cd /home/ito/.local/bin/omotg && go build ./...
```

Expected: build fails — callers of `sendTelegram` need the new signature. Next task fixes all callers.

- [ ] **Step 4: Commit**

```bash
git add bot/handler.go && git commit -m "feat: add threadID param to sendTelegram"
```

---

### Task 4: Update All sendTelegram Call Sites

**Files:**
- Modify: `bot/handler.go`

- [ ] **Step 1: Audit all sendTelegram calls** — there are 8 call sites in handler.go. Each needs `0` for threadID (actual thread routing comes in Task 5).

Update every call from:
```go
b.sendTelegram(chatID, "...")
```
to:
```go
b.sendTelegram(chatID, 0, "...")
```

Call sites (all in `bot/handler.go`):

| Line (current) | Context |
|----------------|---------|
| ~80 | "Maaf, kamu tidak punya akses." |
| ~91 | "/start welcome" |
| ~96 | "/help" |
| ~101 | "Perintah tidak dikenal" |
| ~113 | "OpenCode server sedang tidak tersedia." |
| ~126 | ackText |
| ~132 | "Gagal mengirim perintah ke OpenCode" |
| ~155 | "Gagal subscribe ke event stream" |
| ~186 | error event |
| ~190 | accumulated response |
| ~194 | "✅ Selesai!" |

List is approximate — fix ALL occurrences of `b.sendTelegram(chatID,` in the file.

- [ ] **Step 2: Verify build**

```bash
cd /home/ito/.local/bin/omotg && go build ./...
```

Expected: build succeeds.

- [ ] **Step 3: Commit**

```bash
git add bot/handler.go && git commit -m "fix: update sendTelegram calls with threadID param"
```

---

### Task 5: Parse message_thread_id and Create Topics in Webhook

**Files:**
- Modify: `bot/handler.go`

This is the core logic change.

- [ ] **Step 1: Add TopicClient field to Bot struct**

```go
type Bot struct {
	config      *BotConfig
	ocClient    *OCClient
	sessions    *SessionMap
	topicClient *TopicClient
	httpClient  *http.Client
}
```

- [ ] **Step 2: Update NewBot to accept TopicClient**

```go
func NewBot(cfg *BotConfig, ocClient *OCClient, sessions *SessionMap, topicClient *TopicClient) *Bot {
	// ...
	return &Bot{
		config:      cfg,
		ocClient:    ocClient,
		sessions:    sessions,
		topicClient: topicClient,
		httpClient:  &http.Client{Timeout: 10 * time.Second},
	}
}
```

- [ ] **Step 3: Extend the webhook update struct to parse `message_thread_id` and `chat.type`**

```go
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
```

And update `TelegramChat`:

```go
type TelegramChat struct {
	ID   int64  `json:"id"`
	Type string `json:"type,omitempty"` // "private", "group", "supergroup"
}
```

- [ ] **Step 4: Add topic creation logic after session creation**

After `b.sessions.Store(...)` (replacing the direct Store call), insert:

```go
// Determine thread ID (forum topic) for this session
threadID := update.Message.MessageThreadID
chatType := update.Message.Chat.Type
isForum := (chatType == "supergroup" || chatType == "group")

// If message is in a group with forum topics but NOT in a topic yet, create one
if isForum && threadID == 0 {
	// Use a short session-based topic name
	topicName := fmt.Sprintf("OMOTG-%.6s", sessionID)
	createdID, err := b.topicClient.CreateForumTopic(chatID, topicName)
	if err != nil {
		// Fallback: send to general even if topic creation fails
		slog.Error("webhook: create forum topic", "error", err, "chat_id", chatID)
		b.sendTelegram(chatID, 0, "⚠️ Tidak bisa membuat topic baru. Kirim pesan di topic yang sudah ada.")
		w.WriteHeader(http.StatusOK)
		return
	}
	threadID = createdID
	slog.Info("webhook: created forum topic", "chat_id", chatID, "topic_id", threadID, "topic_name", topicName)
}
```

- [ ] **Step 5: Update Store call to pass threadID**

```go
b.sessions.Store(sessionID, chatID, threadID, b.config.SessionTimeout)
```

- [ ] **Step 6: Update ackText to include topic info if in forum**

```go
ackText := fmt.Sprintf("⏳ Memproses... (session: `%s`)", sessionID)
if threadID > 0 {
	ackText = fmt.Sprintf("⏳ Memproses... (topic: %d, session: `%s`)", threadID, sessionID)
}
```

- [ ] **Step 7: Pass threadID to streamSessionEvents**

Change `streamSessionEvents` signature and calls:

```go
go b.streamSessionEvents(sessionID, chatID, threadID)
```

And the function:

```go
func (b *Bot) streamSessionEvents(sessionID string, chatID int64, threadID int64) {
```

- [ ] **Step 8: Update all sendTelegram calls in streamSessionEvents to use threadID**

Every `b.sendTelegram(chatID, ...)` in the streaming function becomes `b.sendTelegram(chatID, threadID, ...)`.

- [ ] **Step 9: Verify build**

```bash
cd /home/ito/.local/bin/omotg && go build ./...
```

Expected: build succeeds.

- [ ] **Step 10: Commit**

```bash
git add bot/handler.go && git commit -m "feat: parse message_thread_id, auto-create forum topics for new sessions"
```

---

### Task 6: Update main.go to Wire TopicClient

**Files:**
- Modify: `main.go`

- [ ] **Step 1: Create TopicClient and pass to NewBot**

Find the `NewBot` call site and update:

```go
topicClient := bot.NewTopicClient(cfg.TelegramBotToken)
b := bot.NewBot(botConfig, ocClient, sessions, topicClient)
```

- [ ] **Step 2: Verify build**

```bash
cd /home/ito/.local/bin/omotg && go build -o omotg .
```

Expected: build succeeds.

- [ ] **Step 3: Commit**

```bash
git add main.go && git commit -m "feat: wire TopicClient into Bot"
```

---

### Task 7: Update .gitignore and README

**Files:**
- Modify: `.gitignore`
- Modify: `README.md`

- [ ] **Step 1: No .gitignore change needed** — already covers certs.

- [ ] **Step 2: Update README** — add group/topic section to Usage:

```markdown
### Group & Forum Topics

OMOTG supports Telegram groups with forum topics enabled:

1. Add the bot as a **group admin** with `can_manage_topics` permission.
2. Enable **Topics** in the group settings.
3. Send any message in the group — the bot auto-creates a topic per session.

Messages sent in the **General** thread without a topic will create a new topic.
Messages sent in an existing topic run in that topic's session.
```

- [ ] **Step 3: Commit**

```bash
git add README.md && git commit -m "docs: add group & forum topic usage"
```

---

### Task 8: Build, Deploy, and Verify

**Files:**
- No changes — just build and deploy.

- [ ] **Step 1: Build production binary**

```bash
cd /home/ito/.local/bin/omotg && go build -o omotg .
```

Expected: exit 0, binary created.

- [ ] **Step 2: Restart service**

```bash
systemctl --user restart omotg
sleep 2
systemctl --user status omotg --no-pager | head -10
```

Expected: active (running).

- [ ] **Step 3: Verify webhook is still registered**

```bash
grep TELEGRAM_BOT_TOKEN ~/.config/omotg/env | cut -d= -f2 | xargs -I{} curl -s "https://api.telegram.org/bot{}/getWebhookInfo" | python3 -m json.tool
```

Expected: `has_custom_certificate: true`, `url: https://lizbot.indrawan.dev:8443/webhook`

- [ ] **Step 4: Inline test via Telegram** — send a message in the group (if created) or verify private chat still works.

- [ ] **Step 5: Push all commits**

```bash
git push
```

---

## Telegram API Reference

### createForumTopic

```
POST https://api.telegram.org/bot<TOKEN>/createForumTopic
Content-Type: application/json

{
  "chat_id": -1001234567890,
  "name": "Topic Name"
}
```

Response:
```json
{
  "ok": true,
  "result": {
    "message_thread_id": 42
  }
}
```

**Requirements:**
- Bot must be admin in the group with `can_manage_topics` right
- Group must have Topics enabled (Forum mode)
- `name` must be 1–128 characters

### sendMessage (forum-aware)

```
POST https://api.telegram.org/bot<TOKEN>/sendMessage
Content-Type: application/json

{
  "chat_id": -1001234567890,
  "message_thread_id": 42,
  "text": "Hello from topic!"
}
```

### Webhook Update (forum-aware)

```json
{
  "update_id": 12345,
  "message": {
    "message_id": 678,
    "message_thread_id": 42,
    "is_topic_message": true,
    "chat": {
      "id": -1001234567890,
      "type": "supergroup"
    },
    "text": "hello"
  }
}
```

## Self-Review Checklist

- [x] **Spec coverage:** All requirements covered: group support (Tasks 5-6), topic auto-creation (Task 5), response routing (Tasks 3-5), private chat unchanged (Task 4 uses threadID=0).
- [x] **No placeholders:** Every step has actual code or commands.
- [x] **Type consistency:** `ThreadID int64` consistent across `SessionInfo`, `Store`, `sendTelegram`, `streamSessionEvents`.
- [ ] **Missing:** Need to verify the `NewTopicClient` is imported correctly in `handler.go` — it's in the same `bot` package, so no import needed.
