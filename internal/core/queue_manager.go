package core

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.uber.org/zap"

	"djalgorhythm/internal/chat"
)

// Queue Management and Track Addition
// This module handles all queue-related operations including playlist addition,
// priority queue management, queue filling, and queue-fill track logic

// addToPlaylist adds a track to the Spotify playlist or queue based on priority.
func (d *Dispatcher) addToPlaylist(ctx context.Context, msgCtx *MessageContext, originalMsg *chat.Message,
	trackID string) {
	msgCtx.SelectedID = trackID

	// Check if this is a priority request from an admin
	isAdmin := d.isUserAdmin(ctx, originalMsg)
	isPriority := false

	if isAdmin && d.llm != nil {
		var err error
		isPriority, err = d.llm.IsPriorityRequest(ctx, originalMsg.Text)
		if err != nil {
			d.logger.Warn("Failed to check priority status, treating as regular request",
				zap.Error(err),
				zap.String("text", originalMsg.Text))
		}

		d.logger.Debug("Priority request check completed",
			zap.Bool("isAdmin", isAdmin),
			zap.Bool("isPriority", isPriority),
			zap.String("text", originalMsg.Text))
	}

	// Store priority flag in message context for approval workflow
	msgCtx.IsPriority = isPriority

	// Check if admin approval is required
	// If AdminNeedsApproval is enabled, even admins need approval
	// Otherwise, only non-admins need approval when AdminApproval is enabled
	needsApproval := d.isAdminApprovalRequired() && (!isAdmin || d.isAdminNeedsApproval())
	if needsApproval {
		d.awaitAdminApproval(ctx, msgCtx, originalMsg, trackID)
		return
	}

	// If it's a priority request from an admin and no approval needed, add to queue for priority playback
	if isAdmin && isPriority {
		d.executePriorityQueue(ctx, msgCtx, originalMsg, trackID)
		return
	}

	d.executePlaylistAddWithReaction(ctx, msgCtx, originalMsg, trackID)
}

// executePriorityQueue adds priority track to queue and playlist.
func (d *Dispatcher) executePriorityQueue(ctx context.Context, msgCtx *MessageContext,
	originalMsg *chat.Message, trackID string) {
	msgCtx.State = StateAddToPlaylist

	// Get track details before adding to queue
	track, err := d.spotify.GetTrack(ctx, trackID)
	if err != nil {
		d.logger.Error("Failed to get track details for priority queue",
			zap.String("trackID", trackID),
			zap.Error(err))
		d.executePlaylistAddWithReaction(ctx, msgCtx, originalMsg, trackID)
		return
	}

	// Add priority track to queue (will play next)
	queueErr := d.AddToQueueWithShadowTracking(ctx, track, sourcePriority)
	if queueErr != nil {
		d.logger.Warn("Failed to add priority track to queue, proceeding with playlist only",
			zap.String("trackID", trackID),
			zap.Error(queueErr))
		// If queue fails, fall back to regular playlist addition
		d.executePlaylistAddWithReaction(ctx, msgCtx, originalMsg, trackID)
		return
	}

	d.logger.Info("Priority track added to queue",
		zap.String("trackID", trackID))

	// Get current track to determine resume position
	currentTrackID, err := d.spotify.GetCurrentTrackID(ctx)
	if err != nil {
		currentTrackID = "" // If no current track, use empty string
	}

	// Register priority track in the registry with resume song ID
	d.priorityTracksMutex.Lock()
	d.priorityTracks[trackID] = PriorityTrackInfo{
		ResumeSongID: currentTrackID,
	}
	d.priorityTracksMutex.Unlock()

	d.logger.Debug("Registered priority track in registry",
		zap.String("trackID", trackID),
		zap.String("resumeSongID", currentTrackID))

	// Add to playlist at position 0 (top) for history/deduplication to avoid replaying later
	if err := d.spotify.AddToPlaylistAtPosition(ctx, d.config.Spotify.PlaylistID, trackID, 0); err != nil {
		d.logger.Error("Failed to add priority track to playlist",
			zap.String("trackID", trackID),
			zap.Error(err))
		d.reactError(ctx, msgCtx, originalMsg, d.localizer.T("error.playlist.add_failed"))
		return
	}

	d.dedup.Add(trackID)
	d.reactPriorityQueued(ctx, msgCtx, originalMsg, trackID)
}

