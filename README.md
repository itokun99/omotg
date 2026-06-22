# OMOTG — Telegram ↔ OpenCode Bridge

OMOTG is a Go-based bidirectional bridge that connects **Telegram** to **OpenCode** via an MCP SSE server and Telegram webhook. It allows you to interact with OpenCode directly from Telegram.

## Features

- **🤖 Telegram Bot** — Send messages to OpenCode and get responses via Telegram
- **🔌 MCP SSE Server** — Exposes Telegram tools (`send_message`, `send_notification`) to any MCP client (including OpenCode itself)
- **📦 Zero Dependencies** — Pure Go standard library, no external packages
- **🔒 Secure** — Secret token verification, chat ID whitelist, TLS cert for webhook (or plain HTTP behind a reverse proxy)
- **🧵 Session Management** — Thread-safe multi-session store with per-chat and per-topic routing; sessions persist until explicitly deleted
- **💬 Group & Forum Topics** — Supports Telegram supergroup forum topics; `/topic new` creates a dedicated topic + OpenCode session
- **🤖 Bot Persona** — Reads bot name & description from Telegram for personalized welcome messages
- **📋 Systemd Integration** — Ships with systemd user service files for auto-start

## Prerequisites

| Requirement | Version | Notes |
|-------------|---------|-------|
| Go | 1.26+ | For building from source |
| OpenCode CLI | latest | For `opencode serve` backend |
| systemd | any | User-mode services (logind) |
| OpenSSL | any | Required only for standalone TLS (or use a reverse proxy) |
| Linux | any | Tested on Ubuntu |
| Homebrew | latest | Optional — for installing via `brew install omotg` |

## Installation

### Option 1: Homebrew (recommended)

```bash
brew tap itokun99/omotg
brew install omotg
```

This installs the binary to `$(brew --prefix)/bin/omotg` and the env template to `$(brew --prefix)/etc/omotg/env.template`.

### Option 2: Build from Source

```bash
git clone git@github.com:itokun99/omotg.git
cd omotg
go build -o omotg .
sudo cp omotg /usr/local/bin/  # optional, for system-wide install
```

Or install directly to your local bin:

```bash
go build -o ~/.local/bin/omotg/omotg .
```

### 2. TLS Certificate (optional)

Telegram webhook requires HTTPS. You have two options:

#### Option A: Reverse proxy (recommended for production)

Place OMOTG behind a reverse proxy (Traefik, Caddy, Nginx) with Let's Encrypt. Set
both `OMOTG_TLS_CERT_FILE=` and `OMOTG_TLS_KEY_FILE=` to empty in your env file to
start the webhook server in plain HTTP mode. The proxy handles HTTPS termination.

#### Option B: Self-signed certificate (standalone)

```bash
mkdir -p ~/.config/omotg
openssl req -x509 -nodes -days 365 -newkey rsa:2048 \
  -keyout ~/.config/omotg/webhook.key \
  -out ~/.config/omotg/webhook.crt \
  -subj "/CN=your-domain.com" \
  -addext "subjectAltName=DNS:your-domain.com"
```

Replace `your-domain.com` with your actual domain pointing to your server.

### 3. Create Environment Config

```bash
cp env.template ~/.config/omotg/env
# Then edit with your values:
#   TELEGRAM_BOT_TOKEN — from @BotFather
#   TELEGRAM_WEBHOOK_URL — https://your-domain.com/webhook (or :8443/webhook for standalone)
#   TELEGRAM_SECRET_TOKEN — any random string
#   OPENCODE_SERVER_PASSWORD — any string (opencode serve on localhost ignores auth)
#
# For reverse proxy mode, also set:
#   OMOTG_TLS_CERT_FILE=
#   OMOTG_TLS_KEY_FILE=
```

