package core

import (
	"context"
	"time"

	"whatdj/internal/chat"
)

const (
	thumbsUpReaction   = chat.ReactionThumbsUp
	thumbsDownReaction = chat.ReactionThumbsDown

	// EndOfPlaylistThreshold is the number of tracks from the end to consider "near end"
	// This should match the value in internal/spotify/client.go
	endOfPlaylistThreshold = 3

	// Track info retrieval timeout
	trackInfoTimeoutSecs = 10
	// Timeout for initialization track update
	initializationTimeoutSecs = 5

	// Volume fade settings
	fadeStepDurationMs   = 100 // 100ms per step
	fadeSteps            = 20  // 20 steps = 2 seconds total
	playbackStartDelayMs = 500 // Delay for playback to start

	// Track info fallback constants
	unknownArtist = "Unknown"
	unknownTrack  = "Track"

	// Shadow queue constants
	estimatedTrackDurationMins = 3 // Default track duration in minutes when duration unknown
)

// ShadowQueueItem represents a track in our shadow queue for reliable queue management
type ShadowQueueItem struct {
	TrackID  string        // Spotify track ID
	URI      string        // Full Spotify URI
	Position int           // Position in logical queue (0 = next)
	Duration time.Duration // Track duration
	Source   string        // "playlist", "queue-fill", "priority", "manual"
	AddedAt  time.Time     // When we added this item
}

// queueApprovalContext tracks pending queue track approval messages with timeout information
type queueApprovalContext struct {
	trackID    string
	chatID     string
	messageID  string
	expiresAt  time.Time
	cancelFunc context.CancelFunc
}
