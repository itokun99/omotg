package bot

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

// SSEEvent represents a server-sent event from OpenCode's global event stream.
type SSEEvent struct {
	SessionID string `json:"session_id"`
	Type      string `json:"type"`
	Content   string `json:"content,omitempty"`
	Error     string `json:"error,omitempty"`
}

// OCClient is an HTTP client for the OpenCode Serve REST API.
type OCClient struct {
	baseURL    string
	password   string
	httpClient *http.Client
}

// NewOCClient creates a new OCClient with a 30-second HTTP timeout.
func NewOCClient(baseURL, password string) *OCClient {
	return &OCClient{
		baseURL:  strings.TrimRight(baseURL, "/"),
		password: password,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
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
		SessionID string `json:"session_id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decode create session response: %w", err)
	}
	if result.SessionID == "" {
		return "", fmt.Errorf("create session: empty session_id in response")
	}
	return result.SessionID, nil
}

// SendMessage sends a prompt message to an existing session.
func (c *OCClient) SendMessage(ctx context.Context, sessionID, prompt string) error {
	body := map[string]string{"message": prompt}
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(body); err != nil {
		return fmt.Errorf("encode send message body: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/session/"+sessionID+"/message", &buf)
	if err != nil {
		return fmt.Errorf("send message request: %w", err)
	}
	c.setAuth(req)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("send message: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusAccepted {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("send message: status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}

// SubscribeEvents opens an SSE connection to the global event stream and returns
// a channel that receives parsed events. The channel is closed when the context
// is cancelled or the connection drops.
func (c *OCClient) SubscribeEvents(ctx context.Context) (<-chan SSEEvent, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/global/event", http.NoBody)
	if err != nil {
		return nil, fmt.Errorf("subscribe events request: %w", err)
	}
	c.setAuth(req)
	req.Header.Set("Accept", "text/event-stream")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("subscribe events: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("subscribe events: unexpected status %d", resp.StatusCode)
	}

	ch := make(chan SSEEvent, 64)

	go func() {
		defer resp.Body.Close()
		defer close(ch)

		scanner := bufio.NewScanner(resp.Body)
		// Large buffer for SSE lines (up to 1MB per line, 64KB initial)
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

		var ev SSEEvent
		var dataBuf strings.Builder

		for scanner.Scan() {
			line := scanner.Text()

			switch {
			case strings.HasPrefix(line, "event: "):
				ev.Type = strings.TrimPrefix(line, "event: ")

			case strings.HasPrefix(line, "data:"):
				// Strip "data:" prefix and optional leading space
				part := line[5:]
				if len(part) > 0 && part[0] == ' ' {
					part = part[1:]
				}
				if dataBuf.Len() > 0 {
					dataBuf.WriteByte('\n')
				}
				dataBuf.WriteString(part)

			case line == "":
				// Empty line signals end of an event
				if dataBuf.Len() == 0 {
					continue
				}

				data := dataBuf.String()
				// Attempt JSON parse; if it fails, store raw in Content.
				if err := json.Unmarshal([]byte(data), &ev); err != nil {
					ev.Content = data
				}

				select {
				case ch <- ev:
				case <-ctx.Done():
					return
				}

				ev = SSEEvent{}
				dataBuf.Reset()

			default:
				// Ignore comments (lines starting with ":") and unknown fields.
			}
		}

		if err := scanner.Err(); err != nil {
			slog.Error("SSE event stream read error", "error", err)
		}
	}()

	return ch, nil
}
