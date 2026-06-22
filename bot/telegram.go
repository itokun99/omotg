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

// CloseForumTopic closes a forum topic. Messages are preserved but new messages cannot be sent.
// API: POST /bot{token}/closeForumTopic
func (tc *TopicClient) CloseForumTopic(chatID, threadID int64) error {
	payload := map[string]interface{}{
		"chat_id":           chatID,
		"message_thread_id": threadID,
	}
	body, _ := json.Marshal(payload)
	url := fmt.Sprintf("https://api.telegram.org/bot%s/closeForumTopic", tc.botToken)
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("close forum topic request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := tc.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("close forum topic: %w", err)
	}
	defer resp.Body.Close()

	var result struct {
		Ok          bool   `json:"ok"`
		Description string `json:"description,omitempty"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return fmt.Errorf("decode close forum topic response: %w", err)
	}
	if !result.Ok {
		return fmt.Errorf("close forum topic failed: %s", result.Description)
	}
	return nil
}

// DeleteForumTopic deletes a forum topic along with all its messages. Irreversible.
// API: POST /bot{token}/deleteForumTopic
func (tc *TopicClient) DeleteForumTopic(chatID, threadID int64) error {
	payload := map[string]interface{}{
		"chat_id":           chatID,
		"message_thread_id": threadID,
	}
	body, _ := json.Marshal(payload)
	url := fmt.Sprintf("https://api.telegram.org/bot%s/deleteForumTopic", tc.botToken)
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("delete forum topic request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := tc.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("delete forum topic: %w", err)
	}
	defer resp.Body.Close()

	var result struct {
		Ok          bool   `json:"ok"`
		Description string `json:"description,omitempty"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return fmt.Errorf("decode delete forum topic response: %w", err)
	}
	if !result.Ok {
		return fmt.Errorf("delete forum topic failed: %s", result.Description)
	}
	return nil
}

// BotPersona holds information about the bot for personalizing messages.
type BotPersona struct {
	ID          int64  // Telegram bot user ID
	FirstName   string
	Username    string // @username (without @)
	Description string
}

// GetBotPersona fetches the bot's name and description from Telegram API.
func (tc *TopicClient) GetBotPersona() (*BotPersona, error) {
	meURL := fmt.Sprintf("https://api.telegram.org/bot%s/getMe", tc.botToken)
	resp, err := tc.httpClient.Get(meURL)
	if err != nil {
		return nil, fmt.Errorf("getMe: %w", err)
	}
	defer resp.Body.Close()

	var meResult struct {
		Ok     bool `json:"ok"`
		Result *struct {
			ID        int64  `json:"id"`
			FirstName string `json:"first_name"`
			Username  string `json:"username"`
		} `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&meResult); err != nil {
		return nil, fmt.Errorf("decode getMe response: %w", err)
	}
	if !meResult.Ok || meResult.Result == nil {
		return nil, fmt.Errorf("getMe: empty result")
	}

	botID := meResult.Result.ID
	botFirstName := meResult.Result.FirstName
	botUsername := meResult.Result.Username

	descURL := fmt.Sprintf("https://api.telegram.org/bot%s/getMyDescription", tc.botToken)
	resp2, err := tc.httpClient.Get(descURL)
	if err != nil {
		return &BotPersona{ID: botID, FirstName: botFirstName, Username: botUsername}, nil
	}
	defer resp2.Body.Close()

	var descResult struct {
		Ok     bool `json:"ok"`
		Result *struct {
			Description string `json:"description"`
		} `json:"result"`
	}
	if err := json.NewDecoder(resp2.Body).Decode(&descResult); err != nil || !descResult.Ok || descResult.Result == nil {
		return &BotPersona{ID: botID, FirstName: botFirstName, Username: botUsername}, nil
	}

	return &BotPersona{
		ID:          botID,
		FirstName:   botFirstName,
		Username:    botUsername,
		Description: descResult.Result.Description,
	}, nil
}

// WelcomeMessage returns a personalized welcome message for a new topic/session.
func (p *BotPersona) WelcomeMessage(topicName string) string {
	if p.Description != "" {
		return fmt.Sprintf("📂 Topic: **%s**\n💬 %s\n\nKirim pesan untuk mulai ngobrol.", topicName, p.Description)
	}
	if p.FirstName != "" {
		return fmt.Sprintf("📂 Topic: **%s**\n🤖 Halo! Saya **%s**. Kirim pesan untuk mulai ngobrol.", topicName, p.FirstName)
	}
	return fmt.Sprintf("📂 Topic: **%s**\nKirim pesan untuk mulai ngobrol dengan OpenCode.", topicName)
}
