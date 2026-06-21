# OMOTG — Telegram ↔ OpenCode Bridge

**Stack:** Go 1.26, stdlib only (no external deps), OpenCode HTTP API, Telegram Bot API

## STRUCTURE

```
omotg/
├── main.go          # Entry point: config → OC client → bot → MCP → servers
├── config.go        # env-file config loader
├── bot/             # Telegram ↔ OpenCode bridge logic
├── mcp/             # MCP SSE server (exposes Telegram tools)
├── docs/            # Plans, handoffs
├── README.md        # Full user docs
└── env.template     # Config template
```

## WHERE TO LOOK

| Task | Location | Notes |
|------|----------|-------|
| Entry/startup | `main.go` | Loads config, wires deps, starts 2 HTTP servers |
| Telegram handler | `bot/handler.go` | Webhook, session routing, topic management |
| OpenCode client | `bot/opencode.go` | CRUD sessions, sync message send |
| Command parsing | `bot/router.go` | /session, /topic, /help, /deploy, /logs |
| Session map | `bot/session.go` | Thread-safe multi-session store |
| Telegram API | `bot/telegram.go` | TopicClient, BotPersona, Telegram HTTP calls |
| MCP server | `mcp/server.go` | SSE transport, JSON-RPC, tool registry |
| MCP tools | `mcp/tools.go` | send_message, send_notification |

## CODE MAP

| Symbol | Type | Pkg | Refs | Role |
|--------|------|-----|------|------|
| `main` | func | main | 1 | Entry point, wires all deps |
| `Config` | struct | main | 3 | Env-driven config |
| `LoadConfig` | func | main | 1 | Reads `~/.config/omotg/env` |
| `Bot` | struct | bot | 2 | Core handler, holds all deps |
| `BotConfig` | struct | bot | 1 | Bot constructor config |
| `NewBot` | func | bot | 1 | Wire bot with OC + sessions + topics |
| `HandleWebhook` | func | bot | 1 | POST /webhook handler |
| `SessionMap` | struct | bot | 3 | Thread-safe session store |
| `SessionEntry` | struct | bot | 4 | Per-session metadata |
| `OCClient` | struct | bot | 2 | OpenCode HTTP API client |
| `TopicClient` | struct | bot | 2 | Telegram forum topic API |
| `BotPersona` | struct | bot | 1 | Bot name/description from Telegram |
| `ParsedCommand` | struct | bot | 2 | Parsed /command from text |
| `Server` | struct | mcp | 2 | MCP SSE server |
| `TelegramSender` | struct | mcp | 2 | Sends Telegram messages from MCP |

## CONVENTIONS

- **Std lib only** — zero external dependencies beyond Go stdlib
- **Sync OpenCode API** — POST message → read response directly (no SSE/streaming)
- **Slog logging** — structured logging with slog.Info/Warn/Error
- **Error wrapping** — `fmt.Errorf("context: %w", err)` throughout
- **No framework** — bare `net/http` with Go 1.22+ route patterns (`POST /webhook`)
- **Binary name matches module** — output binary is `omotg`

## ANTI-PATTERNS (THIS PROJECT)

- Never import external packages — stdlib only
- No SSE/event streaming for OpenCode responses — sync HTTP only
- No goroutine leaks — every `go` call must have context/shutdown handling
- No credentials in code — all config via env file

## COMMANDS

```bash
go build -o omotg .              # Build binary
go test ./...                     # Run all tests
go vet ./...                      # Static analysis
go build -o omotg . && sudo cp omotg /usr/local/bin/  # Build + install
```

## NOTES

- Session cleanup (`StartCleanup`) is a no-op — sessions persist until explicit `/session delete` or `/topic close`
- Webhook registered on startup with self-signed cert via multipart upload
- MCP server binds to 127.0.0.1 only (not network-accessible)
- `systemctl --user restart omotg` for deployment after rebuild