// executePlaylistAddWithReaction performs the actual playlist addition with appropriate reaction.
func (d *Dispatcher) executePlaylistAddWithReaction(
	ctx context.Context, msgCtx *MessageContext, originalMsg *chat.Message, trackID string) {
	msgCtx.State = StateAddToPlaylist

	// Add track to playlist and wake up queue manager.
	if err := d.addToPlaylistAndWakeQueueManager(ctx, trackID); err != nil {
		d.logger.Error("Failed to add to playlist",
			zap.String("trackID", trackID),
			zap.Error(err))
		d.reactError(ctx, msgCtx, originalMsg, d.localizer.T("error.playlist.add_failed"))
		return
	}

	d.reactAdded(ctx, msgCtx, originalMsg, trackID)
}

// addToPlaylistAndWakeQueueManager adds a track to the playlist, marks it as seen in dedup,
// and wakes up the queue manager to fill the queue from the updated playlist.
// This should be used for all regular playlist additions (not priority tracks).
func (d *Dispatcher) addToPlaylistAndWakeQueueManager(ctx context.Context, trackID string) error {
	// Add track to playlist.
	if err := d.spotify.AddToPlaylist(ctx, d.config.Spotify.PlaylistID, trackID); err != nil {
		return err
	}

	// Mark as seen to prevent duplicates.
	d.dedup.Add(trackID)

	// Wake up queue manager to fill queue from updated playlist.
	select {
	case d.queueManagementWakeup <- struct{}{}:
		d.logger.Debug("Sent wake-up signal to queue manager after playlist addition",
			zap.String("trackID", trackID))
	default:
		// Channel full means queue manager will wake up soon anyway.
		d.logger.Debug("Queue manager wake-up channel full, skipping signal",
			zap.String("trackID", trackID))
	}

	return nil
}

// runQueueAndPlaylistManagement manages queue duration and automatic track filling.
func (d *Dispatcher) runQueueAndPlaylistManagement(ctx context.Context) {
	d.logger.Info("Starting queue and playlist management",
		zap.Duration("interval", time.Duration(d.config.App.QueueCheckIntervalSecs)*time.Second),
		zap.Duration("targetDuration", time.Duration(d.config.App.QueueAheadDurationSecs)*time.Second))

	// Run immediately on startup
	d.checkAndManageQueue(ctx)

	interval := time.Duration(d.config.App.QueueCheckIntervalSecs) * time.Second
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			d.logger.Info("Queue and playlist management stopped")
			return
		case <-ticker.C:
			d.checkAndManageQueue(ctx)
		case <-d.queueManagementWakeup:
			d.logger.Info("Queue manager woken up by playlist update")

			// Check and manage queue with updated playlist.
			// Natural queue fullness checks will prevent adding autodj tracks if queue is already full.
			d.checkAndManageQueue(ctx)
		}
	}
}

// checkAndManageQueue implements the unified queue and playlist management logic.
func (d *Dispatcher) checkAndManageQueue(ctx context.Context) {
	d.logger.Debug("checkAndManageQueue called")

	if !d.checkSpotifyDeviceAvailability(ctx) {
		return
	}

	if !d.acquireQueueManagementLock() {
		return
	}
	defer d.resetQueueManagementFlag()

	d.performQueueManagement(ctx)
}

