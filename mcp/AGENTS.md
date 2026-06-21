# mcp/ — MCP SSE Server (JSON-RPC 2.0 over SSE)

## OVERVIEW

Minimal MCP server exposing Telegram actions as MCP tools via SSE transport.

## STRUCTURE

```
mcp/
├── server.go   # SSE transport, JSON-RPC dispatcher, tool registry
└── tools.go    # Telegram MCP tool definitions + TelegramSender
```

## KEY TYPES

| Symbol | Role |
|--------|------|
| `Server` | MCP server with SSE client fan-out, tool registry, JSON-RPC dispatcher |
| `ToolDefinition` | Describes an MCP tool (name, description, JSON Schema input) |
| `HandlerFunc` | `func(ctx, json.RawMessage) (string, error)` — tool call handler |
| `TelegramSender` | HTTP client wrapping `telegram.org/bot<token>/sendMessage` |
| `RegisterTelegramTools` | Registers both tools on a Server |

## TOOL REGISTRATION PATTERN

```go
s.RegisterTool(ToolDefinition{...}, func(ctx, args) (string, error) { ... })
```

Two tools registered: `send_telegram_message` (raw text) and `send_telegram_notification` (emoji + status + message with MarkdownV2 fallback to plain text).

## INTERNAL CONVENTIONS

- **SSE transport** — GET `/mcp/sse` opens event stream, advertises message endpoint. POST `/mcp/message` decodes JSON-RPC 2.0, broadcasts response to all SSE clients, returns 202 Accepted.
- **All responses broadcast** — `respond()` sends the JSON-RPC response to every connected SSE client, not just the requester.
- **Notification fallback** — `send_telegram_notification` tries MarkdownV2 first, falls back to plain text on error.
- **MarkdownV2 escaping** — `escapeMarkdownV2()` escapes 18 reserved characters with backslash.
- **Client drop tolerance** — SSE client channels buffer 64 messages; slow clients get dropped silently.
- **No auth** — server binds to 127.0.0.1 only, assumes loopback trust.
- **No external deps** — same stdlib-only rule as parent.
