package bot

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// TopicClient handles Telegram API calls for forum topic management.
type TopicClient struct {
	botToken   string
	httpClient *http.Client
}

// NewTopicClient creates a TopicClient.
func NewTopicClient(botToken string) *TopicClient {
	return &TopicClient{
		botToken:   botToken,
		httpClient: &http.Client{Timeout: 10 * time.Second},
	}
}

// CreateForumTopic creates a new forum topic in a group and returns the topic ID.
// API: POST /bot{token}/createForumTopic
func (tc *TopicClient) CreateForumTopic(chatID int64, name string) (int64, error) {
	payload := map[string]interface{}{
		"chat_id": chatID,
		"name":    name,
	}
	body, _ := json.Marshal(payload)

	url := fmt.Sprintf("https://api.telegram.org/bot%s/createForumTopic", tc.botToken)
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return 0, fmt.Errorf("create forum topic request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := tc.httpClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("create forum topic: %w", err)
	}
	defer resp.Body.Close()

	var result struct {
		Ok          bool   `json:"ok"`
		Description string `json:"description,omitempty"`
		Result      *struct {
			MessageThreadID int64 `json:"message_thread_id"`
		} `json:"result,omitempty"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return 0, fmt.Errorf("decode create forum topic response: %w", err)
	}
	if !result.Ok {
		return 0, fmt.Errorf("create forum topic failed: %s", result.Description)
	}
	if result.Result == nil {
		return 0, fmt.Errorf("create forum topic: empty result")
	}
	return result.Result.MessageThreadID, nil
}
