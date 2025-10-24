package core

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"go.uber.org/zap"

	"whatdj/internal/chat"
)

const (
	// Auto-approval timing constants
	autoApprovalReactionDelay = 100 * time.Millisecond
	autoApprovalProcessDelay  = 500 * time.Millisecond

	// Track mood fallback
	unknownTrackMood = "unknown style"
)

// Approval Management
// This module handles all forms of approval workflows including user confirmation,
// admin approval, community approval, and queue track approval

// promptEnhancedApproval asks for user approval with enhanced context
func (d *Dispatcher) promptEnhancedApproval(ctx context.Context, msgCtx *MessageContext,
	originalMsg *chat.Message, candidate *LLMCandidate) {
	msgCtx.State = StateConfirmationPrompt

	// Build format components
	albumPart := ""
	if candidate.Track.Album != "" {
		albumPart = d.localizer.T("format.album", candidate.Track.Album)
	}

	yearPart := ""
	if candidate.Track.Year > 0 {
		yearPart = d.localizer.T("format.year", candidate.Track.Year)
	}

	urlPart := ""
	if candidate.Track.URL != "" {
		urlPart = d.localizer.T("format.url", candidate.Track.URL)
	}

	prompt := d.localizer.T("prompt.enhanced_approval",
		candidate.Track.Artist, candidate.Track.Title, albumPart, yearPart, urlPart)
	promptWithMention := d.formatMessageWithMention(originalMsg, prompt)

	approved, err := d.frontend.AwaitApproval(ctx, originalMsg, promptWithMention, d.config.App.ConfirmTimeoutSecs)
	if err != nil {
		d.logger.Error("Failed to get enhanced approval", zap.Error(err))
		d.replyError(ctx, msgCtx, originalMsg, d.localizer.T("error.generic"))
		return
	}

	if approved {
		d.handleEnhancedApproval(ctx, msgCtx, originalMsg)
	} else {
		d.handleRejection(ctx, msgCtx, originalMsg)
	}
}

// handleEnhancedApproval processes approval for enhanced candidates
func (d *Dispatcher) handleEnhancedApproval(ctx context.Context, msgCtx *MessageContext, originalMsg *chat.Message) {
	if len(msgCtx.Candidates) == 0 {
		d.replyError(ctx, msgCtx, originalMsg, d.localizer.T("error.generic"))
		return
	}

	best := msgCtx.Candidates[0]

	// For enhanced candidates, we already have validated Spotify data
	// Try to find the exact track ID from our previous search
	tracks, err := d.spotify.SearchTrack(ctx, fmt.Sprintf("%s %s", best.Track.Artist, best.Track.Title))
	if err != nil || len(tracks) == 0 {
		d.replyError(ctx, msgCtx, originalMsg, d.localizer.T("error.spotify.not_found"))
		return
	}

	// Find the best matching track (should be the same as our enhanced result)
	var trackID string
	for _, track := range tracks {
		if track.Artist == best.Track.Artist && track.Title == best.Track.Title {
			trackID = track.ID
			break
		}
	}

	// Fallback to first result if exact match not found
	if trackID == "" {
		trackID = tracks[0].ID
	}

	if d.dedup.Has(trackID) {
		d.reactDuplicate(ctx, msgCtx, originalMsg)
		return
	}

	d.addToPlaylist(ctx, msgCtx, originalMsg, trackID)
}

// promptApproval asks for user approval with high confidence
func (d *Dispatcher) promptApproval(ctx context.Context, msgCtx *MessageContext, originalMsg *chat.Message, candidate *LLMCandidate) {
	msgCtx.State = StateConfirmationPrompt

	// Build format components
	yearPart := ""
	if candidate.Track.Year > 0 {
		yearPart = d.localizer.T("format.year", candidate.Track.Year)
	}

	urlPart := ""
	if candidate.Track.URL != "" {
		urlPart = d.localizer.T("format.url", candidate.Track.URL)
	}

	prompt := d.localizer.T("prompt.basic_approval",
		candidate.Track.Artist, candidate.Track.Title, yearPart, urlPart)
	promptWithMention := d.formatMessageWithMention(originalMsg, prompt)

	approved, err := d.frontend.AwaitApproval(ctx, originalMsg, promptWithMention, d.config.App.ConfirmTimeoutSecs)
	if err != nil {
		d.logger.Error("Failed to get approval", zap.Error(err))
		d.replyError(ctx, msgCtx, originalMsg, d.localizer.T("error.generic"))
		return
	}

	if approved {
		d.handleApproval(ctx, msgCtx, originalMsg)
	} else {
		d.handleRejection(ctx, msgCtx, originalMsg)
	}
}

