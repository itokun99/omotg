package bot

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestSendMessageWithAgentEmitsAgentField verifies the request body
// contains the "agent" key when a non-empty agent is supplied.
func TestSendMessageWithAgentEmitsAgentField(t *testing.T) {
	var gotBody map[string]interface{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"parts":[{"type":"text","text":"ok"}]}`)
	}))
	defer srv.Close()

	c := NewOCClient(srv.URL, "p")
	_, err := c.SendMessageWithAgent(context.Background(), "ses_test", "hi", "Atlas - Plan Executor")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if gotBody["agent"] != "Atlas - Plan Executor" {
		t.Fatalf("agent field missing or wrong: got %v", gotBody["agent"])
	}
}

// TestSendMessageWithoutAgentOmitsField verifies "agent" key is absent
// when empty agent string is passed (legacy behavior).
func TestSendMessageWithoutAgentOmitsField(t *testing.T) {
	var gotBody map[string]interface{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"parts":[{"type":"text","text":"ok"}]}`)
	}))
	defer srv.Close()

	c := NewOCClient(srv.URL, "p")
	_, err := c.SendMessageWithAgent(context.Background(), "ses_test", "hi", "")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if _, has := gotBody["agent"]; has {
		t.Fatalf("agent field should be absent for empty agent; body=%v", gotBody)
	}
}
