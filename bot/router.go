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
)

// ParsedCommand holds the result of parsing a Telegram message.
type ParsedCommand struct {
	Type    CommandType
	RawText string
	Args    []string
	Prompt  string
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

	parts := strings.Fields(text)
	cmd := parts[0]
	args := parts[1:]

	switch cmd {
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
/logs [N] — Show last N lines of server logs (default: 50)`
}
