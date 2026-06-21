package bot

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

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

// SendMessage sends a prompt message to an existing session and returns
// the extracted response text from the HTTP response body.
func (c *OCClient) SendMessage(ctx context.Context, sessionID, prompt string) (string, error) {
	body := map[string]interface{}{
		"parts": []map[string]string{
			{"type": "text", "text": prompt},
		},
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

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusAccepted {
		errBody, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("send message: status %d: %s", resp.StatusCode, strings.TrimSpace(string(errBody)))
	}

	var msgResp sendMessageResponse
	if err := json.NewDecoder(resp.Body).Decode(&msgResp); err != nil {
		return "", fmt.Errorf("decode send message response: %w", err)
	}

	return extractTextParts(&msgResp), nil
}