// checkSpotifyDeviceAvailability checks for active Spotify device and handles warnings.
func (d *Dispatcher) checkSpotifyDeviceAvailability(ctx context.Context) bool {
	hasActiveDevice, err := d.spotify.HasActiveDevice(ctx)
	if err != nil {
		d.logger.Warn("Failed to check for active Spotify device, skipping queue management", zap.Error(err))
		return false
	}

	if !hasActiveDevice {
		d.logger.Debug("No active Spotify device found, skipping queue management")
		d.sendDeviceWarningIfNeeded(ctx)
		return false
	}

	// Device is active - clear any existing device warning
	d.warningManager.ClearWarning(ctx, WarningTypeDevice)
	return true
}

// sendDeviceWarningIfNeeded sends a warning to admins if no device is active.
func (d *Dispatcher) sendDeviceWarningIfNeeded(ctx context.Context) {
	if !d.warningManager.ShouldSendWarning(WarningTypeDevice) {
		return
	}

	groupID := d.getGroupID()
	if groupID == "" {
		return
	}

	adminUserIDs, err := d.frontend.GetAdminUserIDs(ctx, groupID)
	if err != nil {
		d.logger.Warn("Failed to get admin user IDs for device warning", zap.Error(err))
		return
	}

	if len(adminUserIDs) == 0 {
		return
	}

	deviceWarningMessage := d.localizer.T("admin.no_active_device")
	if err := d.warningManager.SendWarningToAdmins(ctx, WarningTypeDevice,
		adminUserIDs, deviceWarningMessage); err != nil {
		d.logger.Warn("Failed to send device warning", zap.Error(err))
	}
}

// acquireQueueManagementLock attempts to acquire the queue management lock.
func (d *Dispatcher) acquireQueueManagementLock() bool {
	d.queueManagementMutex.Lock()
	defer d.queueManagementMutex.Unlock()

	if d.queueManagementActive {
		d.logger.Debug("Queue management already active, skipping queue management")
		return false
	}

	d.queueManagementActive = true
	return true
}

// performQueueManagement handles the core queue management logic.
func (d *Dispatcher) performQueueManagement(ctx context.Context) {
	targetDuration := d.calculateTargetQueueDuration()
	d.logger.Debug("Target queue duration calculated",
		zap.Duration("targetDuration", targetDuration))

	currentDuration, err := d.GetQueueRemainingDurationWithShadow(ctx)
	if err != nil {
		d.logger.Warn("Failed to get current queue duration, skipping queue management", zap.Error(err))
		return
	}

	d.logger.Debug("Current queue duration",
		zap.Duration("currentDuration", currentDuration),
		zap.Duration("targetDuration", targetDuration))

	if currentDuration >= targetDuration {
		d.logger.Debug("Queue duration sufficient, no action needed")
		return
	}

	updatedDuration := d.tryFillFromPlaylistTracks(ctx, targetDuration, currentDuration)
	if updatedDuration < targetDuration {
		d.fillQueueToTargetDuration(ctx, targetDuration, updatedDuration)
	}
}

// calculateTargetQueueDuration calculates the target queue duration including approval overhead.
func (d *Dispatcher) calculateTargetQueueDuration() time.Duration {
	baseDuration := time.Duration(d.config.App.QueueAheadDurationSecs) * time.Second

	// Approval overhead: single approval timeout (we only need buffer for one approval at a time)
	approvalOverhead := time.Duration(d.config.App.QueueTrackApprovalTimeoutSecs) * time.Second

	targetDuration := baseDuration + approvalOverhead

	d.logger.Debug("Calculated target queue duration",
		zap.Duration("baseDuration", baseDuration),
		zap.Duration("approvalOverhead", approvalOverhead),
		zap.Duration("targetDuration", targetDuration))

	return targetDuration
}