See [Configuration](#configuration) for all options.

### 4. Set Up systemd Services

Copy the systemd service files:

```bash
mkdir -p ~/.config/systemd/user
```

**opencode-serve.service** — starts OpenCode headless server:

```ini
[Unit]
Description=OpenCode Headless Server (Serve)
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
ExecStart=/path/to/opencode serve --port 4096
Restart=on-failure
RestartSec=5
NoNewPrivileges=true
PrivateTmp=true

[Install]
WantedBy=default.target
```

**omotg.service** — starts the OMOTG bridge:

```ini
[Unit]
Description=OMOTG — Telegram ↔ OpenCode bridge
After=network-online.target opencode-serve.service
Wants=network-online.target
BindsTo=opencode-serve.service

[Service]
Type=simple
ExecStart=/path/to/omotg
Restart=on-failure
RestartSec=5
EnvironmentFile=%h/.config/omotg/env
NoNewPrivileges=true
ProtectHome=read-only
ProtectSystem=full
PrivateTmp=true

[Install]
WantedBy=default.target
```

Then enable and start:

```bash
systemctl --user daemon-reload
systemctl --user enable opencode-serve omotg
systemctl --user start opencode-serve omotg
```

## Configuration

Configuration is via environment variables in `~/.config/omotg/env`:

### Required

| Variable | Description |
|----------|-------------|
| `TELEGRAM_BOT_TOKEN` | Bot token from [@BotFather](https://t.me/botfather) |
| `TELEGRAM_WEBHOOK_URL` | Public HTTPS URL for Telegram webhook (e.g. `https://your.domain/webhook` or `https://your.domain:8443/webhook`) |
| `TELEGRAM_SECRET_TOKEN` | Custom secret string to verify webhook requests |
| `OPENCODE_SERVER_PASSWORD` | Password for OpenCode API (opencode serve on localhost ignores auth, but field is required) |

### Optional

| Variable | Default | Description |
|----------|---------|-------------|
| `OPENCODE_SERVER_URL` | `http://127.0.0.1:4096` | OpenCode serve URL |
| `OMOTG_WEBHOOK_PORT` | `8443` | Webhook listen port |
| `OMOTG_MCP_PORT` | `9090` | MCP SSE server port |
| `OMOTG_ALLOWED_CHAT_IDS` | (empty = all allowed) | Comma-separated Telegram chat IDs |
| `OMOTG_SESSION_TIMEOUT` | `300` | Session timeout in seconds |
| `OMOTG_TLS_CERT_FILE` | `~/.config/omotg/webhook.crt` | TLS certificate path (empty = plain HTTP, for reverse proxy mode) |
| `OMOTG_TLS_KEY_FILE` | `~/.config/omotg/webhook.key` | TLS key path (empty = plain HTTP, for reverse proxy mode) |

## Usage

### Telegram Commands

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

Any non-command message is forwarded to OpenCode using the current session.

### Group & Forum Topics

OMOTG supports **Telegram supergroup forum topics**. Conversations are routed as follows:

- **Message in a topic** — OMOTG replies in the same topic thread, using the session bound to that topic
- **Message in General** — OMOTG replies in General using the group's default session
- **Private chat** — works as before, with persistent session

To create a dedicated workspace, use `/topic new <nama>` — this creates a new forum topic and binds a fresh OpenCode session to it. Use `/topic close` or `/topic delete` to clean up the topic and its session.

Each OpenCode session maps to one topic, keeping conversations organized in busy groups.

### MCP Tools

Available via the MCP SSE server at `http://127.0.0.1:9090/mcp/sse`:

| Tool | Description |
|------|-------------|
| `send_message` | Send a text message to a Telegram chat |
| `send_notification` | Send a notification to a Telegram chat |

### OpenCode MCP Integration

Add to your OpenCode config (`~/.config/opencode/opencode.json`):

```json
{
  "mcp": {
    "omotg": {
      "type": "remote",
      "url": "http://127.0.0.1:9090/mcp/sse"
    }
  }
}
```

## Architecture

### Standalone TLS

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

### Behind Reverse Proxy (recommended)

```
Telegram App          Server (VPS)                             OpenCode CLI
    │                      │                                        │
    │  POST /webhook       │                                        │
    │  ────────────────────→  Traefik (port 443, Let's Encrypt)     │
    │                      │    │                                   │
    │                      │    │ http://172.17.0.1:8443            │
    │                      │    ↓                                   │
    │                      │  omotg (plain HTTP)                    │
    │                      │    │                                   │
    │                      │    │ POST /session                     │
    │                      │    │ POST /session/{id}/msg            │
    │                      │    │ DELETE /session/{id}              │
    │                      │    └───────────────────────────────→ opencode serve
    │                      │                                        │ (port 4096)
    │                      │                                        │
    │  sendMessage         │                                        │
    │  ←────────────────────  omotg (sync response)                 │
    │                      │    │                                   │
    │                      │    │ MCP SSE endpoint                  │
    │                      │    │ http://127.0.0.1:9090             │
    │                      │    │                                   │
    │                      │    │ ←── MCP tools ────────────── OpenCode
```

### Ports

| Port | Service | Bind | Protocol |
|------|---------|------|----------|
| 8443 | OMOTG Webhook | `0.0.0.0` | HTTPS (TLS) or plain HTTP behind proxy |
| 9090 | OMOTG MCP SSE | `127.0.0.1` | HTTP/SSE |
| 4096 | OpenCode Serve | `127.0.0.1` | HTTP/REST |

## Uninstall

### Via Homebrew

```bash
brew uninstall omotg
brew untap itokun99/omotg
```

Then proceed to steps 1–3 below to clean up services & config.

### Manual

### 1. Stop & Disable Services

```bash
systemctl --user stop omotg opencode-serve
systemctl --user disable omotg opencode-serve
```

### 2. Remove Service Files

```bash
rm ~/.config/systemd/user/omotg.service
rm ~/.config/systemd/user/opencode-serve.service
systemctl --user daemon-reload
```

### 3. Unregister Telegram Webhook

```bash
curl -X POST "https://api.telegram.org/bot<YOUR_TOKEN>/deleteWebhook"
```

### 4. Remove Binary & Config

```bash
# If installed via Homebrew:
rm -rf ~/.config/omotg

# If installed from source:
rm -rf ~/.local/bin/omotg
rm -rf ~/.config/omotg
```

### 5. Remove OpenCode MCP Config (optional)

Edit `~/.config/opencode/opencode.json` and remove the `omotg` entry from the `mcp` section.

## Troubleshooting

### Webhook: "Connection refused" in getWebhookInfo

This is normal if OMOTG was stopped. Start the service:

```bash
systemctl --user start omotg
```

### "OpenCode server is not available"

OpenCode serve is not running:

```bash
systemctl --user status opencode-serve
systemctl --user start opencode-serve
```

### Checking Logs

```bash
# OMOTG logs
journalctl --user -u omotg -f

# OpenCode serve logs
journalctl --user -u opencode-serve -f
```

## Security Notes

- For **standalone mode**, the webhook server uses a self-signed certificate. For production, use a reverse proxy (Traefik/Caddy/Nginx) with Let's Encrypt and set `OMOTG_TLS_CERT_FILE=` and `OMOTG_TLS_KEY_FILE=` to empty to disable internal TLS.
- In **reverse proxy mode**, OMOTG binds to `0.0.0.0` with plain HTTP. Ensure your proxy is the only publicly reachable endpoint and that the proxy port is firewalled.
- The MCP server binds to `127.0.0.1` only — not accessible from the network.
- Chat ID whitelist is **highly recommended**. Without it, anyone who finds your bot can interact with OpenCode.
- The secret token verifies that webhook requests are genuinely from Telegram.
- OpenCode serve on localhost does not enforce authentication; keep it bound to 127.0.0.1.

## License

**CC BY-NC 4.0 — Attribution-NonCommercial 4.0 International**

Copyright (c) 2026 Indrawan Lisanto

You are free to share and adapt this software for **non-commercial purposes only**, provided you give appropriate credit. Commercial use is **strictly prohibited** without explicit permission.

See [LICENSE](LICENSE) for full terms.
