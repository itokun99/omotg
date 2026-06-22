package bot

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestBotPassesAgentToMessageRequest verifies that a Bot constructed
// with a non-empty Agent field forwards that agent in the POST body.
func TestBotPassesAgentToMessageRequest(t *testing.T) {
	var lastAgentSeen string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/message") {
			raw, _ := io.ReadAll(r.Body)
			var body map[string]interface{}
			_ = json.Unmarshal(raw, &body)
			lastAgentSeen, _ = body["agent"].(string)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"parts":[{"type":"text","text":"ok"}]}`)
	}))
	defer srv.Close()

	oc := NewOCClient(srv.URL, "p")
	sessions := NewSessionMap()
	tc := NewTopicClient("dummy-token")

	cfg := &BotConfig{
		SecretToken:    "x",
		AllowedChatIDs: nil,
		SessionTimeout: 5 * time.Second,
		BotToken:       "dummy",
		Agent:          "Atlas - Plan Executor",
	}
	b := NewBot(cfg, oc, sessions, tc)

	_, _ = oc.SendMessageWithAgent(context.Background(), "ses_demo", "hello", b.agent)
	if lastAgentSeen != "Atlas - Plan Executor" {
		t.Fatalf("expected agent 'Atlas - Plan Executor', got %q", lastAgentSeen)
	}
}