// handleApproval processes user approval
func (d *Dispatcher) handleApproval(ctx context.Context, msgCtx *MessageContext, originalMsg *chat.Message) {
	if len(msgCtx.Candidates) == 0 {
		d.replyError(ctx, msgCtx, originalMsg, d.localizer.T("error.generic"))
		return
	}

	best := msgCtx.Candidates[0]

	tracks, err := d.spotify.SearchTrack(ctx, fmt.Sprintf("%s %s", best.Track.Artist, best.Track.Title))
	if err != nil || len(tracks) == 0 {
		d.replyError(ctx, msgCtx, originalMsg, d.localizer.T("error.spotify.not_found"))
		return
	}

	trackID := tracks[0].ID
	if d.dedup.Has(trackID) {
		d.reactDuplicate(ctx, msgCtx, originalMsg)
		return
	}

	d.addToPlaylist(ctx, msgCtx, originalMsg, trackID)
}

// handleRejection processes user rejection
func (d *Dispatcher) handleRejection(ctx context.Context, msgCtx *MessageContext, originalMsg *chat.Message) {
	d.askWhichSong(ctx, msgCtx, originalMsg)
}

// isAdminApprovalRequired checks if admin approval is enabled
func (d *Dispatcher) isAdminApprovalRequired() bool {
	// Check if the frontend supports admin approval
	if telegramFrontend, ok := d.frontend.(interface {
		IsAdminApprovalEnabled() bool
	}); ok {
		return telegramFrontend.IsAdminApprovalEnabled()
	}
	return false
}

// isAdminNeedsApproval checks if admins also need approval
func (d *Dispatcher) isAdminNeedsApproval() bool {
	return d.config.Telegram.AdminNeedsApproval
}

// isUserAdmin checks if the message sender is an admin in the chat
func (d *Dispatcher) isUserAdmin(ctx context.Context, msg *chat.Message) bool {
	isAdmin, err := d.frontend.IsUserAdmin(ctx, msg.ChatID, msg.SenderID)
	if err != nil {
		d.logger.Warn("Failed to check admin status, assuming non-admin",
			zap.String("userID", msg.SenderID),
			zap.String("chatID", msg.ChatID),
			zap.Error(err))
		return false
	}

	d.logger.Debug("Admin status checked",
		zap.String("userID", msg.SenderID),
		zap.String("userName", msg.SenderName),
		zap.Bool("isAdmin", isAdmin))

	return isAdmin
}

