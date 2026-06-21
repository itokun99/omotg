package bot

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"time"
)

// SessionEntry holds metadata about an active session.
type SessionEntry struct {
	SessionID string
	ChatID    int64
	ThreadID  int64 // Telegram forum topic ID; 0 means no topic (private chat or non-forum group)
	CreatedAt time.Time
	LastUsed  time.Time
}

// SessionMap provides a thread-safe mapping for multiple sessions per chat
// with topic-based routing for group conversations.
type SessionMap struct {
	mu          sync.RWMutex
	entries     map[string]*SessionEntry // sessionID → entry
	chatCurrent map[int64]string         // chatID → current sessionID (private chat)
	topicBind   map[string]string        // "chatID:threadID" → sessionID (group topics)
}

// NewSessionMap creates a new empty SessionMap.
func NewSessionMap() *SessionMap {
	return &SessionMap{
		entries:     make(map[string]*SessionEntry),
		chatCurrent: make(map[int64]string),
		topicBind:   make(map[string]string),
	}
}

// Store adds or updates a session entry.
// For private chat (threadID == 0), this also sets the session as "current".
// For group topics (threadID > 0), this binds the session to the topic.
// The timeout parameter is kept for backward compatibility but ignored
// — sessions persist until explicitly deleted.
func (sm *SessionMap) Store(sessionID string, chatID int64, threadID int64, _ time.Duration) {
	now := time.Now()

	sm.mu.Lock()
	if existing, ok := sm.entries[sessionID]; ok {
		existing.ChatID = chatID
		existing.ThreadID = threadID
		existing.LastUsed = now
	} else {
		sm.entries[sessionID] = &SessionEntry{
			SessionID: sessionID,
			ChatID:    chatID,
			ThreadID:  threadID,
			CreatedAt: now,
			LastUsed:  now,
		}
	}

	if threadID == 0 {
		// Private chat: set as current session
		sm.chatCurrent[chatID] = sessionID
	} else {
		// Group topic: bind session to topic
		key := topicKey(chatID, threadID)
		sm.topicBind[key] = sessionID
	}
	sm.mu.Unlock()

	slog.Debug("session stored",
		"session_id", sessionID,
		"chat_id", chatID,
		"thread_id", threadID,
	)
}

// Load retrieves a session entry by ID. Returns false if not found.
func (sm *SessionMap) Load(sessionID string) (*SessionEntry, bool) {
	sm.mu.RLock()
	entry, ok := sm.entries[sessionID]
	sm.mu.RUnlock()
	if !ok {
		return nil, false
	}
	return entry, true
}

// Delete removes a session and cleans up all references to it.
func (sm *SessionMap) Delete(sessionID string) {
	sm.mu.Lock()
	entry, ok := sm.entries[sessionID]
	if ok {
		delete(sm.entries, sessionID)
		// Clean up chatCurrent reference
		if sm.chatCurrent[entry.ChatID] == sessionID {
			delete(sm.chatCurrent, entry.ChatID)
		}
		// Clean up topicBind reference
		key := topicKey(entry.ChatID, entry.ThreadID)
		if sm.topicBind[key] == sessionID {
			delete(sm.topicBind, key)
		}
	}
	sm.mu.Unlock()

	if ok {
		slog.Debug("session deleted", "session_id", sessionID, "chat_id", entry.ChatID)
	}
}

// GetCurrentSession returns the current session for a private chat, or nil.
func (sm *SessionMap) GetCurrentSession(chatID int64) *SessionEntry {
	sm.mu.RLock()
	sessionID, ok := sm.chatCurrent[chatID]
	if !ok {
		sm.mu.RUnlock()
		return nil
	}
	entry, ok := sm.entries[sessionID]
	sm.mu.RUnlock()
	if !ok {
		return nil
	}
	return entry
}

// SetCurrentSession sets the current session for a private chat.
func (sm *SessionMap) SetCurrentSession(chatID int64, sessionID string) {
	sm.mu.Lock()
	sm.chatCurrent[chatID] = sessionID
	sm.mu.Unlock()
}

// GetTopicSession returns the session bound to a group topic, or nil.
func (sm *SessionMap) GetTopicSession(chatID, threadID int64) *SessionEntry {
	key := topicKey(chatID, threadID)
	sm.mu.RLock()
	sessionID, ok := sm.topicBind[key]
	if !ok {
		sm.mu.RUnlock()
		return nil
	}
	entry, ok := sm.entries[sessionID]
	sm.mu.RUnlock()
	if !ok {
		return nil
	}
	return entry
}

// DeleteTopicBinding removes the session binding for a group topic.
// The session entry itself is preserved but disassociated from the topic.
func (sm *SessionMap) DeleteTopicBinding(chatID, threadID int64) {
	key := topicKey(chatID, threadID)
	sm.mu.Lock()
	delete(sm.topicBind, key)
	sm.mu.Unlock()
}

// ListChatSessions returns all sessions for a chat, newest last-used first.
func (sm *SessionMap) ListChatSessions(chatID int64) []*SessionEntry {
	sm.mu.RLock()
	var result []*SessionEntry
	for _, entry := range sm.entries {
		if entry.ChatID == chatID {
			result = append(result, entry)
		}
	}
	sm.mu.RUnlock()

	sort.Slice(result, func(i, j int) bool {
		return result[i].LastUsed.After(result[j].LastUsed)
	})
	return result
}

// Renew refreshes the last-used timestamp on a session.
func (sm *SessionMap) Renew(sessionID string) {
	sm.mu.Lock()
	if entry, ok := sm.entries[sessionID]; ok {
		entry.LastUsed = time.Now()
	}
	sm.mu.Unlock()
}

// StartCleanup is a no-op in the persistent design.
// Sessions persist until explicitly deleted — OpenCode server handles
// its own session timeouts internally.
func (sm *SessionMap) StartCleanup(_ context.Context, _ time.Duration) {}

// CleanupExpired is a no-op in the persistent design.
func (sm *SessionMap) CleanupExpired() {}

// topicKey builds a map key from chatID and threadID.
func topicKey(chatID, threadID int64) string {
	return fmt.Sprintf("%d:%d", chatID, threadID)
}

// StoreIfNotExists is deprecated. Use Store instead.
func (sm *SessionMap) StoreIfNotExists(sessionID string, chatID int64, threadID int64, timeout time.Duration) bool {
	sm.Store(sessionID, chatID, threadID, timeout)
	return true
}