// tryFillFromPlaylistTracks attempts to fill the queue with tracks from the existing playlist
// Returns the updated duration after adding playlist tracks (may still be < targetDuration).
func (d *Dispatcher) tryFillFromPlaylistTracks(ctx context.Context, targetDuration,
	currentDuration time.Duration) time.Duration {
	d.logger.Debug("tryFillFromPlaylistTracks called",
		zap.Duration("targetDuration", targetDuration),
		zap.Duration("currentDuration", currentDuration))

	// Calculate how much duration we need to add
	neededDuration := targetDuration - currentDuration
	if neededDuration <= 0 {
		d.logger.Debug("No additional tracks needed based on duration calculation")
		return currentDuration
	}

	nextTracks, err := d.getNextPlaylistTracks(ctx)
	if err != nil || len(nextTracks) == 0 {
		return currentDuration
	}

	// Add tracks one by one until we reach target duration
	var addedDuration time.Duration
	successCount := 0

	d.logger.Info("Adding playlist tracks to queue until target duration reached",
		zap.Duration("neededDuration", neededDuration),
		zap.Int("availableTracks", len(nextTracks)))

	for _, track := range nextTracks {
		if currentDuration+addedDuration >= targetDuration {
			d.logger.Debug("Target duration reached, stopping playlist track addition")
			break
		}

		// Skip if track is already in shadow queue (already queued but not yet played)
		if d.GetShadowQueuePosition(track.ID) >= 0 {
			d.logger.Debug("Skipping track already in shadow queue",
				zap.String("trackID", track.ID),
				zap.String("artist", track.Artist),
				zap.String("title", track.Title))
			continue
		}

		if err := d.AddToQueueWithShadowTracking(ctx, &track, sourcePlaylist); err != nil {
			d.logger.Warn("Failed to add track to queue",
				zap.String("trackID", track.ID), zap.Error(err))
			continue
		}

		addedDuration += track.Duration
		successCount++
		d.logger.Debug("Added playlist track to queue",
			zap.String("trackID", track.ID),
			zap.Duration("trackDuration", track.Duration),
			zap.Duration("totalAddedDuration", addedDuration))
	}

	if successCount > 0 {
		d.logger.Info("Successfully added playlist tracks to queue",
			zap.Int("tracksAdded", successCount),
			zap.Duration("addedDuration", addedDuration))
	}

	finalDuration := currentDuration + addedDuration
	d.logger.Debug("Playlist track addition completed",
		zap.Duration("finalDuration", finalDuration),
		zap.Duration("targetDuration", targetDuration),
		zap.Bool("stillNeedsMore", finalDuration < targetDuration))

	return finalDuration
}

// getNextPlaylistTracks retrieves the next tracks from the playlist based on current position.
func (d *Dispatcher) getNextPlaylistTracks(ctx context.Context) ([]Track, error) {
	// Get logical playlist position to ensure correct progression after priority songs
	logicalPosition, err := d.getLogicalPlaylistPosition(ctx)
	if err != nil {
		d.logger.Warn("Failed to get logical playlist position", zap.Error(err))
		return nil, err
	}

	if logicalPosition == nil {
		d.logger.Debug("Current track not found in playlist, cannot determine position for queue filling")
		return nil, errors.New("current track not found in playlist")
	}

	// Get ALL available next tracks from playlist (up to reasonable limit) using logical position
	nextTracks, err := d.spotify.GetNextPlaylistTracksFromPosition(ctx, *logicalPosition, maxTracksToFetch)
	if err != nil {
		d.logger.Warn("Failed to get next playlist tracks", zap.Error(err))
		return nil, err
	}

	if len(nextTracks) == 0 {
		d.logger.Debug("No more tracks available after current track in playlist")
		return nil, errors.New("no tracks available")
	}

	return nextTracks, nil
}

