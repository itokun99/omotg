package bot

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// SSEEvent is a single SSE event from OpenCode's /event stream.
// The data: line contains JSON with these fields.
type SSEEvent struct {
	ID         string          `json:"id"`
	Type       string          `json:"type"`
	Properties json.RawMessage `json:"properties"`
	Time       int64           `json:"time,omitempty"`
}

type PartUpdateProps struct {
	SessionID string          `json:"sessionID"`
	Part      json.RawMessage `json:"part"`
}

type PartData struct {
	Type  string          `json:"type"`
	Text  string          `json:"text,omitempty"`
	Tool  string          `json:"tool,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`
	State *PartState      `json:"state,omitempty"`
}

type PartState struct {
	Status string          `json:"status"`
	Input  json.RawMessage `json:"input,omitempty"`
}

var toolEmoji = map[string]string{
	"websearch":          "🔍",
	"bash":               "💻",
	"Read":               "📖",
	"Write":              "📝",
	"Edit":               "✏️",
	"Grep":               "🔎",
	"Glob":               "📂",
	"task":               "🔄",
	"browser_navigate":   "🌐",
	"browser_click":      "🖱️",
	"browser_type":       "⌨️",
	"browser_snapshot":   "📸",
	"browser_screenshot": "📷",
	"look_at":            "👁️",
	"webfetch":           "📡",
	"todowrite":          "✅",
	"todo":               "📋",
	"lsp_diagnostics":    "🔧",
	"memory":             "💾",
	"question":           "❓",
	"skill":              "🎯",
	"edit":               "✏️",
}

func EmojiForTool(toolName string) string {
	if emoji, ok := toolEmoji[toolName]; ok {
		return emoji
	}
	lower := strings.ToLower(toolName)
	switch {
	case strings.Contains(lower, "search") || strings.Contains(lower, "find"):
		return "🔍"
	case strings.Contains(lower, "write") || strings.Contains(lower, "edit") || strings.Contains(lower, "create"):
		return "📝"
	case strings.Contains(lower, "read") || strings.Contains(lower, "view"):
		return "📖"
	case strings.Contains(lower, "bash") || strings.Contains(lower, "run") || strings.Contains(lower, "exec"):
		return "💻"
	default:
		return "⚙️"
	}
}

type OCClient struct {
	baseURL    string
	password   string
	httpClient *http.Client
	sseClient  *http.Client
}

func NewOCClient(baseURL, password string) *OCClient {
	return &OCClient{
		baseURL:  strings.TrimRight(baseURL, "/"),
		password: password,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
		sseClient: &http.Client{},
	}
}

func (c *OCClient) setAuth(req *http.Request) {
	req.SetBasicAuth("opencode", c.password)
}

// CreateSession creates a new OpenCode session and returns its ID.
func (c *OCClient) CreateSession(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/session", http.NoBody)
	if err != nil {
		return "", fmt.Errorf("create session request: %w", err)
	}
	c.setAuth(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("create session: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("create session: status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var result struct {
		SessionID string `json:"id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decode create session response: %w", err)
	}
	if result.SessionID == "" {
		return "", fmt.Errorf("create session: empty session_id in response")
	}
	return result.SessionID, nil
}

// DeleteSession deletes an existing OpenCode session.
func (c *OCClient) DeleteSession(ctx context.Context, sessionID string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, c.baseURL+"/session/"+sessionID, http.NoBody)
	if err != nil {
		return fmt.Errorf("delete session request: %w", err)
	}
	c.setAuth(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("delete session: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("delete session: status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}

type sendMessageResponse struct {
	Parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"parts"`
}

func extractTextParts(resp *sendMessageResponse) string {
	var texts []string
	for _, part := range resp.Parts {
		if part.Type == "text" && part.Text != "" {
			texts = append(texts, part.Text)
		}
	}
	return strings.Join(texts, "\n")
}

// SendMessage sends a prompt message to an existing session without
// specifying an agent (server default applies). Kept for backward compat.
func (c *OCClient) SendMessage(ctx context.Context, sessionID, prompt string) (string, error) {
	return c.SendMessageWithAgent(ctx, sessionID, prompt, "")
}

// SendMessageWithAgent sends a prompt with the given agent routing field.
// If agent is empty, the field is omitted (server falls back to its default).
func (c *OCClient) SendMessageWithAgent(ctx context.Context, sessionID, prompt, agent string) (string, error) {
	body := map[string]interface{}{
		"parts": []map[string]string{
			{"type": "text", "text": prompt},
		},
	}
	if agent != "" {
		body["agent"] = agent
	}
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(body); err != nil {
		return "", fmt.Errorf("encode send message body: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/session/"+sessionID+"/message", &buf)
	if err != nil {
		return "", fmt.Errorf("send message request: %w", err)
	}
	c.setAuth(req)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.sseClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("send message: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusAccepted {
		return "", fmt.Errorf("send message: status %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}

	var msgResp sendMessageResponse
	if err := json.Unmarshal(respBody, &msgResp); err != nil {
		return "", fmt.Errorf("decode send message response: %w", err)
	}

	return extractTextParts(&msgResp), nil
}

// sseStream reads SSE events from a response body and sends them to a channel.
// The response body is closed when the stream ends or context is cancelled.
// Each event is sent as an SSEEvent struct parsed from the "data:" line JSON.
func sseStream(ctx context.Context, r io.ReadCloser) <-chan SSEEvent {
	ch := make(chan SSEEvent, 200)
	go func() {
		defer r.Close()
		defer close(ch)

		scanner := bufio.NewScanner(r)
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for scanner.Scan() {
			line := scanner.Text()
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			var ev SSEEvent
			if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &ev); err != nil {
				continue
			}
			select {
			case ch <- ev:
			case <-ctx.Done():
				return
			}
		}
	}()
	return ch
}

// ConnectEventStream connects to the global SSE event stream at /event
// and returns a channel of all server-wide events.
// The channel is closed when the context is cancelled or the connection drops.
func (c *OCClient) ConnectEventStream(ctx context.Context) (<-chan SSEEvent, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/event", nil)
	if err != nil {
		return nil, fmt.Errorf("event stream request: %w", err)
	}
	c.setAuth(req)
	req.Header.Set("Accept", "text/event-stream")

	resp, err := c.sseClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("event stream: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, fmt.Errorf("event stream: status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return sseStream(ctx, resp.Body), nil
}

// GetSessionChildren returns the IDs of child sessions spawned by the given session.
func (c *OCClient) GetSessionChildren(ctx context.Context, sessionID string) ([]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		c.baseURL+"/session/"+sessionID+"/children", nil)
	if err != nil {
		return nil, fmt.Errorf("get children request: %w", err)
	}
	c.setAuth(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("get children: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("get children: status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var result []string
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode children response: %w", err)
	}
	return result, nil
}
