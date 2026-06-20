package bot

import (
	"strings"
	"testing"
)

func TestParseMessage(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		wantType   CommandType
		wantArgs   []string
		wantPrompt string
		checkFunc  func(t *testing.T, p ParsedCommand)
	}{
		{
			name:     "start command",
			input:    "/start",
			wantType: CmdStart,
		},
		{
			name:     "help command",
			input:    "/help",
			wantType: CmdHelp,
		},
		{
			name:     "status command",
			input:    "/status",
			wantType: CmdStatus,
			checkFunc: func(t *testing.T, p ParsedCommand) {
				if p.Prompt == "" {
					t.Error("ParseMessage(\"/status\").Prompt should be non-empty")
				}
			},
		},
		{
			name:       "deploy with args",
			input:      "/deploy staging",
			wantType:   CmdDeploy,
			wantArgs:   []string{"staging"},
			wantPrompt: "deploy application to staging environment",
		},
		{
			name:     "deploy without args",
			input:    "/deploy",
			wantType: CmdDeploy,
		},
		{
			name:     "logs with count",
			input:    "/logs 100",
			wantType: CmdLogs,
			wantArgs: []string{"100"},
		},
		{
			name:     "logs default",
			input:    "/logs",
			wantType: CmdLogs,
			checkFunc: func(t *testing.T, p ParsedCommand) {
				if !strings.Contains(p.Prompt, "50") {
					t.Errorf("ParseMessage(\"/logs\").Prompt = %q, should contain \"50\"", p.Prompt)
				}
			},
		},
		{
			name:     "unknown command",
			input:    "/foobar",
			wantType: CmdUnknown,
		},
		{
			name:       "free text",
			input:      "hello world",
			wantType:   CmdFreeChat,
			wantPrompt: "hello world",
		},
		{
			name:     "empty string",
			input:    "",
			wantType: CmdUnknown,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ParseMessage(tt.input)

			if got.Type != tt.wantType {
				t.Errorf("ParseMessage(%q).Type = %v, want %v", tt.input, got.Type, tt.wantType)
			}

			if tt.wantArgs != nil {
				if len(got.Args) != len(tt.wantArgs) {
					t.Errorf("ParseMessage(%q).Args = %v, want %v", tt.input, got.Args, tt.wantArgs)
				} else {
					for i := range tt.wantArgs {
						if got.Args[i] != tt.wantArgs[i] {
							t.Errorf("ParseMessage(%q).Args[%d] = %q, want %q",
								tt.input, i, got.Args[i], tt.wantArgs[i])
						}
					}
				}
			}

			if tt.wantPrompt != "" && got.Prompt != tt.wantPrompt {
				t.Errorf("ParseMessage(%q).Prompt = %q, want %q",
					tt.input, got.Prompt, tt.wantPrompt)
			}

			if tt.checkFunc != nil {
				tt.checkFunc(t, got)
			}
		})
	}
}

func TestHelpText(t *testing.T) {
	h := HelpText()
	if len(h) <= 50 {
		t.Errorf("HelpText() length = %d, want > 50", len(h))
	}
}