// fillQueueToTargetDuration adds tracks to reach the target queue duration if insufficient.
func (d *Dispatcher) fillQueueToTargetDuration(ctx context.Context, targetDuration, currentDuration time.Duration) {
	if currentDuration >= targetDuration {
		d.logger.Debug("Queue duration sufficient")
		return
	}

	neededDuration := targetDuration - currentDuration
	d.logger.Debug("Queue still needs more duration, adding tracks to fill gap",
		zap.Duration("neededDuration", neededDuration),
		zap.Duration("currentDuration", currentDuration),
		zap.Duration("targetDuration", targetDuration))

	d.logger.Info("Need to add tracks to fill queue duration",
		zap.Duration("neededDuration", neededDuration))

	// Create a new flow for this queue filling operation
	flow := d.createQueueManagementFlow("fill")

	// Check if we've exceeded the rejection limit for auto-approval
	autoApprove := flow.RejectionCount >= d.config.App.MaxQueueTrackReplacements

	// Always use the unified approval workflow
	trackID, searchQuery, newTrackMood, err := d.spotify.GetRecommendedTrack(ctx)
	if err != nil {
		d.logger.Warn("Failed to get queue-filling track", zap.Error(err))
		return
	}

	// Get track info for the approval message
	track, trackErr := d.spotify.GetTrack(ctx, trackID)
	if trackErr != nil {
		d.logger.Warn("Could not get track info for queue-filling track approval", zap.Error(trackErr))
		track = &Track{Title: unknownTrack, Artist: unknownArtist, URL: ""}
	}

	// Track this queue-filling track for approval (DO NOT add to queue/playlist yet in auto-approve case)
	trackName := fmt.Sprintf("%s - %s", track.Artist, track.Title)

	// Add track to flow-specific registry
	d.queueManagementMutex.Lock()
	flow.PendingTracks[trackID] = trackName
	d.queueManagementMutex.Unlock()

	// Send approval message to group (will auto-approve if rejection limit exceeded)
	messageKey := getQueueApprovalMessageKey("bot.queue_management", autoApprove)
	d.sendQueueTrackApprovalMessage(ctx, trackID, track, messageKey, "queue management track",
		autoApprove, searchQuery, newTrackMood)

	if autoApprove {
		d.logger.Info("Auto-approving queue-filling track after rejection limit",
			zap.String("trackID", trackID),
			zap.String("trackName", trackName),
			zap.String("flowID", flow.FlowID),
			zap.Int("rejectionCount", flow.RejectionCount),
			zap.Int("maxRejections", d.config.App.MaxQueueTrackReplacements))
	} else {
		d.logger.Info("Requesting approval for queue-filling track",
			zap.String("trackID", trackID),
			zap.String("trackName", trackName),
			zap.String("flowID", flow.FlowID),
			zap.Int("currentRejections", flow.RejectionCount),
			zap.Duration("stillNeeded", targetDuration-currentDuration))
	}
}

// addApprovedQueueTrack adds an approved queue track to the playlist.
// The track will be queued by the queue manager when it detects the playlist update.
func (d *Dispatcher) addApprovedQueueTrack(ctx context.Context, trackID string) error {
	// Get track details for logging.
	track, err := d.spotify.GetTrack(ctx, trackID)
	if err != nil {
		d.logger.Error("Failed to get track details for approved queue track",
			zap.String("trackID", trackID),
			zap.Error(err))
		return err
	}

	// Add the approved track to playlist and wake up queue manager.
	// The queue manager will pick it up and add it to the queue via tryFillFromPlaylistTracks.
	if err := d.addToPlaylistAndWakeQueueManager(ctx, trackID); err != nil {
		d.logger.Error("Failed to add approved queue track to playlist",
			zap.String("trackID", trackID),
			zap.Error(err))
		return err
	}

	d.logger.Info("Approved queue track added to playlist",
		zap.String("trackID", trackID),
		zap.String("artist", track.Artist),
		zap.String("title", track.Title))

	// Find and remove the flow that handled this track
	d.queueManagementMutex.Lock()
	var flowToRemove *QueueManagementFlow
	for _, flow := range d.queueManagementFlows {
		if _, exists := flow.PendingTracks[trackID]; exists {
			flowToRemove = flow
			break
		}
	}
	d.queueManagementMutex.Unlock()

	if flowToRemove != nil {
		d.removeQueueManagementFlow(flowToRemove.FlowID)
		d.logger.Debug("Flow completed successfully - track approved",
			zap.String("flowID", flowToRemove.FlowID),
			zap.String("trackID", trackID))
	}

	// Queue workflow is complete, reset the flag
	d.resetQueueManagementFlag()

	d.logger.Info("Queue track approved and added successfully",
		zap.String("trackID", trackID))
	return nil
}

