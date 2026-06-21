# bot/ — Telegram ↔ OpenCode Bridge Package

**Package:** `bot` (import `"omotg/bot"`)

## FILES & OWNERSHIP

| File | Lines | Owns |
|------|-------|------|
| `handler.go` | 570 | Webhook dispatch, session resolution, command handlers, Telegram message send |
| `router.go` | 179 | Command parser (`ParseMessage`), command/topic action enums, help text |
| `session.go` | 205 | `SessionMap` — thread-safe multi-session store with topic bindings |
| `opencode.go` | 142 | `OCClient` — OpenCode HTTP API (create/delete session, send message) |
| `telegram.go` | 194 | `TopicClient` — Telegram forum topic CRUD, `BotPersona` fetch/welcome |

## KEY TYPES

| Type | File | Role |
|------|------|------|
| `Bot` | handler.go | Core handler: holds `BotConfig`, `OCClient`, `SessionMap`, `TopicClient`, `BotPersona` |
| `BotConfig` | handler.go | SecretToken, AllowedChatIDs, SessionTimeout, BotToken |
| `ParsedCommand` | router.go | Type + sub-actions (SessionAct, TopicAct) + Arg + Prompt |
| `CommandType` | router.go | 9 types: CmdUnknown/Start/Help/Status/Deploy/Logs/FreeChat/Session/Topic |
| `SessionEntry` | session.go | SessionID, ChatID, ThreadID, CreatedAt, LastUsed |
| `SessionMap` | session.go | sync.RWMutex, 3 maps: entries, chatCurrent, topicBind |
| `OCClient` | opencode.go | BasicAuth, CreateSession/DeleteSession/SendMessage |
| `TopicClient` | telegram.go | CreateForumTopic, CloseForumTopic, DeleteForumTopic |
| `BotPersona` | telegram.go | FirstName, Description, WelcomeMessage() |

## MESSAGE FLOW

```
Telegram webhook POST /webhook
  → handler.go: HandleWebhook()
    → Verify X-Telegram-Bot-Api-Secret-Token
    → Whitelist check (isChatAllowed)
    → router.go: ParseMessage() → ParsedCommand
    → Resolve session (resolveSession): private → reuse/chatCurrent | group → topicBind
    → Send ack → go ocClient.SendMessage()
    → Response → sendTelegram() with UTF-8 chunking
```

## SESSION LIFECYCLE

- **Private chat**: 1 `chatCurrent` per chatID. `/session switch` changes it. No auto-create.
- **Group forum**: Topic-bound via `"chatID:threadID"` → sessionID map. Each topic = 1 session.
- **General group** (threadID=0): Treated like private chat (chatCurrent).
- **Persistence**: No expiry. `StartCleanup` / `CleanupExpired` are no-ops.
- **Deletion**: Only via `/session delete` or `/topic close`/`/topic delete`. Cleans all 3 maps.

## COMMAND PARSING (router.go)

- Non-`/` text → `CmdFreeChat` with prompt = raw text
- `/session` sub-commands: `new`, `list`, `switch`, `delete` (+ Indonesian aliases)
- `/topic` sub-commands: `new <name>`, `close`, `delete`
- `/deploy`, `/logs`, `/status`, `/start`, `/help`

## CONVENTIONS (bot-specific)

- **goroutines**: Async `go func()` for `SendMessage` — webhook returns 200 immediately, response sent later
- **Message chunking**: `splitMessage()` splits at UTF-8 rune boundaries, 4000 rune max per chunk
- **Timeout**: 10s for session ops, `BotConfig.SessionTimeout` for message send (configured)
- **Persona**: Loaded at startup via `NewBot` → `GetBotPersona()`, non-fatal on failure
- **Topic lifecycle**: `handleTopicNew` creates topic + session atomically; `handleTopicClose/Delete` cleans up both
