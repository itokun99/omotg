package bot

import (
	"strings"
	"testing"
)

func TestEscapeTelegramHTML(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "plain text unchanged",
			input: "hello world",
			want:  "hello world",
		},
		{
			name:  "escapes raw angle brackets",
			input: "a < b && b > c",
			want:  "a &lt; b &amp;&amp; b &gt; c",
		},
		{
			name:  "preserves pre tag",
			input: "<pre>code block</pre>",
			want:  "<pre>code block</pre>",
		},
		{
			name:  "preserves code tag",
			input: "<code>inline</code>",
			want:  "<code>inline</code>",
		},
		{
			name:  "preserves bold tag",
			input: "<b>bold</b> and <strong>strong</strong>",
			want:  "<b>bold</b> and <strong>strong</strong>",
		},
		{
			name:  "preserves italic tag",
			input: "<i>italic</i> and <em>emphasis</em>",
			want:  "<i>italic</i> and <em>emphasis</em>",
		},
		{
			name:  "preserves u and s tags",
			input: "<u>underline</u> <s>strike</s>",
			want:  "<u>underline</u> <s>strike</s>",
		},
		{
			name:  "preserves blockquote",
			input: "<blockquote>quote</blockquote>",
			want:  "<blockquote>quote</blockquote>",
		},
		{
			name:  "escapes unknown tags",
			input: "<script>alert(1)</script>",
			want:  "&lt;script&gt;alert(1)&lt;/script&gt;",
		},
		{
			name:  "mixed safe and unsafe",
			input: "<b>safe</b> <div>unsafe</div> <pre>code</pre>",
			want:  "<b>safe</b> &lt;div&gt;unsafe&lt;/div&gt; <pre>code</pre>",
		},
		{
			name:  "escapes ampersand properly",
			input: "AT&T <b>bold</b>",
			want:  "AT&amp;T <b>bold</b>",
		},
		{
			name:  "nested safe tags",
			input: "<pre><code>nested</code></pre>",
			want:  "<pre><code>nested</code></pre>",
		},
		{
			name:  "empty string",
			input: "",
			want:  "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := escapeTelegramHTML(tt.input)
			if got != tt.want {
				t.Errorf("escapeTelegramHTML(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestBuildPrompt(t *testing.T) {
	got := buildPrompt(12345, 0, "ses_abc", "cek status server", nil)
	if !strings.Contains(got, " chat_id: 12345") {
		t.Errorf("buildPrompt() should contain chat_id, got: %q", got)
	}
	if !strings.Contains(got, " thread_id: 0") {
		t.Errorf("buildPrompt() should contain thread_id, got: %q", got)
	}
	if !strings.Contains(got, " session_id: ses_abc") {
		t.Errorf("buildPrompt() should contain session_id, got: %q", got)
	}
	if !strings.Contains(got, "Here is the chat context for this message:") {
		t.Errorf("buildPrompt() should contain context header, got: %q", got)
	}
	if !strings.HasPrefix(got, "cek status server") {
		t.Errorf("buildPrompt() should start with prompt text, got: %q", got)
	}
	if !strings.HasSuffix(got, " session_id: ses_abc") {
		t.Errorf("buildPrompt() should end with session_id, got: %q", got)
	}
}

func TestBuildPromptWithThreadID(t *testing.T) {
	got := buildPrompt(67890, 42, "ses_def", "cek logs", nil)
	if !strings.Contains(got, " chat_id: 67890") {
		t.Errorf("buildPrompt() should contain chat_id, got: %q", got)
	}
	if !strings.Contains(got, " thread_id: 42") {
		t.Errorf("buildPrompt() should contain thread_id, got: %q", got)
	}
	if !strings.Contains(got, " session_id: ses_def") {
		t.Errorf("buildPrompt() should contain session_id, got: %q", got)
	}
	if !strings.HasPrefix(got, "cek logs") {
		t.Errorf("buildPrompt() should start with prompt text, got: %q", got)
	}
	if !strings.HasSuffix(got, " session_id: ses_def") {
		t.Errorf("buildPrompt() should end with session_id, got: %q", got)
	}
}