// handleQueueTrackDecision handles admin decisions on queue track suggestions.
func (d *Dispatcher) handleQueueTrackDecision(ctx context.Context, trackID string, approved bool) {
	flow, trackName, exists, messageToCancel := d.cleanupTrackFromFlow(trackID)
	if !exists {
		d.logger.Warn("Received queue decision for unknown track", zap.String("trackID", trackID))
		return
	}

	d.logCanceledTimeout(trackID, messageToCancel)

	if approved {
		d.handleApprovedQueueTrack(ctx, trackID, trackName)
	} else {
		d.handleRejectedQueueTrack(ctx, trackID, trackName, flow)
	}
}

// cleanupTrackFromFlow removes the track from its flow and cancels any pending timeouts.
func (d *Dispatcher) cleanupTrackFromFlow(trackID string) (
	flow *QueueManagementFlow, trackName string, exists bool, messageToCancel string,
) {
	d.queueManagementMutex.Lock()
	defer d.queueManagementMutex.Unlock()

	// Find and clean up the track from its flow
	for _, f := range d.queueManagementFlows {
		name, trackExists := f.PendingTracks[trackID]
		if !trackExists {
			continue
		}
		delete(f.PendingTracks, trackID)
		flow = f
		trackName = name
		exists = true
		break
	}

	// Cancel any pending timeout for this track
	for messageID, approvalCtx := range d.pendingApprovalMessages {
		if approvalCtx.trackID == trackID {
			approvalCtx.cancelFunc() // Cancel the timeout
			delete(d.pendingApprovalMessages, messageID)
			messageToCancel = messageID
			break
		}
	}

	return
}

// logCanceledTimeout logs if a timeout was canceled.
func (d *Dispatcher) logCanceledTimeout(trackID, messageToCancel string) {
	if messageToCancel != "" {
		d.logger.Debug("Canceled queue approval timeout",
			zap.String("trackID", trackID),
			zap.String("messageID", messageToCancel))
	}
}

// handleApprovedQueueTrack handles the logic for an approved queue track.
func (d *Dispatcher) handleApprovedQueueTrack(ctx context.Context, trackID, trackName string) {
	d.logger.Info("Queue track approved, adding to queue and playlist",
		zap.String("trackID", trackID),
		zap.String("trackName", trackName))

	if err := d.addApprovedQueueTrack(ctx, trackID); err != nil {
		d.logger.Error("Failed to add manually approved queue track",
			zap.String("trackID", trackID),
			zap.Error(err))
	}
}

// handleRejectedQueueTrack handles the logic for a rejected queue track.
func (d *Dispatcher) handleRejectedQueueTrack(ctx context.Context, trackID, trackName string,
	flow *QueueManagementFlow) {
	d.logger.Info("Queue track denied, will not be added",
		zap.String("trackID", trackID),
		zap.String("trackName", trackName))

	if flow == nil {
		d.logger.Warn("Could not find flow for rejected track", zap.String("trackID", trackID))
		return
	}

	currentCount := d.incrementFlowRejectionCount(flow)
	d.logger.Info("Track rejected, incremented flow rejection counter",
		zap.String("flowID", flow.FlowID),
		zap.Int("rejectionCount", currentCount),
		zap.Int("maxRejections", d.config.App.MaxQueueTrackReplacements))

	// Track was never added to anything, so nothing to remove
	// Try to get a new queue-filling track to replace the denied one
	go d.findAndSuggestReplacementTrack(ctx, flow)
}

// incrementFlowRejectionCount safely increments the rejection count for a flow.
func (d *Dispatcher) incrementFlowRejectionCount(flow *QueueManagementFlow) int {
	d.queueManagementMutex.Lock()
	defer d.queueManagementMutex.Unlock()

	flow.RejectionCount++
	return flow.RejectionCount
}

