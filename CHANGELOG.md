# Changelog

All notable changes to OMOTG are documented here.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [v0.6.0] — 2026-06-24

### Added

- **Session persistence across restarts** — `SessionMap` now persists
  `chatCurrent` and `topicBind` mappings to `~/.config/omotg/sessions_<bot>.json`.
  Chat and topic session bindings survive server restarts, so conversations
  continue with the same OpenCode session (and its full history) instead of
  creating a new blank session.
- **Dynamic child timeout** — child agent timeout timer resets on every SSE
  event, so long-running sub-agents are only interrupted when truly idle (60s),
  not when they are actively producing output.
- **Completion reason logging** — every exit path from `processMessage` logs
  a structured `reason` field (`completed`, `session_timeout`, `child_timeout`,
  `send_message_error`, `sse_unavailable`) for observability.

### Fixed

- **Agent completion reports missing** — 5 root causes fixed where agent output
  never reached Telegram: SSE stream death + slow POST response now retries and
  notifies user; successful responses append completion marker; child and session
  timeouts send explicit Telegram messages instead of silently returning;
  `handleMsgResult` now correctly sets `*mainTextSent`.
- **MCP endpoint URL** — `opencode.json` URL corrected from `/mcp` (404) to
  `/mcp/sse` (200), enabling agents to call `send_telegram_message` and
  `send_telegram_notification` tools.
- **Rate limiting** — `sendTelegram` enforces 500ms minimum gap between sends
  to avoid Telegram "Too Many Requests" errors from burst tool notifications.

## [v0.5.1] — 2026-06-23

### Fixed

- **Module path** — corrected from `omotg` to `github.com/itokun99/omotg`,
  enabling `go install github.com/itokun99/omotg@latest` for global install
- **Hephaestus agent mapping** — restored `"hephaestus": "Hephaestus - Deep Agent"`
  after temporary workaround (`"general"`), ensuring Ymir bot routes to the
  correct OMO agent (gpt-5.5) instead of general-purpose model

### Documentation

- README: add `go install` option alongside Homebrew
- Clean up stale handoff/planning docs

## [v0.5.0] — 2026-06-23

### Added

- **Multi-bot multi-agent** — one binary runs N Telegram bots, each routing
  to a specific OMO agent (Sisyphus, Prometheus, Atlas, Hephaestus)
- `SendMessageWithAgent()` — `agent` field injection in OpenCode message API,
  enabling per-bot agent routing via config
- `BotAgentConfig` — structured config for auxiliary bot-agent pairs
- N-bot startup loop — `main.go` iterates `cfg.Bots`, wires per-bot
  `SessionMap`/`TopicClient`, registers multi-path webhook mux
- `OMO_*_BOT_TOKEN` env vars — `OMO_PROMETHEUS_BOT_TOKEN`,
  `OMO_ATLAS_BOT_TOKEN`, `OMO_HEPHAESTUS_BOT_TOKEN` (auxiliary bots)
- Per-bot webhook paths — `/webhook` (primary), `/webhook/<key>` (auxiliaries)

### Fixed

- Event loop 30s hang on sub-agent SSE death — `ctx.Done()` added to inner
  select; `msgCh` nilled after child session receive; `mainTextSent` flag
  correctly set after main text send

## [v0.4.0] — 2026-06-22

### Added

- Plain HTTP webhook mode — when `OMOTG_TLS_CERT_FILE` and `OMOTG_TLS_KEY_FILE` are
  both empty, the webhook server starts without TLS, for use behind a reverse proxy
  (Traefik, Caddy, Nginx) that terminates HTTPS.
- `envLookup()` config helper — uses `os.LookupEnv` so empty env values correctly
  override defaults, unlike `envOrDefault()` which treats empty as "not set".
- `env.template` now documents `OMOTG_TLS_CERT_FILE` and `OMOTG_TLS_KEY_FILE` with
  instructions for reverse proxy mode.

### Changed

- Webhook server startup is now conditional: `ListenAndServeTLS()` with TLS files
  present, `ListenAndServe()` without. Log line differs per mode.
- README tables and architecture diagram now reflect both TLS and plain-HTTP modes.

## [v0.3.0] — 2026-06-21

### Added

- Telegram-friendly response metadata — OMOTG messages include bot name, chat info
- Typing indicator while waiting for OpenCode response
- Verbose SSE tool descriptions in MCP server
- `[OMOTG]` marker and HTML parse mode in Telegram responses
- Social media promotion handoff document

### Changed

- README and CONTRIBUTING.md translated to English
- AGENTS.md hierarchy and contributing guidelines added

## [v0.2.0] — 2026-06-21

### Added

- BotPersona — fetches bot name and description from Telegram for personalized
  welcome messages
- Group forum topic support — messages routed per-topic with `/topic new`,
  `/topic close`, `/topic delete`
- Multi-session support — one-to-many sessions per chat; sessions persist until
  explicitly deleted

### Fixed

- Skip echoed user message from OpenCode SSE text events (prevents duplicate
  bot responses)

## [v0.1.0] — 2026-06-21

### Added

- Initial OMOTG release
- Telegram webhook server with TLS and self-signed certificate
- OpenCode HTTP client — create sessions, send messages, read responses
- MCP SSE server exposing `send_message` and `send_notification` tools
- Thread-safe session map with per-chat routing
- Chat ID whitelist for access control
- Secret token verification for webhook security
- Systemd user service files (`omotg.service`, `opencode-serve.service`)
- Homebrew install via `itokun99/omotg` tap
- CC BY-NC 4.0 license
