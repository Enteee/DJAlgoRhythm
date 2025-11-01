package core

import (
	"context"
	"fmt"
	"math/rand"
	"strconv"
	"strings"
	"time"

	"go.uber.org/zap"

	"djalgorhythm/internal/chat"
)

// Message Formatting and User Interactions
// This module handles all user-facing messages, reactions, and formatting logic

// reactAdded reacts to successfully added tracks.
func (d *Dispatcher) reactAdded(ctx context.Context, msgCtx *MessageContext, originalMsg *chat.Message,
	trackID string) {
	d.reactAddedWithMessage(ctx, msgCtx, originalMsg, trackID, "success.track_added")
}

// reactAddedAfterApproval reacts to successfully added tracks after admin approval.
func (d *Dispatcher) reactAddedAfterApproval(ctx context.Context, msgCtx *MessageContext,
	originalMsg *chat.Message, trackID string) {
	d.reactAddedWithMessage(ctx, msgCtx, originalMsg, trackID, "success.admin_approved_and_added")
}

// reactAddedAfterCommunityApproval reacts to successfully added tracks after community approval.
func (d *Dispatcher) reactAddedAfterCommunityApproval(
	ctx context.Context, msgCtx *MessageContext, originalMsg *chat.Message, trackID string) {
	d.reactAddedWithMessage(ctx, msgCtx, originalMsg, trackID, "success.community_approved_and_added")
}

// reactPriorityQueued reacts to priority tracks that were queued successfully.
func (d *Dispatcher) reactPriorityQueued(ctx context.Context, msgCtx *MessageContext,
	originalMsg *chat.Message, trackID string) {
	d.reactAddedWithMessage(ctx, msgCtx, originalMsg, trackID, "success.track_priority_playing")
}

// reactAddedWithMessage reacts to successfully added tracks with a specific message.
func (d *Dispatcher) reactAddedWithMessage(
	ctx context.Context, msgCtx *MessageContext, originalMsg *chat.Message, trackID, messageKey string) {
	msgCtx.State = StateReactAdded

	track, err := d.spotify.GetTrack(ctx, trackID)
	if err != nil {
		d.logger.Error("Failed to get track info", zap.Error(err))
		track = &Track{ID: trackID, Title: unknownTrack, Artist: unknownArtist}
	}

	// React with thumbs up
	if reactErr := d.frontend.React(ctx, originalMsg.ChatID, originalMsg.ID, thumbsUpReaction); reactErr != nil {
		d.logger.Error("Failed to react with thumbs up", zap.Error(reactErr))
	}

	// Check if we should include queue position in the message
	// Use shadow queue to get the track position directly (much simpler!)
	queuePosition := d.GetShadowQueuePosition(trackID)
	if queuePosition >= 0 {
		// Track found in playlist - use message with queue position
		var queueMessageKey string
		switch messageKey {
		case "success.track_added":
			queueMessageKey = "success.track_added_with_queue"
		case "success.admin_approved_and_added":
			queueMessageKey = "success.admin_approved_and_added_queue"
		default:
			// For other messages (like priority playing), fall back to original
			queueMessageKey = messageKey
		}

		if queueMessageKey != messageKey {
			// Use queue position message with 1-based indexing for user display
			successMessage := d.formatMessageWithMention(originalMsg,
				d.localizer.T(queueMessageKey, track.Artist, track.Title, track.URL, queuePosition+1))
			if _, sendErr := d.frontend.SendText(ctx, originalMsg.ChatID, originalMsg.ID, successMessage); sendErr != nil {
				d.logger.Error("Failed to send success message with queue position", zap.Error(sendErr))
			}
			return
		}
	}

	// Use basic message format without queue position
	successMessage := d.formatMessageWithMention(originalMsg,
		d.localizer.T(messageKey, track.Artist, track.Title, track.URL))
	if _, sendErr := d.frontend.SendText(ctx, originalMsg.ChatID, originalMsg.ID, successMessage); sendErr != nil {
		d.logger.Error("Failed to send success message", zap.Error(sendErr))
	}
}

// reactDuplicate reacts to duplicate track attempts.
func (d *Dispatcher) reactDuplicate(ctx context.Context, msgCtx *MessageContext, originalMsg *chat.Message) {
	msgCtx.State = StateReactDuplicate

	// React with thumbs down
	if err := d.frontend.React(ctx, originalMsg.ChatID, originalMsg.ID, thumbsDownReaction); err != nil {
		d.logger.Error("Failed to react with thumbs down", zap.Error(err))
	}

	// Reply with duplicate message
	duplicateMessage := d.formatMessageWithMention(originalMsg, d.localizer.T("success.duplicate"))
	if _, err := d.frontend.SendText(ctx, originalMsg.ChatID, originalMsg.ID, duplicateMessage); err != nil {
		d.logger.Error("Failed to reply with duplicate message", zap.Error(err))
	}
}

// reactError sends error messages.
func (d *Dispatcher) reactError(ctx context.Context, msgCtx *MessageContext, originalMsg *chat.Message,
	message string) {
	msgCtx.State = StateReactError
	errorMessage := d.formatMessageWithMention(originalMsg, message)
	if _, err := d.frontend.SendText(ctx, originalMsg.ChatID, originalMsg.ID, errorMessage); err != nil {
		d.logger.Error("Failed to reply with error message", zap.Error(err))
	}
}

// replyError sends error messages.
func (d *Dispatcher) replyError(ctx context.Context, msgCtx *MessageContext, originalMsg *chat.Message,
	message string) {
	d.reactError(ctx, msgCtx, originalMsg, message)
}

