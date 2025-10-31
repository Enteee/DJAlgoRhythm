package core

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"go.uber.org/zap"

	"djalgorhythm/internal/chat"
)

const (
	// Auto-approval timing constants.
	autoApprovalReactionDelay = 100 * time.Millisecond
	autoApprovalProcessDelay  = 500 * time.Millisecond

	// Track mood fallback.
	unknownTrackMood = "unknown style"
)

// generateTrackMoodForCandidate generates track mood for a candidate and stores it in MessageContext.
func (d *Dispatcher) generateTrackMoodForCandidate(ctx context.Context, msgCtx *MessageContext, candidate *Track) {
	if msgCtx.TrackMood != "" {
		// Already generated, don't regenerate
		return
	}

	if d.llm != nil {
		if mood, moodErr := d.llm.GenerateTrackMood(ctx, []Track{*candidate}); moodErr != nil {
			d.logger.Warn("Failed to generate track mood for user prompt, using fallback",
				zap.Error(moodErr), zap.String("artist", candidate.Artist), zap.String("title", candidate.Title))
			msgCtx.TrackMood = unknownTrackMood
		} else {
			msgCtx.TrackMood = mood
		}
	} else {
		msgCtx.TrackMood = unknownTrackMood
	}
}

// Approval Management
// This module handles all forms of approval workflows including user confirmation,
// admin approval, community approval, and queue track approval

