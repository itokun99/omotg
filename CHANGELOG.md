# Changelog

All notable changes to OMOTG are documented here.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- ...

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