// replyHelp sends a help message to the user explaining how to use the bot.
func (d *Dispatcher) replyHelp(ctx context.Context, originalMsg *chat.Message) {
	helpMessage := d.localizer.T("bot.help_message")
	if _, err := d.frontend.SendText(ctx, originalMsg.ChatID, originalMsg.ID, helpMessage); err != nil {
		d.logger.Error("Failed to send help message", zap.Error(err))
	}
}

// reactProcessing adds a processing reaction to show the message is being handled.
func (d *Dispatcher) reactProcessing(ctx context.Context, msg *chat.Message) {
	if err := d.frontend.React(ctx, msg.ChatID, msg.ID, "👀"); err != nil {
		d.logger.Debug("Failed to add processing reaction", zap.Error(err))
	}
}

// reactIgnored adds a random "see/hear/speak no evil" emoji to ignored messages.
func (d *Dispatcher) reactIgnored(ctx context.Context, msg *chat.Message) {
	// Randomly choose one of the three "no evil" emojis
	ignoredEmojis := []string{"🙈", "🙉", "🙊"}
	emoji := ignoredEmojis[rand.Intn(len(ignoredEmojis))] // #nosec G404 - Non-cryptographic use for emoji selection

	if err := d.frontend.React(ctx, msg.ChatID, msg.ID, chat.Reaction(emoji)); err != nil {
		d.logger.Debug("Failed to add ignored reaction", zap.Error(err))
	}
}

// formatUserMention creates a user mention string based on the frontend type.
func (d *Dispatcher) formatUserMention(msg *chat.Message) string {
	// Always add @ prefix if not already present
	mention := "@" + msg.SenderName
	if strings.HasPrefix(msg.SenderName, "@") {
		mention = msg.SenderName
	}
	return mention
}

// formatMessageWithMention formats a message with user mention.
func (d *Dispatcher) formatMessageWithMention(msg *chat.Message, messageText string) string {
	mention := d.formatUserMention(msg)
	return fmt.Sprintf("%s %s", mention, messageText)
}

// formatCommunityApprovalMessage formats the community approval message with track details (for channel).
func (d *Dispatcher) formatCommunityApprovalMessage(track *Track, trackMood string) string {
	// Format album and year information
	var albumInfo, yearInfo string
	if track.Album != "" {
		albumInfo = d.localizer.T("format.album", track.Album)
	}
	if track.Year > 0 {
		yearInfo = d.localizer.T("format.year", track.Year)
	}

	// Format URL part for community message
	urlPart := ""
	if track.URL != "" {
		urlPart = d.localizer.T("format.url", track.URL)
	}

	return d.localizer.T("admin.approval_required_community",
		track.Artist, track.Title, albumInfo, yearInfo, urlPart, trackMood, d.config.Telegram.CommunityApproval)
}

// sendStartupMessage sends a startup notification to the group.
func (d *Dispatcher) sendStartupMessage(ctx context.Context) {
	if groupID := d.getGroupID(); groupID != "" {
		playlistURL := "https://open.spotify.com/playlist/" + d.config.Spotify.PlaylistID
		startupMessage := d.localizer.T("bot.startup", playlistURL)
		if _, err := d.frontend.SendText(ctx, groupID, "", startupMessage); err != nil {
			d.logger.Warn("Failed to send startup message", zap.Error(err))
		}
	}
}

// sendShutdownMessage sends a shutdown notification to the group.
func (d *Dispatcher) sendShutdownMessage(ctx context.Context) {
	if groupID := d.getGroupID(); groupID != "" {
		playlistURL := "https://open.spotify.com/playlist/" + d.config.Spotify.PlaylistID
		shutdownMessage := d.localizer.T("bot.shutdown", playlistURL)
		if _, err := d.frontend.SendText(ctx, groupID, "", shutdownMessage); err != nil {
			d.logger.Warn("Failed to send shutdown message", zap.Error(err))
		}
	}
}

// getGroupID returns the appropriate group ID based on enabled frontends.
func (d *Dispatcher) getGroupID() string {
	// Use the configuration to determine the group ID based on enabled frontend
	if d.config.Telegram.Enabled && d.config.Telegram.GroupID != 0 {
		return strconv.FormatInt(d.config.Telegram.GroupID, 10)
	}
	return ""
}

// convertToInputMessage converts a chat.Message to our internal InputMessage format.
func (d *Dispatcher) convertToInputMessage(msg *chat.Message) InputMessage {
	// Determine message type based on URLs and content
	msgType := MessageTypeFreeText
	var urls []string

	if len(msg.URLs) > 0 {
		urls = msg.URLs
		// Check if any URL is a Spotify link
		for _, url := range msg.URLs {
			if strings.Contains(url, "spotify.com") || strings.Contains(url, "spotify:") ||
				strings.Contains(url, "spotify.link") || strings.Contains(url, "spotify.app.link") {
				msgType = MessageTypeSpotifyLink
				break
			}
		}
		// If not Spotify, check for other music services
		if msgType == MessageTypeFreeText {
			msgType = MessageTypeNonSpotifyLink
		}
	}

	return InputMessage{
		Type:      msgType,
		Text:      msg.Text,
		URLs:      urls,
		GroupJID:  msg.ChatID,
		SenderJID: msg.SenderID,
		MessageID: msg.ID,
		Timestamp: time.Now(), // Original timestamp not available in chat.Message
	}
}

// addApprovalReactions adds thumbs up reaction for admin approval community notification.
func (d *Dispatcher) addApprovalReactions(ctx context.Context, chatID, msgID string) {
	// Add thumbs up reaction from bot as required for admin approval flow
	if err := d.frontend.React(ctx, chatID, msgID, thumbsUpReaction); err != nil {
		d.logger.Debug("Failed to add thumbs up reaction", zap.Error(err))
	}
}
