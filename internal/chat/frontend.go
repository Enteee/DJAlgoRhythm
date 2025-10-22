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

// User represents a Telegram user
type User struct {
	ID        int64  `json:"id"`
	IsBot     bool   `json:"is_bot"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name,omitempty"`
	Username  string `json:"username,omitempty"`
}

// ChatMember represents a chat member with their status and permissions
//
//nolint:revive // ChatMember is intentionally named for clarity across packages
type ChatMember struct {
	Status                 string `json:"status"`
	User                   *User  `json:"user"`
	CanDeleteMessages      bool   `json:"can_delete_messages,omitempty"`
	CanRestrictMembers     bool   `json:"can_restrict_members,omitempty"`
	CanPromoteMembers      bool   `json:"can_promote_members,omitempty"`
	CanChangeInfo          bool   `json:"can_change_info,omitempty"`
	CanInviteUsers         bool   `json:"can_invite_users,omitempty"`
	CanPinMessages         bool   `json:"can_pin_messages,omitempty"`
	CanManageVideoChats    bool   `json:"can_manage_video_chats,omitempty"`
	IsAnonymous            bool   `json:"is_anonymous,omitempty"`
	CanManageChat          bool   `json:"can_manage_chat,omitempty"`
	CanPostMessages        bool   `json:"can_post_messages,omitempty"`
	CanEditMessages        bool   `json:"can_edit_messages,omitempty"`
	CanDeleteVideoChats    bool   `json:"can_delete_video_chats,omitempty"`
	CanManageVideoChatsOld bool   `json:"can_manage_voice_chats,omitempty"` // Legacy field
}

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
	AwaitCommunityApproval(ctx context.Context, msgID string, requiredReactions int, timeoutSec int,
		requesterUserID int64) (approved bool, err error)

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

	// GetMe returns information about the bot user
	GetMe(ctx context.Context) (*User, error)

	// GetChatMember returns information about a chat member
	GetChatMember(ctx context.Context, chatID, userID int64) (*ChatMember, error)
}