// resetQueueManagementFlag resets the queue management active flag.
func (d *Dispatcher) resetQueueManagementFlag() {
	d.queueManagementMutex.Lock()
	d.queueManagementActive = false
	d.queueManagementMutex.Unlock()
}

// findAndSuggestReplacementTrack finds a suitable track and suggests it to admin for queue addition.
func (d *Dispatcher) findAndSuggestReplacementTrack(ctx context.Context, flow *QueueManagementFlow) {
	d.logger.Debug("Finding replacement track for queue", zap.String("flowID", flow.FlowID))

	// Check if we've exceeded the rejection limit for auto-approval
	d.queueManagementMutex.RLock()
	rejectionCount := flow.RejectionCount
	d.queueManagementMutex.RUnlock()

	autoApprove := rejectionCount >= d.config.App.MaxQueueTrackReplacements

	// Always use the unified approval workflow
	newTrackID, newSearchQuery, newTrackMood, err := d.spotify.GetRecommendedTrack(ctx)
	if err != nil {
		d.logger.Warn("Failed to get replacement queue track", zap.Error(err))
		d.resetQueueManagementFlag()
		return
	}

	// Get track info for the replacement message
	track, err := d.spotify.GetTrack(ctx, newTrackID)
	if err != nil {
		d.logger.Warn("Could not get track info for replacement queue track", zap.Error(err))
		track = &Track{Title: unknownTrack, Artist: unknownArtist, URL: ""}
	}

	// Track this replacement for approval (DO NOT add to playlist yet)
	trackName := fmt.Sprintf("%s - %s", track.Artist, track.Title)

	// Add track to flow-specific registry
	d.queueManagementMutex.Lock()
	flow.PendingTracks[newTrackID] = trackName
	d.queueManagementMutex.Unlock()

	// Send message to chat about the replacement track (will auto-approve if rejection limit exceeded)
	messageKey := getQueueApprovalMessageKey("bot.queue_replacement", autoApprove)
	d.sendQueueTrackApprovalMessage(ctx, newTrackID, track, messageKey, "replacement queue track",
		autoApprove, newSearchQuery, newTrackMood)

	if autoApprove {
		d.logger.Info("Auto-approving replacement track after rejection limit",
			zap.String("trackID", newTrackID),
			zap.String("trackName", trackName),
			zap.String("flowID", flow.FlowID),
			zap.Int("rejectionCount", rejectionCount),
			zap.Int("maxRejections", d.config.App.MaxQueueTrackReplacements))
	} else {
		d.logger.Info("Requesting approval for replacement track",
			zap.String("trackID", newTrackID),
			zap.String("trackName", trackName),
			zap.String("flowID", flow.FlowID),
			zap.Int("currentRejections", rejectionCount))
	}
}

// Flow management helper functions

// createQueueManagementFlow creates a new queue management flow with a unique ID.
func (d *Dispatcher) createQueueManagementFlow(flowType string) *QueueManagementFlow {
	flowID := fmt.Sprintf("%s-%d", flowType, time.Now().UnixNano())
	flow := &QueueManagementFlow{
		FlowID:         flowID,
		RejectionCount: 0,
		PendingTracks:  make(map[string]string),
		CreatedAt:      time.Now(),
	}

	d.queueManagementMutex.Lock()
	d.queueManagementFlows[flowID] = flow
	d.queueManagementMutex.Unlock()

	d.logger.Debug("Created new queue management flow", zap.String("flowID", flowID))
	return flow
}

// removeQueueManagementFlow removes a flow from the registry.
func (d *Dispatcher) removeQueueManagementFlow(flowID string) {
	d.queueManagementMutex.Lock()
	_, exists := d.queueManagementFlows[flowID]
	if exists {
		delete(d.queueManagementFlows, flowID)
	}
	d.queueManagementMutex.Unlock()

	d.logger.Debug("Removed queue management flow",
		zap.String("flowID", flowID),
		zap.Bool("existed", exists))
}
