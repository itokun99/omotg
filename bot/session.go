package bot

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

// SessionInfo holds metadata about an active session.
type SessionInfo struct {
	ChatID    int64
	CreatedAt time.Time
	ExpiresAt time.Time
}

// SessionMap provides a thread-safe mapping between session IDs and chat IDs
// with automatic expiration support.
type SessionMap struct {
	mu   sync.RWMutex
	data map[string]SessionInfo
}

// NewSessionMap creates a new empty SessionMap.
func NewSessionMap() *SessionMap {
	return &SessionMap{
		data: make(map[string]SessionInfo),
	}
}

// Store associates a session ID with a chat ID for the given duration.
func (sm *SessionMap) Store(sessionID string, chatID int64, timeout time.Duration) {
	now := time.Now()
	info := SessionInfo{
		ChatID:    chatID,
		CreatedAt: now,
		ExpiresAt: now.Add(timeout),
	}

	sm.mu.Lock()
	sm.data[sessionID] = info
	sm.mu.Unlock()

	slog.Debug("session stored",
		"session_id", sessionID,
		"chat_id", chatID,
		"expires_at", info.ExpiresAt,
	)
}

// Load retrieves session info by session ID. Returns false if the session
// does not exist or has expired.
func (sm *SessionMap) Load(sessionID string) (SessionInfo, bool) {
	sm.mu.RLock()
	info, ok := sm.data[sessionID]
	sm.mu.RUnlock()

	if !ok {
		return SessionInfo{}, false
	}

	if time.Now().After(info.ExpiresAt) {
		slog.Debug("session expired on load", "session_id", sessionID)
		return SessionInfo{}, false
	}

	return info, true
}

// StoreIfNotExists atomically stores a session only if no active session exists for the chat.
// Returns true if the session was stored, false if the chat already has an active session.
//
// Deprecated: Use Store instead. OpenCode supports multiple concurrent sessions
// per chat, so the 1:1 constraint has been removed.
func (sm *SessionMap) StoreIfNotExists(sessionID string, chatID int64, timeout time.Duration) bool {
	now := time.Now()
	info := SessionInfo{
		ChatID:    chatID,
		CreatedAt: now,
		ExpiresAt: now.Add(timeout),
	}

	sm.mu.Lock()
	sm.data[sessionID] = info
	sm.mu.Unlock()

	slog.Debug("session stored (if not exists — always succeeds)",
		"session_id", sessionID,
		"chat_id", chatID,
		"expires_at", info.ExpiresAt,
	)
	return true
}

// Delete removes a session from the map.
func (sm *SessionMap) Delete(sessionID string) {
	sm.mu.Lock()
	info, ok := sm.data[sessionID]
	if ok {
		delete(sm.data, sessionID)
	}
	sm.mu.Unlock()

	if ok {
		slog.Debug("session deleted", "session_id", sessionID, "chat_id", info.ChatID)
	}
}

// StartCleanup spawns a background goroutine that periodically removes expired sessions.
// The goroutine exits when ctx is cancelled.
func (sm *SessionMap) StartCleanup(ctx context.Context, interval time.Duration) {
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				sm.CleanupExpired()
			case <-ctx.Done():
				return
			}
		}
	}()
}

// CleanupExpired iterates over all sessions and removes any that have expired.
func (sm *SessionMap) CleanupExpired() {
	now := time.Now()

	sm.mu.Lock()
	for sessionID, info := range sm.data {
		if now.After(info.ExpiresAt) {
			delete(sm.data, sessionID)
			slog.Debug("expired session cleaned up",
				"session_id", sessionID,
				"chat_id", info.ChatID,
			)
		}
	}
	sm.mu.Unlock()
}
