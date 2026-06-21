package bot

import (
	"fmt"
	"strings"
)

// CommandType represents the type of parsed Telegram command.
type CommandType int

const (
	CmdUnknown CommandType = iota
	CmdStart
	CmdHelp
	CmdStatus
	CmdDeploy
	CmdLogs
	CmdFreeChat
	CmdSession
	CmdTopic
)

// SessionAction is the sub-action for CmdSession.
type SessionAction int

const (
	SessNone SessionAction = iota
	SessNew
	SessList
	SessSwitch
	SessDelete
)

// TopicAction is the sub-action for CmdTopic.
type TopicAction int

const (
	TopicNone TopicAction = iota
	TopicNew
	TopicClose
	TopicDelete
)

// ParsedCommand holds the result of parsing a Telegram message.
type ParsedCommand struct {
	Type         CommandType
	SessionAct   SessionAction // sub-action for CmdSession
	SessionArg   string        // session ID argument for switch/delete
	TopicAct     TopicAction
	TopicName    string // name for /topic new
	RawText      string
	Args         []string
	Prompt       string
}

// ParseMessage parses a Telegram message and returns a ParsedCommand.
func ParseMessage(text string) ParsedCommand {
	text = strings.TrimSpace(text)
	if text == "" {
		return ParsedCommand{Type: CmdUnknown, RawText: text}
	}

	if !strings.HasPrefix(text, "/") {
		return ParsedCommand{
			Type:    CmdFreeChat,
			RawText: text,
			Prompt:  text,
		}
	}

	// Strip bot mention suffix (e.g. "/session@MyBot" → "/session")
	cmdRaw := strings.Fields(text)[0]
	cmdRaw = strings.SplitN(cmdRaw, "@", 2)[0]

	parts := strings.Fields(text)
	args := parts[1:]

	switch cmdRaw {
	case "/start":
		return ParsedCommand{Type: CmdStart, RawText: text}

	case "/help":
		return ParsedCommand{Type: CmdHelp, RawText: text}

	case "/status":
		return ParsedCommand{
			Type:    CmdStatus,
			RawText: text,
			Prompt:  "check server status, cpu, memory, and disk usage",
		}

	case "/deploy":
		p := ParsedCommand{Type: CmdDeploy, RawText: text, Args: args}
		if len(args) > 0 {
			p.Prompt = fmt.Sprintf("deploy application to %s environment", args[0])
		}
		return p

	case "/logs":
		p := ParsedCommand{Type: CmdLogs, RawText: text, Args: args}
		n := "50"
		if len(args) > 0 {
			n = args[0]
		}
		p.Prompt = fmt.Sprintf("show last %s lines of server logs", n)
		return p

	case "/session", "/sesi":
		cmd := ParsedCommand{Type: CmdSession, RawText: text, Args: args}
		if len(args) == 0 {
			return cmd
		}
		switch args[0] {
		case "new", "baru":
			cmd.SessionAct = SessNew
			if len(args) > 1 {
				cmd.Prompt = strings.Join(args[1:], " ")
			}
		case "list", "daftar", "ls":
			cmd.SessionAct = SessList
		case "switch", "ganti", "use", "sw":
			cmd.SessionAct = SessSwitch
			if len(args) > 1 {
				cmd.SessionArg = args[1]
			}
		case "delete", "hapus", "rm":
			cmd.SessionAct = SessDelete
			if len(args) > 1 {
				cmd.SessionArg = args[1]
			}
		case "close", "tutup":
			cmd.SessionAct = SessNew
		default:
		}
		return cmd

	case "/topic":
		cmd := ParsedCommand{Type: CmdTopic, RawText: text, Args: args}
		if len(args) == 0 {
			return cmd
		}
		switch args[0] {
		case "new":
			if len(args) > 1 {
				cmd.TopicAct = TopicNew
				cmd.TopicName = strings.Join(args[1:], " ")
			}
		case "close", "tutup":
			cmd.TopicAct = TopicClose
		case "delete", "hapus", "rm":
			cmd.TopicAct = TopicDelete
		}
		return cmd

	default:
		return ParsedCommand{Type: CmdUnknown, RawText: text}
	}
}

// HelpText returns the formatted help message.
func HelpText() string {
	return `Available commands:

/start — Start the bot
/help — Show this help message
/status — Check server status (CPU, memory, disk)
/deploy <env> — Deploy application to <env> environment
/logs [N] — Show last N lines of server logs (default: 50)
/topic new <nama> — Create a new forum topic (group only)
/topic close — Close the current topic (group only)
/topic delete — Permanently delete the current topic (group only)

Session management (private chat only):
/session — Show current session info
/session new [text] — Create new session (optionally with first prompt)
/session list — List all sessions
/session switch <id> — Switch to a different session
/session delete <id> — Delete a session`
}