// awaitAdminApproval requests admin approval before adding to playlist
func (d *Dispatcher) awaitAdminApproval(ctx context.Context, msgCtx *MessageContext, originalMsg *chat.Message, trackID string) {
	msgCtx.State = StateAwaitAdminApproval

	// Get track information for the approval request
	track, err := d.spotify.GetTrack(ctx, trackID)
	if err != nil {
		d.logger.Error("Failed to get track info for admin approval", zap.Error(err))
		d.reactError(ctx, msgCtx, originalMsg, "Failed to get track information")
		return
	}

	songInfo := fmt.Sprintf("%s - %s", track.Artist, track.Title)
	songURL := track.URL

	// Generate track mood for approval messages
	var trackMood string
	if d.llm != nil {
		mood, moodErr := d.llm.GenerateTrackMood(ctx, []Track{*track})
		if moodErr != nil {
			d.logger.Warn("Failed to generate track mood for approval, using fallback",
				zap.Error(moodErr), zap.String("trackID", trackID))
			trackMood = unknownTrackMood
		} else {
			trackMood = mood
		}
	} else {
		trackMood = unknownTrackMood
	}

	// Send notification to channel that admin approval is required with song details
	approvalMessage := d.formatCommunityApprovalMessage(track, trackMood)
	approvalMsgID, err := d.frontend.SendText(ctx, originalMsg.ChatID, originalMsg.ID, approvalMessage)
	if err != nil {
		d.logger.Error("Failed to notify user about admin approval", zap.Error(err))
	}

	// Add reaction buttons if message was sent successfully and community approval is enabled
	if approvalMsgID != "" {
		d.addApprovalReactions(ctx, originalMsg.ChatID, approvalMsgID)
	}

	// Check if frontend supports both admin approval and community approval
	telegramFrontend, supportsAdminApproval := d.frontend.(interface {
		AwaitAdminApproval(ctx context.Context, origin *chat.Message, songInfo, songURL, trackMood string, timeoutSec int) (bool, error)
	})

	communityApprovalFrontend, supportsCommunityApproval := d.frontend.(interface {
		AwaitCommunityApproval(ctx context.Context, msgID string, requiredReactions int, timeoutSec int, requesterUserID int64) (bool, error)
	})

	if !supportsAdminApproval {
		d.logger.Error("Frontend doesn't support admin approval, proceeding without")
		d.executePlaylistAddWithReaction(ctx, msgCtx, originalMsg, trackID)
		return
	}

	// Check if community approval is enabled and supported
	communityApprovalThreshold := d.config.Telegram.CommunityApproval
	if supportsCommunityApproval && communityApprovalThreshold > 0 && approvalMsgID != "" {
		// Run both admin approval and community approval concurrently
		d.awaitConcurrentApproval(ctx, msgCtx, originalMsg, trackID, songInfo, songURL, trackMood, approvalMsgID,
			telegramFrontend, communityApprovalFrontend, communityApprovalThreshold)
	} else {
		// Only admin approval
		d.awaitAdminApprovalOnly(ctx, msgCtx, originalMsg, trackID, songInfo, songURL, trackMood, approvalMsgID, telegramFrontend)
	}
}

// awaitConcurrentApproval runs both admin and community approval concurrently
func (d *Dispatcher) awaitConcurrentApproval(
	ctx context.Context, msgCtx *MessageContext, originalMsg *chat.Message, trackID, songInfo, songURL, trackMood, approvalMsgID string,
	adminFrontend interface {
		AwaitAdminApproval(ctx context.Context, origin *chat.Message, songInfo, songURL, trackMood string, timeoutSec int) (bool, error)
	},
	communityFrontend interface {
		AwaitCommunityApproval(ctx context.Context, msgID string, requiredReactions int, timeoutSec int, requesterUserID int64) (bool, error)
	},
	communityThreshold int,
) {
	// Create channels for results
	adminResult := make(chan bool, 1)
	communityResult := make(chan bool, 1)
	const maxConcurrentApprovals = 2
	errorResult := make(chan error, maxConcurrentApprovals)

	// Start admin approval in goroutine
	go func() {
		approved, err := adminFrontend.AwaitAdminApproval(ctx, originalMsg, songInfo, songURL, trackMood, d.config.App.ConfirmAdminTimeoutSecs)
		if err != nil {
			errorResult <- err
			return
		}
		adminResult <- approved
	}()

	// Start community approval in goroutine
	go func() {
		// Convert string user ID to int64 for Telegram format
		requesterUserID, err := strconv.ParseInt(originalMsg.SenderID, 10, 64)
		if err != nil {
			d.logger.Warn("Failed to parse requester user ID, defaulting to 0",
				zap.String("senderID", originalMsg.SenderID), zap.Error(err))
			requesterUserID = 0 // Fallback to 0 if parsing fails
		}

		approved, err := communityFrontend.AwaitCommunityApproval(ctx, approvalMsgID, communityThreshold,
			d.config.App.ConfirmAdminTimeoutSecs, requesterUserID)
		if err != nil {
			errorResult <- err
			return
		}
		communityResult <- approved
	}()

	// Wait for first approval or timeout
	select {
	case approved := <-adminResult:
		d.handleApprovalResult(ctx, msgCtx, originalMsg, trackID, songInfo, approvalMsgID, approved, "admin")
	case approved := <-communityResult:
		if approved {
			// Community approval succeeded - cancel admin approval and clean up admin messages
			if adminCanceller, ok := adminFrontend.(interface {
				CancelAdminApproval(ctx context.Context, origin *chat.Message)
			}); ok {
				adminCanceller.CancelAdminApproval(ctx, originalMsg)
			}
			d.handleApprovalResult(ctx, msgCtx, originalMsg, trackID, songInfo, approvalMsgID, true, "community")
		} else {
			// Community approval failed, still wait for admin
			select {
			case approved := <-adminResult:
				d.handleApprovalResult(ctx, msgCtx, originalMsg, trackID, songInfo, approvalMsgID, approved, "admin")
			case err := <-errorResult:
				d.logger.Error("Admin approval failed", zap.Error(err))
				d.reactError(ctx, msgCtx, originalMsg, d.localizer.T("error.admin.process_failed"))
			case <-ctx.Done():
				d.handleApprovalResult(ctx, msgCtx, originalMsg, trackID, songInfo, approvalMsgID, false, "timeout")
			}
		}
	case err := <-errorResult:
		d.logger.Error("Approval process failed", zap.Error(err))
		d.reactError(ctx, msgCtx, originalMsg, d.localizer.T("error.admin.process_failed"))
	case <-ctx.Done():
		d.handleApprovalResult(ctx, msgCtx, originalMsg, trackID, songInfo, approvalMsgID, false, "timeout")
	}
}

