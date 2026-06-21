# Handoff — Landing Page & Documentation Site for OMOTG

## Project Overview

**OMOTG** — Telegram ↔ OpenCode Bridge.
A Go-based bidirectional bridge that lets you talk to OpenCode AI agent directly from Telegram. Both private chat and group forum topics supported.

- **Repo (private):** https://github.com/itokun99/omotg
- **Homebrew tap (public):** https://github.com/itokun99/homebrew-omotg
- **License:** CC BY-NC 4.0 (non-commercial)
- **Latest version:** v0.2.0
- **Install:** `brew tap itokun99/omotg && brew install omotg`

---

## Design References

### Landing Page — Model after https://omo.dev

[omo.dev](https://omo.dev) is the reference for the landing page. Key patterns to follow:

- **Dark theme** with minimal, modern aesthetic
- **Hero section** with big stats (GitHub stars, downloads, etc.)
- **Terminal/code aesthetic** — show commands like `brew install omotg` in a terminal style
- **Bold typography** — short punchy headings, phases/workflow steps with numbers
- **Feature cards** — icon + title + short description
- **Architecture diagram** — clean ASCII-style or SVG flow diagram
- **Testimonials** section (if available)
- **Strong CTA** at bottom ("Install OMOTG" or similar)
- **Stats bar** showing key metrics

### Documentation Page — Model after https://opencode.ai/docs

[opencode.ai/docs](https://opencode.ai/docs) is the reference for the documentation. Key patterns:

- **Clean doc layout** with sidebar navigation
- **Code tabs** for different install methods (Homebrew, from source, etc.)
- **Step-by-step numbered guides**
- **Callout/info boxes** (Tip, Note, Warning)
- **Terminal window snippets** with copy button
- **Configuration table** with variable name, description, default
- **On-this-page** sidebar table of contents

---

## Target Audience

- **Primary:** Developers using OpenCode (AI coding agent) who want to interact with it from Telegram
- **Secondary:** Anyone who wants to run an AI coding agent remotely from their phone
- **Tech level:** Intermediate to advanced — comfortable with terminal, Go, systemd, TLS

---

## Architecture Overview (to communicate on the page)

```
Telegram App          Server (VPS)                    OpenCode CLI
    │                      │                              │
    │  POST /webhook       │                              │
    │  ────────────────────→  omotg (port 8443, TLS)      │
    │                      │    │                         │
    │                      │    │ POST /session           │
    │                      │    │ POST /session/{id}/msg  │
    │                      │    │ DELETE /session/{id}    │
    │                      │    └───────────────────────→ opencode serve
    │                      │                              │ (port 4096)
    │                      │                              │
    │  sendMessage         │                              │
    │  ←────────────────────  omotg (sync response)       │
    │                      │    │                         │
    │                      │    │ MCP SSE endpoint        │
    │                      │    │ http://127.0.0.1:9090   │
    │                      │    │                         │
    │                      │    │ ←── MCP tools ────── OpenCode
```

### Ports

| Port | Service | Bind | Protocol |
|------|---------|------|----------|
| 8443 | OMOTG Webhook | `0.0.0.0` | HTTPS (TLS) |
| 9090 | OMOTG MCP SSE | `127.0.0.1` | HTTP/SSE |
| 4096 | OpenCode Serve | `127.0.0.1` | HTTP/REST |

---

## Key Features to Highlight

1. **Telegram Bot** — Send messages to OpenCode from Telegram, get responses back
2. **MCP SSE Server** — Exposes `send_message` and `send_notification` tools to any MCP client
3. **Multi-Session** — Create, switch, and delete multiple OpenCode sessions from Telegram
4. **Forum Topic Support** — Works with Telegram supergroup forums; `/topic new` creates dedicated topic + session
5. **BotPersona** — Bot reads its Telegram name & description for personalized welcome messages
6. **Systemd Integration** — Ships with systemd user service files for production deployment
7. **Secure** — Secret token verification, chat ID whitelist, self-signed TLS cert
8. **Homebrew Package** — `brew tap itokun99/omotg && brew install omotg`

---

## Telegram Commands (for Docs)

| Command | Description |
|---------|-------------|
| `/start` | Welcome message |
| `/help` | Show available commands |
| `/status` | Check server status via OpenCode |
| `/deploy <env>` | Deploy application to environment |
| `/logs [N]` | Show last N lines of server logs (default: 50) |
| `/session` | Show current session info |
| `/session new [text]` | Create a new session (optionally with first prompt) |
| `/session list` | List all sessions |
| `/session switch <id>` | Switch to a different session |
| `/session delete <id>` | Delete a session |
| `/topic new <nama>` | Create a new forum topic with bound session (group only) |
| `/topic close` | Close the current forum topic (group only) |
| `/topic delete` | Permanently delete the current forum topic (group only) |

---

## Configuration Options (for Docs)

### Required

| Variable | Description |
|----------|-------------|
| `TELEGRAM_BOT_TOKEN` | Bot token from @BotFather |
| `TELEGRAM_WEBHOOK_URL` | Public HTTPS URL (e.g. `https://your.domain:8443/webhook`) |
| `TELEGRAM_SECRET_TOKEN` | Secret string to verify webhook requests |
| `OPENCODE_SERVER_PASSWORD` | Password for OpenCode API |

### Optional

| Variable | Default | Description |
|----------|---------|-------------|
| `OPENCODE_SERVER_URL` | `http://127.0.0.1:4096` | OpenCode serve URL |
| `OMOTG_WEBHOOK_PORT` | `8443` | Webhook TLS listen port |
| `OMOTG_MCP_PORT` | `9090` | MCP SSE server port |
| `OMOTG_ALLOWED_CHAT_IDS` | (all allowed) | Comma-separated Telegram chat IDs |
| `OMOTG_SESSION_TIMEOUT` | `300` | Session timeout in seconds |
| `OMOTG_TLS_CERT_FILE` | `~/.config/omotg/webhook.crt` | TLS cert path |
| `OMOTG_TLS_KEY_FILE` | `~/.config/omotg/webhook.key` | TLS key path |

---

## Suggested Landing Page Sections

1. **Hero** — "Chat with AI Agents from Telegram" + terminal animation/command
2. **Stats** — GitHub stars, Homebrew installs, sessions served (if available)
3. **How It Works** — 3-step flow diagram
4. **Features** — Card grid with icons
5. **Architecture** — Network diagram
6. **Quick Start** — Copy-paste commands
7. **Testimonials** — (if any)
8. **CTA** — "Install Now" with brew command

## Suggested Documentation Sections

1. **Overview** — What is OMOTG
2. **Quick Start** — Install → configure → run in 5 minutes
3. **Installation** — Homebrew, from source, Docker
4. **Configuration** — All env vars table
5. **Usage** — Commands, sessions, topics, MCP tools
6. **Deployment** — systemd, TLS, reverse proxy
7. **Security** — Whitelist, TLS, secret token
8. **Troubleshooting** — Common issues & solutions
9. **FAQ**

---

## Branding Notes

- **Name:** OMOTG (all caps) or Omotg (camel case)
- **Tagline idea:** "Chat with AI Agents from Telegram" or "Your OpenCode, Now in Telegram"
- **Bot name:** Elizabeth (the actual deployed bot)
- **Color scheme:** Dark theme to match OpenCode/OMO ecosystem
- **Logo idea:** Telegram paper plane + OpenCode terminal icon merged

---

## Assets & Links

- **Source code:** https://github.com/itokun99/omotg (private)
- **Homebrew formula:** https://github.com/itokun99/homebrew-omotg (public)
- **OpenCode:** https://opencode.ai
- **OMO:** https://omo.dev
