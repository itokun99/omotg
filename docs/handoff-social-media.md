# Handoff — Social Media Promotion for OMOTG

## Product

**OMOTG** — Telegram ↔ OpenCode Bridge.
A Go-based bidirectional bridge that lets you talk to OpenCode AI agent directly from Telegram. Both private chat and group forum topics supported.

| Item | Link |
|------|------|
| GitHub (private) | https://github.com/itokun99/omotg |
| Homebrew tap (public) | https://github.com/itokun99/homebrew-omotg |
| Install | `brew tap itokun99/omotg && brew install omotg` |
| License | CC BY-NC 4.0 (non-commercial, attribution required) |
| Tech | Go 1.26, stdlib only, OpenCode HTTP API, Telegram Bot API |

---

## Positioning & Core Narrative

**One-liner:**
> Chat with AI coding agents directly from Telegram.

**Elevator pitch:**
OMOTG bridges Telegram and OpenCode — the open-source AI coding agent. Run OpenCode on your server, then message it from your phone via Telegram. Create sessions, switch between them, use forum topics for team collaboration. It's pure Go, zero external dependencies, and installs via Homebrew.

**Why this exists:**
OpenCode runs in the terminal. You can't always be at your desk. OMOTG lets you interact with it from anywhere — your phone, your tablet, someone else's computer — through Telegram.

**Taglines:**
- "Your OpenCode, wherever you are."
- "AI coding from your pocket."
- "Telegram meets OpenCode."

---

## Target Audience

| Segment | Why They Care |
|---------|---------------|
| **OpenCode users** | Already use OpenCode daily. OMOTG gives them mobile access. |
| **AI coding agent enthusiasts** | Love the idea of running agents remotely. |
| **Go developers** | Appreciate stdlib-only, zero-dependency architecture. |
| **Self-hosting community** | systemd integration, TLS, full control. |
| **Technical founders / indie devs** | Check on long-running agent tasks from phone. |

---

## Platform Strategy

### LinkedIn

**Format:** Long-form post (800-1200 chars), technical but approachable.

**Angle:** "I built a Go bridge between Telegram and my AI coding agent so I can code from my phone."

**Structure:**
1. Hook — relatable problem ("Ever wished you could check on your AI coding agent from your phone?")
2. What OMOTG is — screenshot or architecture GIF
3. Why Go + stdlib-only — reliability flex
4. Quick demo — install commands
5. CTA — try it, star the repo, contribute (under guidelines)

**Best time:** Weekday mornings (Tue-Thu, 8-10am user's timezone).

### Twitter / X

**Format:** Short thread or single post.

**Structure:**
1. One-liner + screenshot
2. "Built with Go stdlib, no deps" flex
3. Install command
4. Link to repo

**Hashtags:** `#OpenCode` `#GoLang` `#Telegram` `#AIAgent` `#DevTools`

### Reddit

**Relevant subreddits:** r/golang, r/selfhosted, r/opensource

**Format:** Casual, conversational. "I built X, here's what it does."

**Angle:** Focus on the technical side for r/golang (stdlib-only, architecture). Focus on the use case for r/selfhosted (systemd, TLS, full control).

**Avoid:** Over-promotion. Reddit hates marketing. Post as a maker sharing their project.

### Dev.to

**Format:** Tutorial-style blog post.

**Title idea:** "Run OpenCode on Your Server and Chat With It From Telegram"

**Content:** Full walkthrough — install → configure → deploy → use. Include the architecture diagram from README.

---

## Key Features to Highlight

1. **Mobile access to OpenCode** — message your AI agent from anywhere
2. **Multi-session** — create, switch, delete sessions from Telegram
3. **Forum topic support** — `/topic new` for dedicated workspaces in group chats
4. **Zero external dependencies** — pure Go stdlib, nothing to npm/pip install
5. **Homebrew install** — `brew tap && brew install`, done
6. **systemd integration** — production-ready with auto-start and graceful shutdown
7. **MCP SSE server** — exposes Telegram tools (`send_message`, `send_notification`) to any MCP client

---

## Visual Ideas

| Asset | Description |
|-------|-------------|
| **Screenshot 1** | Telegram chat with OMOTG — showing `/session list`, responses from OpenCode |
| **Screenshot 2** | Architecture diagram (from README) — Telegram → OMOTG → OpenCode |
| **Screenshot 3** | Terminal showing `brew install omotg` |
| **GIF** | Screen recording: Telegram message → OMOTG processes → response appears |
| **Logo** | Merged Telegram paper plane + terminal icon (optional, nice to have) |

---

## Messaging by Platform

### LinkedIn Example Hook
> "I spend all day in OpenCode. But I can't always be at my desk.
> So I built OMOTG — a Go bridge that connects Telegram to OpenCode.
> Now I can message my AI coding agent from anywhere."

### Twitter Example
> "Chat with OpenCode from Telegram.
> OMOTG bridges Telegram ↔ your AI coding agent.
> Go, stdlib-only, Homebrew install.
> 🔗 github.com/itokun99/homebrew-omotg"

### Reddit (r/golang) Example
> "Built OMOTG — a Telegram ↔ OpenCode bridge in pure Go, zero dependencies. Uses stdlib net/http, no frameworks. Would love feedback on the architecture."

---

## Hashtag Strategy

| Platform | Hashtags |
|----------|----------|
| LinkedIn | `#OpenCode #GoLang #AIAgent #DevTools #OpenSource #Telegram #SelfHosted` |
| Twitter/X | `#OpenCode #GoLang #Telegram #AIAgent` (2-3 max) |
| Dev.to | `#golang #opencode #telegram #selfhosted` |

---

## Tone & Voice

- **Authentic, not hype.** No "revolutionary" or "game-changing." It's a practical tool.
- **Technical but accessible.** Show the commands, explain the architecture, skip the buzzwords.
- **Humble.** Maker sharing a thing they built. Open to feedback.
- **Consistent with the repo's vibe.** The project is straightforward and no-nonsense. The social posts should match.

---

## CTAs

- "Install it: `brew tap itokun99/omotg && brew install omotg`"
- "Star the repo on GitHub"
- "Contributions welcome — see CONTRIBUTING.md"
- "Questions? Open an issue or DM me"

---

## Notes & Constraints

- **Repo is private** — direct source access by invitation only. Homebrew tap is public.
- **License is CC BY-NC 4.0** — non-commercial. Mention for transparency.
- **Contributions welcome but no profile farming** — see CONTRIBUTING.md for details.
- **No paid promotion** — organic sharing only.
- **Bot name is "Elizabeth"** — optional easter egg for posts.
