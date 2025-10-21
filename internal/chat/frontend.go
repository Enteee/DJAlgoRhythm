// Package chat provides a unified interface for chat frontends (WhatsApp, Telegram, etc.)
package chat

import (
	"context"
)

// Message represents a normalized chat message from any frontend
type Message struct {
	ID         string
	ChatID     string
	SenderID   string
	SenderName string
	Text       string
	URLs       []string
	IsGroup    bool
	Raw        any // underlying library message struct
}

// Reaction represents standard emoji reactions
type Reaction string

// Standard reaction emojis used for user confirmations
const (
	ReactionThumbsUp   Reaction = "👍"
	ReactionThumbsDown Reaction = "👎"
)

// Frontend defines the unified interface for all chat integrations
type Frontend interface {
	// Start initializes the chat frontend and begins listening for updates
	Start(ctx context.Context) error

	// Listen starts listening for messages and calls the handler for each message
	Listen(ctx context.Context, handler func(*Message)) error

	// SendText sends a text message to the specified chat, optionally as a reply
	SendText(ctx context.Context, chatID string, replyToID string, text string) (string, error)

	// React adds an emoji reaction to a message
	React(ctx context.Context, chatID string, msgID string, r Reaction) error

	// AwaitApproval waits for user approval via reaction or inline buttons
	// Returns true if approved within timeout, false otherwise
	AwaitApproval(ctx context.Context, origin *Message, prompt string, timeoutSec int) (approved bool, err error)

	// IsUserAdmin checks if a user is an administrator in the chat
	IsUserAdmin(ctx context.Context, chatID, userID string) (bool, error)

	// DeleteMessage deletes a message by its ID
	DeleteMessage(ctx context.Context, chatID, msgID string) error

	// AwaitCommunityApproval waits for enough community 👍 reactions to bypass admin approval
	// Returns true if enough reactions received within timeout, false otherwise
	AwaitCommunityApproval(ctx context.Context, msgID string, requiredReactions int, timeoutSec int) (approved bool, err error)

	// GetAdminUserIDs returns a list of admin user IDs as strings for the group
	GetAdminUserIDs(ctx context.Context, chatID string) ([]string, error)

	// SendDirectMessage sends a direct message to a user and returns the message ID
	SendDirectMessage(ctx context.Context, userID, text string) (string, error)

	// SendQueueTrackApproval sends a queue track approval message with approve/deny buttons
	// Returns the message ID for tracking responses
	SendQueueTrackApproval(ctx context.Context, chatID, trackID, message string) (string, error)

	// SetQueueTrackDecisionHandler sets the handler for queue track approval/denial decisions
	SetQueueTrackDecisionHandler(handler func(trackID string, approved bool))

	// EditMessage edits an existing message by ID (returns error if not supported)
	EditMessage(ctx context.Context, chatID, messageID, newText string) error
}