// awaitAdminApprovalOnly handles only admin approval (legacy behavior)
func (d *Dispatcher) awaitAdminApprovalOnly(
	ctx context.Context, msgCtx *MessageContext, originalMsg *chat.Message, trackID, songInfo, songURL, trackMood, approvalMsgID string,
	adminFrontend interface {
		AwaitAdminApproval(ctx context.Context, origin *chat.Message, songInfo, songURL, trackMood string, timeoutSec int) (bool, error)
	},
) {
	approved, err := adminFrontend.AwaitAdminApproval(ctx, originalMsg, songInfo, songURL, trackMood, d.config.App.ConfirmAdminTimeoutSecs)
	if err != nil {
		d.logger.Error("Admin approval failed", zap.Error(err))
		d.reactError(ctx, msgCtx, originalMsg, d.localizer.T("error.admin.process_failed"))
		return
	}

	d.handleApprovalResult(ctx, msgCtx, originalMsg, trackID, songInfo, approvalMsgID, approved, "admin")
}

// handleApprovalResult processes the approval result regardless of source
func (d *Dispatcher) handleApprovalResult(
	ctx context.Context, msgCtx *MessageContext, originalMsg *chat.Message, trackID, songInfo, approvalMsgID string,
	approved bool, approvalSource string,
) {
	// Delete the admin approval required message
	if approvalMsgID != "" {
		if deleteErr := d.frontend.DeleteMessage(ctx, originalMsg.ChatID, approvalMsgID); deleteErr != nil {
			d.logger.Debug("Failed to delete admin approval message", zap.Error(deleteErr))
		}
	}

	if approved {
		d.logger.Info("Song addition approved",
			zap.String("user", originalMsg.SenderName),
			zap.String("song", songInfo),
			zap.String("approval_source", approvalSource))

		// Skip individual approval message - will be combined with success message
		d.executePlaylistAddAfterApproval(ctx, msgCtx, originalMsg, trackID, approvalSource)
	} else {
		d.logger.Info("Song addition denied",
			zap.String("user", originalMsg.SenderName),
			zap.String("song", songInfo),
			zap.String("approval_source", approvalSource))

		// Notify user of denial
		denialMessage := d.formatMessageWithMention(originalMsg, d.localizer.T("admin.denied"))
		if _, err := d.frontend.SendText(ctx, originalMsg.ChatID, originalMsg.ID, denialMessage); err != nil {
			d.logger.Error("Failed to notify user about denial", zap.Error(err))
		}

		// React with thumbs down on the original request
		if err := d.frontend.React(ctx, originalMsg.ChatID, originalMsg.ID, thumbsDownReaction); err != nil {
			d.logger.Warn("Failed to react with thumbs down on denied song request",
				zap.String("chatID", originalMsg.ChatID),
				zap.String("messageID", originalMsg.ID),
				zap.Error(err))
		}
	}
}

