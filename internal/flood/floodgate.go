// Package flood provides anti-spam flood prevention for chat applications.
package flood

import (
	"sync"
	"time"
)

const (
	// windowDuration is the fixed time window for flood detection (always 1 minute)
	windowDuration = 60 * time.Second
	// cleanupInterval is how often we clean up expired entries
	cleanupInterval = 10 * time.Minute
	// idleTimeout is how long before we remove idle user entries
	idleTimeout = 10 * time.Minute
)

// Floodgate provides per-user, per-chat flood prevention with sliding window rate limiting
type Floodgate struct {
	limitPerMinute int                   // Maximum messages per user per minute
	entries        map[string]*userEntry // Key: "chatID:userID"
	mutex          sync.RWMutex
	stopCleanup    chan struct{}
}

// userEntry tracks message timestamps for a specific user in a specific chat
type userEntry struct {
	timestamps []time.Time // Sliding window of message timestamps
	lastSeen   time.Time   // When this user was last seen (for cleanup)
}

// New creates a new Floodgate with the specified rate limiting configuration
// The time window is fixed at 60 seconds (1 minute)
func New(limitPerMinute int) *Floodgate {
	fg := &Floodgate{
		limitPerMinute: limitPerMinute,
		entries:        make(map[string]*userEntry),
		stopCleanup:    make(chan struct{}),
	}

	// Start background cleanup goroutine
	go fg.cleanup()

	return fg
}

// Stop stops the background cleanup goroutine
func (fg *Floodgate) Stop() {
	close(fg.stopCleanup)
}

// CheckMessage checks if a message from the specified user in the specified chat should be allowed
// Returns true if the message should be processed, false if it should be blocked due to flood
func (fg *Floodgate) CheckMessage(chatID, userID string) bool {
	key := chatID + ":" + userID
	now := time.Now()

	fg.mutex.Lock()
	defer fg.mutex.Unlock()

	// Get or create user entry
	entry, exists := fg.entries[key]
	if !exists {
		entry = &userEntry{
			timestamps: make([]time.Time, 0, fg.limitPerMinute+1),
		}
		fg.entries[key] = entry
	}

	// Update last seen time
	entry.lastSeen = now

	// Remove timestamps outside the window
	windowStart := now.Add(-windowDuration)
	validTimestamps := entry.timestamps[:0] // Reuse slice capacity
	for _, ts := range entry.timestamps {
		if ts.After(windowStart) {
			validTimestamps = append(validTimestamps, ts)
		}
	}
	entry.timestamps = validTimestamps

	// Check if user has exceeded the limit
	if len(entry.timestamps) >= fg.limitPerMinute {
		// User has exceeded the limit, do not allow message
		return false
	}

	// Add current timestamp and allow message
	entry.timestamps = append(entry.timestamps, now)
	return true
}

// cleanup removes idle user entries to prevent memory leaks
func (fg *Floodgate) cleanup() {
	// Run immediately on startup
	fg.performCleanup()

	ticker := time.NewTicker(cleanupInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			fg.performCleanup()
		case <-fg.stopCleanup:
			return
		}
	}
}

// performCleanup removes entries that have been idle for too long
func (fg *Floodgate) performCleanup() {
	fg.mutex.Lock()
	defer fg.mutex.Unlock()

	cutoff := time.Now().Add(-idleTimeout)
	for key, entry := range fg.entries {
		if entry.lastSeen.Before(cutoff) {
			delete(fg.entries, key)
		}
	}
}

// GetStats returns statistics about the floodgate for monitoring/debugging
func (fg *Floodgate) GetStats() Stats {
	fg.mutex.RLock()
	defer fg.mutex.RUnlock()

	return Stats{
		ActiveUsers:    len(fg.entries),
		LimitPerMinute: fg.limitPerMinute,
		WindowSeconds:  int(windowDuration.Seconds()), // Fixed 1-minute window
	}
}

// Stats contains floodgate statistics
type Stats struct {
	ActiveUsers    int `json:"active_users"`
	LimitPerMinute int `json:"limit_per_minute"`
	WindowSeconds  int `json:"window_seconds"`
}
