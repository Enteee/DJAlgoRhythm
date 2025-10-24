package core

import (
	"context"
	"time"

	"djalgorhythm/internal/chat"
)

const (
	thumbsUpReaction   = chat.ReactionThumbsUp
	thumbsDownReaction = chat.ReactionThumbsDown

	// Track info fallback constants
	unknownArtist = "Unknown"
	unknownTrack  = "Track"

	// Shadow queue constants
	// Track fetching constants
	maxTracksToFetch = 10 // Maximum tracks to fetch at once

	// Track source constants
	sourcePlaylist  = "playlist"
	sourcePriority  = "priority"
	sourceQueueFill = "queue-fill"
)

// ShadowQueueItem represents a track in our shadow queue for reliable queue management
type ShadowQueueItem struct {
	TrackID  string        // Spotify track ID
	Position int           // Position in logical queue (0 = next)
	Duration time.Duration // Track duration
	Source   string        // sourcePlaylist, sourceQueueFill, sourcePriority
	AddedAt  time.Time     // When we added this item
}

// PriorityTrackInfo stores information about a priority track for resume logic
type PriorityTrackInfo struct {
	ResumeSongID string // ID of song that was playing before priority interruption
}

// queueApprovalContext tracks pending queue track approval messages with timeout information
type queueApprovalContext struct {
	trackID    string
	chatID     string
	messageID  string
	expiresAt  time.Time
	cancelFunc context.CancelFunc
}

// QueueManagementFlow represents a single queue management flow with its own rejection tracking
type QueueManagementFlow struct {
	FlowID         string            // Unique identifier for this flow
	RejectionCount int               // Number of rejections in this flow
	PendingTracks  map[string]string // trackID -> track name for tracks pending in this flow
	CreatedAt      time.Time         // When this flow was started
}