// executePlaylistAddAfterApproval performs playlist addition after approval with appropriate messaging
func (d *Dispatcher) executePlaylistAddAfterApproval(
	ctx context.Context, msgCtx *MessageContext, originalMsg *chat.Message, trackID, approvalSource string) {
	msgCtx.State = StateAddToPlaylist

	// Check if this was a priority request that needs special handling
	if msgCtx.IsPriority {
		d.executePriorityQueue(ctx, msgCtx, originalMsg, trackID)
		return
	}

	// Add track to playlist
	for retry := 0; retry < d.config.App.MaxRetries; retry++ {
		if err := d.spotify.AddToPlaylist(ctx, d.config.Spotify.PlaylistID, trackID); err != nil {
			d.logger.Error("Failed to add to playlist",
				zap.String("trackID", trackID),
				zap.Int("retry", retry),
				zap.Error(err))

			if retry == d.config.App.MaxRetries-1 {
				d.reactError(ctx, msgCtx, originalMsg, d.localizer.T("error.playlist.add_failed"))
				return
			}

			time.Sleep(time.Duration(d.config.App.RetryDelaySecs) * time.Second)
			continue
		}

		d.dedup.Add(trackID)

		// React with thumbs up
		if reactErr := d.frontend.React(ctx, originalMsg.ChatID, originalMsg.ID, thumbsUpReaction); reactErr != nil {
			d.logger.Error("Failed to react with thumbs up", zap.Error(reactErr))
		}

		// Send appropriate success message based on approval source
		if approvalSource == "admin" {
			d.reactAddedAfterApproval(ctx, msgCtx, originalMsg, trackID)
		} else if approvalSource == "community" {
			d.reactAddedAfterCommunityApproval(ctx, msgCtx, originalMsg, trackID)
		} else {
			// Fallback for unknown approval sources
			d.reactAdded(ctx, msgCtx, originalMsg, trackID)
		}
		return
	}
}

// getQueueApprovalMessageKey determines the correct message key based on auto-approval status
func getQueueApprovalMessageKey(baseKey string, autoApprove bool) string {
	if !autoApprove {
		return baseKey
	}
	switch baseKey {
	case "bot.queue_management":
		return "bot.queue_management_auto"
	case "bot.queue_replacement":
		return "bot.queue_replacement_auto"
	default:
		return baseKey
	}
}

// sendQueueTrackApprovalMessage sends an queue approval message with fallback to regular text
func (d *Dispatcher) sendQueueTrackApprovalMessage(
	ctx context.Context, trackID string, track *Track, messageKey, logContext string, autoApprove bool, mood, newTrackMood string,
) {
	groupID := d.getGroupID()
	if groupID == "" {
		return
	}

	message := d.localizer.T(messageKey, track.Artist, track.Title, track.URL, mood, newTrackMood)

	if autoApprove {
		// For auto-approval: send plain text message (no interactive buttons)
		messageID, err := d.frontend.SendText(ctx, groupID, "", message)
		if err != nil {
			d.logger.Warn("Failed to send auto-approval "+logContext+" message", zap.Error(err))
			// Still auto-approve even if message sending failed
			go func() {
				time.Sleep(autoApprovalReactionDelay)
				d.handleQueueTrackDecision(trackID, true)
			}()
			return
		}

		// Add thumbs up reaction for visual feedback
		if reactErr := d.frontend.React(ctx, groupID, messageID, chat.ReactionThumbsUp); reactErr != nil {
			d.logger.Debug("Failed to add thumbs up reaction for auto-approval", zap.Error(reactErr))
		}

		// Auto-approve after brief delay for visual effect
		go func() {
			time.Sleep(autoApprovalProcessDelay) // Longer delay so users can see the reaction
			d.handleQueueTrackDecision(trackID, true)
		}()

		d.logger.Info("Sent auto-approval "+logContext+" message",
			zap.String("trackID", trackID),
			zap.String("messageID", messageID),
			zap.String("artist", track.Artist),
			zap.String("title", track.Title))
	} else {
		// For manual approval: send interactive message with buttons
		messageID, err := d.frontend.SendQueueTrackApproval(ctx, groupID, trackID, message)
		if err != nil {
			d.logger.Warn("Failed to send "+logContext+" message", zap.Error(err))
			// If sending approval message fails, fall back to regular text
			if _, fallbackErr := d.frontend.SendText(ctx, groupID, "", message); fallbackErr != nil {
				d.logger.Warn("Failed to send fallback "+logContext+" message", zap.Error(fallbackErr))
			}
			return
		}

		// Start timeout tracking for this interactive approval message
		d.startQueueTrackApprovalTimeout(ctx, messageID, trackID, groupID)

		d.logger.Info("Sent "+logContext+" message with approval buttons",
			zap.String("trackID", trackID),
			zap.String("messageID", messageID),
			zap.String("artist", track.Artist),
			zap.String("title", track.Title))
	}
}