// promptEnhancedApproval asks for user approval with enhanced context.
func (d *Dispatcher) promptEnhancedApproval(ctx context.Context, msgCtx *MessageContext,
	originalMsg *chat.Message, candidate *Track) {
	msgCtx.State = StateConfirmationPrompt

	// Generate track mood for this candidate
	d.generateTrackMoodForCandidate(ctx, msgCtx, candidate)

	// Build format components
	albumPart := ""
	if candidate.Album != "" {
		albumPart = d.localizer.T("format.album", candidate.Album)
	}

	yearPart := ""
	if candidate.Year > 0 {
		yearPart = d.localizer.T("format.year", candidate.Year)
	}

	urlPart := ""
	if candidate.URL != "" {
		urlPart = d.localizer.T("format.url", candidate.URL)
	}

	prompt := d.localizer.T("prompt.enhanced_approval",
		candidate.Artist, candidate.Title, albumPart, yearPart, urlPart, msgCtx.TrackMood)
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

// handleEnhancedApproval processes approval for enhanced candidates.
func (d *Dispatcher) handleEnhancedApproval(ctx context.Context, msgCtx *MessageContext, originalMsg *chat.Message) {
	if len(msgCtx.Candidates) == 0 {
		d.replyError(ctx, msgCtx, originalMsg, d.localizer.T("error.generic"))
		return
	}

	best := msgCtx.Candidates[0]

	// For enhanced tracks, we already have validated Spotify data
	// Try to find the exact track ID from our previous search
	tracks, err := d.spotify.SearchTrack(ctx, fmt.Sprintf("%s %s", best.Artist, best.Title))
	if err != nil || len(tracks) == 0 {
		d.replyError(ctx, msgCtx, originalMsg, d.localizer.T("error.spotify.not_found"))
		return
	}

	// Find the best matching track (should be the same as our enhanced result)
	var trackID string
	for _, track := range tracks {
		if track.Artist == best.Artist && track.Title == best.Title {
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

// handleRejection processes user rejection.
func (d *Dispatcher) handleRejection(ctx context.Context, msgCtx *MessageContext, originalMsg *chat.Message) {
	// React with thumbs down to provide visual feedback for rejection
	if reactErr := d.frontend.React(ctx, originalMsg.ChatID, originalMsg.ID, thumbsDownReaction); reactErr != nil {
		d.logger.Error("Failed to react with thumbs down", zap.Error(reactErr))
	}

	d.askWhichSong(ctx, msgCtx, originalMsg)
}

// isAdminApprovalRequired checks if admin approval is enabled.
func (d *Dispatcher) isAdminApprovalRequired() bool {
	// Check if the frontend supports admin approval
	if telegramFrontend, ok := d.frontend.(interface {
		IsAdminApprovalEnabled() bool
	}); ok {
		return telegramFrontend.IsAdminApprovalEnabled()
	}
	return false
}

// isAdminNeedsApproval checks if admins also need approval.
func (d *Dispatcher) isAdminNeedsApproval() bool {
	return d.config.Telegram.AdminNeedsApproval
}

// isUserAdmin checks if the message sender is an admin in the chat.
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

// awaitAdminApproval requests admin approval before adding to playlist.
func (d *Dispatcher) awaitAdminApproval(ctx context.Context, msgCtx *MessageContext,
	originalMsg *chat.Message, trackID string) {
	msgCtx.State = StateAwaitAdminApproval

	track, songInfo, songURL, trackMood, err := d.prepareTrackForApproval(ctx, trackID, msgCtx)
	if err != nil {
		d.reactError(ctx, msgCtx, originalMsg, "Failed to get track information")
		return
	}

	approvalMsgID := d.sendApprovalNotification(ctx, originalMsg, track, trackMood)

	adminFrontend, communityFrontend, err := d.validateApprovalSupport()
	if err != nil {
		d.logger.Error("Frontend doesn't support admin approval, proceeding without")
		d.executePlaylistAddWithReaction(ctx, msgCtx, originalMsg, trackID)
		return
	}

	d.executeApprovalStrategy(ctx, msgCtx, originalMsg, trackID, songInfo, songURL, trackMood,
		approvalMsgID, adminFrontend, communityFrontend)
}

// prepareTrackForApproval gets track information and mood for approval.
func (d *Dispatcher) prepareTrackForApproval(ctx context.Context, trackID string,
	msgCtx *MessageContext) (track *Track, songInfo, songURL, trackMood string, err error) {
	track, err = d.spotify.GetTrack(ctx, trackID)
	if err != nil {
		d.logger.Error("Failed to get track info for admin approval", zap.Error(err))
		return nil, "", "", "", err
	}

	songInfo = fmt.Sprintf("%s - %s", track.Artist, track.Title)
	songURL = track.URL
	trackMood = d.getOrGenerateTrackMood(ctx, msgCtx, track, trackID)

	return
}

// getOrGenerateTrackMood gets track mood from context or generates it.
func (d *Dispatcher) getOrGenerateTrackMood(ctx context.Context, msgCtx *MessageContext,
	track *Track, trackID string) string {
	if msgCtx.TrackMood != "" {
		d.logger.Debug("Reusing track mood from MessageContext",
			zap.String("trackID", trackID), zap.String("trackMood", msgCtx.TrackMood))
		return msgCtx.TrackMood
	}

	if d.llm == nil {
		return unknownTrackMood
	}

	mood, err := d.llm.GenerateTrackMood(ctx, []Track{*track})
	if err != nil {
		d.logger.Warn("Failed to generate track mood for approval, using fallback",
			zap.Error(err), zap.String("trackID", trackID))
		return unknownTrackMood
	}

	return mood
}

// sendApprovalNotification sends the approval notification message and adds reactions.
func (d *Dispatcher) sendApprovalNotification(ctx context.Context, originalMsg *chat.Message,
	track *Track, trackMood string) string {
	approvalMessage := d.formatCommunityApprovalMessage(track, trackMood)
	approvalMsgID, err := d.frontend.SendText(ctx, originalMsg.ChatID, originalMsg.ID, approvalMessage)
	if err != nil {
		d.logger.Error("Failed to notify user about admin approval", zap.Error(err))
		return ""
	}

	if approvalMsgID != "" {
		d.addApprovalReactions(ctx, originalMsg.ChatID, approvalMsgID)
	}

	return approvalMsgID
}

// validateApprovalSupport checks if the frontend supports required approval methods.
func (d *Dispatcher) validateApprovalSupport() (adminInterface interface {
	AwaitAdminApproval(ctx context.Context, origin *chat.Message, songInfo, songURL, trackMood string,
		timeoutSec int) (bool, error)
}, communityInterface interface {
	AwaitCommunityApproval(ctx context.Context, msgID string, requiredReactions int, timeoutSec int,
		requesterUserID int64) (bool, error)
}, err error) {
	adminFrontend, supportsAdminApproval := d.frontend.(interface {
		AwaitAdminApproval(ctx context.Context, origin *chat.Message, songInfo, songURL, trackMood string,
			timeoutSec int) (bool, error)
	})

	communityFrontend, supportsCommunityApproval := d.frontend.(interface {
		AwaitCommunityApproval(ctx context.Context, msgID string, requiredReactions int, timeoutSec int,
			requesterUserID int64) (bool, error)
	})

	if !supportsAdminApproval {
		return nil, nil, errors.New("frontend doesn't support admin approval")
	}

	var communityApprovalInterface interface {
		AwaitCommunityApproval(ctx context.Context, msgID string, requiredReactions int, timeoutSec int,
			requesterUserID int64) (bool, error)
	}
	if supportsCommunityApproval {
		communityApprovalInterface = communityFrontend
	}

	return adminFrontend, communityApprovalInterface, nil
}

// executeApprovalStrategy decides between concurrent or admin-only approval.
func (d *Dispatcher) executeApprovalStrategy(ctx context.Context, msgCtx *MessageContext,
	originalMsg *chat.Message, trackID, songInfo, songURL, trackMood, approvalMsgID string,
	adminFrontend interface {
		AwaitAdminApproval(ctx context.Context, origin *chat.Message, songInfo, songURL, trackMood string,
			timeoutSec int) (bool, error)
	},
	communityFrontend interface {
		AwaitCommunityApproval(ctx context.Context, msgID string, requiredReactions int, timeoutSec int,
			requesterUserID int64) (bool, error)
	}) {
	communityApprovalThreshold := d.config.Telegram.CommunityApproval
	if communityFrontend != nil && communityApprovalThreshold > 0 && approvalMsgID != "" {
		d.awaitConcurrentApproval(ctx, msgCtx, originalMsg, trackID, songInfo, songURL, trackMood,
			approvalMsgID, adminFrontend, communityFrontend, communityApprovalThreshold)
	} else {
		d.awaitAdminApprovalOnly(ctx, msgCtx, originalMsg, trackID, songInfo, songURL, trackMood,
			approvalMsgID, adminFrontend)
	}
}

// awaitConcurrentApproval runs both admin and community approval concurrently.
func (d *Dispatcher) awaitConcurrentApproval(
	ctx context.Context, msgCtx *MessageContext, originalMsg *chat.Message,
	trackID, songInfo, songURL, trackMood, approvalMsgID string,
	adminFrontend interface {
		AwaitAdminApproval(ctx context.Context, origin *chat.Message, songInfo, songURL, trackMood string,
			timeoutSec int) (bool, error)
	},
	communityFrontend interface {
		AwaitCommunityApproval(ctx context.Context, msgID string, requiredReactions int, timeoutSec int,
			requesterUserID int64) (bool, error)
	},
	communityThreshold int,
) {
	adminResult, communityResult, errorResult := d.createApprovalChannels()

	d.startAdminApproval(ctx, adminResult, errorResult, adminFrontend, originalMsg, songInfo, songURL, trackMood)
	d.startCommunityApproval(ctx, communityResult, errorResult, communityFrontend, originalMsg,
		approvalMsgID, communityThreshold)

	d.handleConcurrentApprovalResults(ctx, msgCtx, originalMsg, trackID, songInfo, approvalMsgID,
		adminResult, communityResult, errorResult, adminFrontend)
}

// createApprovalChannels creates the channels needed for concurrent approval.
func (d *Dispatcher) createApprovalChannels() (adminResult, communityResult chan bool, errorResult chan error) {
	adminResult = make(chan bool, 1)
	communityResult = make(chan bool, 1)
	const maxConcurrentApprovals = 2
	errorResult = make(chan error, maxConcurrentApprovals)
	return
}

// startAdminApproval starts the admin approval process in a goroutine..
func (d *Dispatcher) startAdminApproval(ctx context.Context, adminResult chan bool, errorResult chan error,
	adminFrontend interface {
		AwaitAdminApproval(ctx context.Context, origin *chat.Message, songInfo, songURL, trackMood string,
			timeoutSec int) (bool, error)
	}, originalMsg *chat.Message, songInfo, songURL, trackMood string) {
	go func() {
		approved, err := adminFrontend.AwaitAdminApproval(ctx, originalMsg, songInfo, songURL, trackMood,
			d.config.App.ConfirmAdminTimeoutSecs)
		if err != nil {
			errorResult <- err
			return
		}
		adminResult <- approved
	}()
}

// startCommunityApproval starts the community approval process in a goroutine..
func (d *Dispatcher) startCommunityApproval(ctx context.Context, communityResult chan bool, errorResult chan error,
	communityFrontend interface {
		AwaitCommunityApproval(ctx context.Context, msgID string, requiredReactions int, timeoutSec int,
			requesterUserID int64) (bool, error)
	}, originalMsg *chat.Message, approvalMsgID string, communityThreshold int) {
	go func() {
		requesterUserID := d.parseRequesterUserID(originalMsg.SenderID)
		approved, err := communityFrontend.AwaitCommunityApproval(ctx, approvalMsgID, communityThreshold,
			d.config.App.ConfirmAdminTimeoutSecs, requesterUserID)
		if err != nil {
			errorResult <- err
			return
		}
		communityResult <- approved
	}()
}

// parseRequesterUserID safely parses the requester user ID..
func (d *Dispatcher) parseRequesterUserID(senderID string) int64 {
	requesterUserID, err := strconv.ParseInt(senderID, 10, 64)
	if err != nil {
		d.logger.Warn("Failed to parse requester user ID, defaulting to 0",
			zap.String("senderID", senderID), zap.Error(err))
		return 0
	}
	return requesterUserID
}

// handleConcurrentApprovalResults handles the results from concurrent approval processes.
func (d *Dispatcher) handleConcurrentApprovalResults(ctx context.Context, msgCtx *MessageContext,
	originalMsg *chat.Message, trackID, songInfo, approvalMsgID string,
	adminResult, communityResult chan bool, errorResult chan error,
	adminFrontend interface {
		AwaitAdminApproval(ctx context.Context, origin *chat.Message, songInfo, songURL, trackMood string,
			timeoutSec int) (bool, error)
	}) {
	select {
	case approved := <-adminResult:
		d.handleApprovalResult(ctx, msgCtx, originalMsg, trackID, songInfo, approvalMsgID, approved, "admin")
	case approved := <-communityResult:
		d.handleCommunityApprovalResult(ctx, msgCtx, originalMsg, trackID, songInfo, approvalMsgID,
			approved, adminResult, errorResult, adminFrontend)
	case err := <-errorResult:
		d.logger.Error("Approval process failed", zap.Error(err))
		d.reactError(ctx, msgCtx, originalMsg, d.localizer.T("error.admin.process_failed"))
	case <-ctx.Done():
		d.handleApprovalResult(ctx, msgCtx, originalMsg, trackID, songInfo, approvalMsgID, false, "timeout")
	}
}

// handleCommunityApprovalResult handles community approval results and fallback to admin if needed.
func (d *Dispatcher) handleCommunityApprovalResult(ctx context.Context, msgCtx *MessageContext,
	originalMsg *chat.Message, trackID, songInfo, approvalMsgID string, approved bool,
	adminResult chan bool, errorResult chan error,
	adminFrontend interface {
		AwaitAdminApproval(ctx context.Context, origin *chat.Message, songInfo, songURL, trackMood string,
			timeoutSec int) (bool, error)
	}) {
	if approved {
		d.cancelAdminApproval(ctx, adminFrontend, originalMsg)
		d.handleApprovalResult(ctx, msgCtx, originalMsg, trackID, songInfo, approvalMsgID, true, "community")
		return
	}

	// Community approval failed, wait for admin
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

// cancelAdminApproval cancels admin approval if the frontend supports it.
func (d *Dispatcher) cancelAdminApproval(ctx context.Context,
	adminFrontend interface {
		AwaitAdminApproval(ctx context.Context, origin *chat.Message, songInfo, songURL, trackMood string,
			timeoutSec int) (bool, error)
	}, originalMsg *chat.Message) {
	if adminCanceller, ok := adminFrontend.(interface {
		CancelAdminApproval(ctx context.Context, origin *chat.Message)
	}); ok {
		adminCanceller.CancelAdminApproval(ctx, originalMsg)
	}
}

// awaitAdminApprovalOnly handles only admin approval (legacy behavior).
func (d *Dispatcher) awaitAdminApprovalOnly(
	ctx context.Context, msgCtx *MessageContext, originalMsg *chat.Message,
	trackID, songInfo, songURL, trackMood, approvalMsgID string,
	adminFrontend interface {
		AwaitAdminApproval(ctx context.Context, origin *chat.Message, songInfo, songURL, trackMood string,
			timeoutSec int) (bool, error)
	},
) {
	approved, err := adminFrontend.AwaitAdminApproval(ctx, originalMsg, songInfo, songURL, trackMood,
		d.config.App.ConfirmAdminTimeoutSecs)
	if err != nil {
		d.logger.Error("Admin approval failed", zap.Error(err))
		d.reactError(ctx, msgCtx, originalMsg, d.localizer.T("error.admin.process_failed"))
		return
	}

	d.handleApprovalResult(ctx, msgCtx, originalMsg, trackID, songInfo, approvalMsgID, approved, "admin")
}

// handleApprovalResult processes the approval result regardless of source.
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

// executePlaylistAddAfterApproval performs playlist addition after approval with appropriate messaging.
func (d *Dispatcher) executePlaylistAddAfterApproval(
	ctx context.Context, msgCtx *MessageContext, originalMsg *chat.Message, trackID, approvalSource string) {
	msgCtx.State = StateAddToPlaylist

	// Check if this was a priority request that needs special handling
	if msgCtx.IsPriority {
		d.executePriorityQueue(ctx, msgCtx, originalMsg, trackID)
		return
	}

	// Add track to playlist and wake up queue manager.
	if err := d.addToPlaylistAndWakeQueueManager(ctx, trackID); err != nil {
		d.logger.Error("Failed to add to playlist",
			zap.String("trackID", trackID),
			zap.Error(err))
		d.reactError(ctx, msgCtx, originalMsg, d.localizer.T("error.playlist.add_failed"))
		return
	}

	// React with thumbs up
	if reactErr := d.frontend.React(ctx, originalMsg.ChatID, originalMsg.ID, thumbsUpReaction); reactErr != nil {
		d.logger.Error("Failed to react with thumbs up", zap.Error(reactErr))
	}

	// Send appropriate success message based on approval source
	switch approvalSource {
	case "admin":
		d.reactAddedAfterApproval(ctx, msgCtx, originalMsg, trackID)
	case "community":
		d.reactAddedAfterCommunityApproval(ctx, msgCtx, originalMsg, trackID)
	default:
		// Fallback for unknown approval sources
		d.reactAdded(ctx, msgCtx, originalMsg, trackID)
	}
}

// getQueueApprovalMessageKey determines the correct message key based on auto-approval status.
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

// sendQueueTrackApprovalMessage sends an queue approval message with fallback to regular text.
func (d *Dispatcher) sendQueueTrackApprovalMessage(
	ctx context.Context, trackID string, track *Track, messageKey, logContext string,
	autoApprove bool, mood, newTrackMood string,
) {
	groupID := d.getGroupID()
	if groupID == "" {
		return
	}

	message := d.localizer.T(messageKey, track.Artist, track.Title, track.URL, mood, newTrackMood)

	if autoApprove {
		d.sendAutoApprovalMessage(ctx, groupID, trackID, track, message, logContext)
	} else {
		d.sendManualApprovalMessage(ctx, groupID, trackID, message, logContext)
	}
}

// sendAutoApprovalMessage sends an auto-approval message with automatic approval.
func (d *Dispatcher) sendAutoApprovalMessage(ctx context.Context, groupID, trackID string, track *Track, message, logContext string) {
	// For auto-approval: send plain text message (no interactive buttons)
	messageID, err := d.frontend.SendText(ctx, groupID, "", message)
	if err != nil {
		d.logger.Warn("Failed to send auto-approval "+logContext+" message", zap.Error(err))
		// Still auto-approve even if message sending failed
		go func(c context.Context, tid string) {
			time.Sleep(autoApprovalReactionDelay)
			d.handleQueueTrackDecision(c, tid, true)
		}(ctx, trackID)
		return
	}

	// Add thumbs up reaction for visual feedback
	if reactErr := d.frontend.React(ctx, groupID, messageID, chat.ReactionThumbsUp); reactErr != nil {
		d.logger.Debug("Failed to add thumbs up reaction for auto-approval", zap.Error(reactErr))
	}

	// Auto-approve after brief delay for visual effect
	go func(c context.Context, tid string) {
		time.Sleep(autoApprovalProcessDelay) // Longer delay so users can see the reaction
		d.handleQueueTrackDecision(c, tid, true)
	}(ctx, trackID)

	d.logger.Info("Sent auto-approval "+logContext+" message",
		zap.String("trackID", trackID),
		zap.String("messageID", messageID),
		zap.String("artist", track.Artist),
		zap.String("title", track.Title))
}

// sendManualApprovalMessage sends a manual approval message with interactive buttons.
func (d *Dispatcher) sendManualApprovalMessage(ctx context.Context, groupID, trackID, message, logContext string) {
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
		zap.String("messageID", messageID))
}

// startQueueTrackApprovalTimeout starts timeout tracking for an queue approval message.
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

// handleQueueTrackApprovalTimeout handles timeout expiry for queue approval messages.
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
		trackStillPending := false
		for _, flow := range d.queueManagementFlows {
			if _, exists := flow.PendingTracks[trackID]; exists {
				trackStillPending = true
				break
			}
		}
		if !trackStillPending {
			// Track was already processed by auto-approval, clean up and exit
			delete(d.pendingApprovalMessages, messageID)
			d.queueManagementMutex.Unlock()
			d.logger.Debug("Track already processed by auto-approval, skipping timeout handler",
				zap.String("trackID", trackID),
				zap.String("messageID", messageID))
			return
		}

		// Clean up pending approval
		delete(d.pendingApprovalMessages, messageID)
		d.queueManagementMutex.Unlock()

		d.logger.Info("Queue approval timed out, auto-accepting track",
			zap.String("trackID", trackID),
			zap.String("messageID", messageID))

		// Create a fresh context for post-timeout operations (the original ctx is expired)
		// We must use context.Background() here because ctx has exceeded its deadline.
		const postTimeoutOperationTimeout = 10 * time.Second
		freshCtx, cancel := context.WithTimeout(context.Background(), postTimeoutOperationTimeout)
		defer cancel()

		// Actually add the track to queue and playlist
		//nolint:contextcheck // Parent context is intentionally expired; we need a fresh context
		if err := d.addApprovedQueueTrack(freshCtx, trackID); err != nil {
			d.logger.Error("Failed to add auto-accepted queue track",
				zap.String("trackID", trackID),
				zap.Error(err))
		}

		// Remove approval buttons to show auto-acceptance
		//nolint:contextcheck // Parent context is intentionally expired; we need a fresh context
		d.removeQueueTrackApprovalButtons(freshCtx, chatID, messageID)
	}
}

// removeQueueTrackApprovalButtons removes approval buttons from an queue message.
func (d *Dispatcher) removeQueueTrackApprovalButtons(ctx context.Context, chatID, messageID string) {
	// For Telegram, we can edit the message to remove the inline keyboard
	// This is a no-op for platforms that don't support inline buttons

	// Try to edit the message to remove buttons without changing the text (Telegram-specific)
	// This will gracefully fail for platforms that don't support message editing
	if err := d.editMessageToRemoveButtons(ctx, chatID, messageID, ""); err != nil {
		d.logger.Debug("Could not edit message to remove buttons (platform may not support editing)",
			zap.String("messageID", messageID),
			zap.Error(err))
	}

	// React with thumbs up to indicate auto-acceptance
	if err := d.frontend.React(ctx, chatID, messageID, thumbsUpReaction); err != nil {
		d.logger.Debug("Could not react to queue message (platform may not support reactions)",
			zap.String("messageID", messageID),
			zap.Error(err))
	}
}

// editMessageToRemoveButtons attempts to edit a message to remove inline buttons (Telegram-specific).
func (d *Dispatcher) editMessageToRemoveButtons(ctx context.Context, chatID, messageID, newText string) error {
	return d.frontend.EditMessage(ctx, chatID, messageID, newText)
}