// startQueueTrackApprovalTimeout starts timeout tracking for an queue approval message
func (d *Dispatcher) startQueueTrackApprovalTimeout(ctx context.Context, messageID, trackID, chatID string) {
	timeoutSecs := d.config.App.QueueTrackApprovalTimeoutSecs
	if timeoutSecs <= 0 {
		return // Timeout disabled
	}

	timeoutCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSecs)*time.Second)

	approvalCtx := &queueApprovalContext{
		trackID:    trackID,
		chatID:     chatID,
		messageID:  messageID,
		expiresAt:  time.Now().Add(time.Duration(timeoutSecs) * time.Second),
		cancelFunc: cancel,
	}

	d.queueManagementMutex.Lock()
	d.pendingApprovalMessages[messageID] = approvalCtx
	d.queueManagementMutex.Unlock()

	// Start timeout goroutine
	go d.handleQueueTrackApprovalTimeout(timeoutCtx, messageID)
}

// handleQueueTrackApprovalTimeout handles timeout expiry for queue approval messages
func (d *Dispatcher) handleQueueTrackApprovalTimeout(ctx context.Context, messageID string) {
	<-ctx.Done()

	// Check if timeout expired (not canceled by approval/denial)
	if ctx.Err() == context.DeadlineExceeded {
		d.queueManagementMutex.Lock()
		approvalCtx, exists := d.pendingApprovalMessages[messageID]
		if !exists {
			d.queueManagementMutex.Unlock()
			return // Already handled by auto-approval or manual decision
		}

		trackID := approvalCtx.trackID
		chatID := approvalCtx.chatID

		// Double-check that the track is still pending (race condition protection)
		_, trackStillPending := d.pendingQueueTracks[trackID]
		if !trackStillPending {
			// Track was already processed by auto-approval, clean up and exit
			delete(d.pendingApprovalMessages, messageID)
			d.queueManagementMutex.Unlock()
			d.logger.Debug("Track already processed by auto-approval, skipping timeout handler",
				zap.String("trackID", trackID),
				zap.String("messageID", messageID))
			return
		}

		// Clean up pending approval and queue tracks
		delete(d.pendingApprovalMessages, messageID)
		delete(d.pendingQueueTracks, trackID)
		d.queueManagementMutex.Unlock()

		d.logger.Info("Queue approval timed out, auto-accepting track",
			zap.String("trackID", trackID),
			zap.String("messageID", messageID))

		// Actually add the track to queue and playlist
		if err := d.addApprovedQueueTrack(context.Background(), trackID); err != nil {
			d.logger.Error("Failed to add auto-accepted queue track",
				zap.String("trackID", trackID),
				zap.Error(err))
		}

		// Remove approval buttons to show auto-acceptance
		d.removeQueueTrackApprovalButtons(context.Background(), chatID, messageID)
	}
}

// removeQueueTrackApprovalButtons removes approval buttons from an queue message
func (d *Dispatcher) removeQueueTrackApprovalButtons(ctx context.Context, chatID, messageID string) {
	// For Telegram, we can edit the message to remove the inline keyboard
	// For WhatsApp, this is a no-op since it doesn't support inline buttons

	// Try to edit the message to remove buttons without changing the text (Telegram-specific)
	// This will gracefully fail for WhatsApp and other platforms that don't support message editing
	if err := d.editMessageToRemoveButtons(ctx, chatID, messageID, ""); err != nil {
		d.logger.Debug("Could not edit message to remove buttons (expected for WhatsApp)",
			zap.String("messageID", messageID),
			zap.Error(err))
	}

	// React with thumbs up to indicate auto-acceptance
	if err := d.frontend.React(ctx, chatID, messageID, thumbsUpReaction); err != nil {
		d.logger.Debug("Could not react to queue message (expected for WhatsApp)",
			zap.String("messageID", messageID),
			zap.Error(err))
	}
}

// editMessageToRemoveButtons attempts to edit a message to remove inline buttons (Telegram-specific)
func (d *Dispatcher) editMessageToRemoveButtons(ctx context.Context, chatID, messageID, newText string) error {
	return d.frontend.EditMessage(ctx, chatID, messageID, newText)
}
