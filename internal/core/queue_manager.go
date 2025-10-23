package core

import (
	"context"
	"fmt"
	"time"

	"go.uber.org/zap"

	"whatdj/internal/chat"
)

// Queue Management and Track Addition
// This module handles all queue-related operations including playlist addition,
// priority queue management, queue filling, and queue-fill track logic

// addToPlaylist adds a track to the Spotify playlist or queue based on priority
func (d *Dispatcher) addToPlaylist(ctx context.Context, msgCtx *MessageContext, originalMsg *chat.Message, trackID string) {
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

	// If it's a priority request from an admin, add to queue for priority playback
	if isAdmin && isPriority {
		d.executePriorityQueue(ctx, msgCtx, originalMsg, trackID)
		return
	}

	// Check if admin approval is required
	// If AdminNeedsApproval is enabled, even admins need approval
	// Otherwise, only non-admins need approval when AdminApproval is enabled
	needsApproval := d.isAdminApprovalRequired() && (!isAdmin || d.isAdminNeedsApproval())
	if needsApproval {
		d.awaitAdminApproval(ctx, msgCtx, originalMsg, trackID)
		return
	}

	d.executePlaylistAddWithReaction(ctx, msgCtx, originalMsg, trackID)
}

// executePriorityQueue adds priority track to queue and playlist
func (d *Dispatcher) executePriorityQueue(ctx context.Context, msgCtx *MessageContext, originalMsg *chat.Message, trackID string) {
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
	for retry := 0; retry < d.config.App.MaxRetries; retry++ {
		if err := d.spotify.AddToPlaylistAtPosition(ctx, d.config.Spotify.PlaylistID, trackID, 0); err != nil {
			d.logger.Error("Failed to add priority track to playlist",
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
		d.reactPriorityQueued(ctx, msgCtx, originalMsg, trackID)
		return
	}
}

// executePlaylistAddWithReaction performs the actual playlist addition with appropriate reaction
func (d *Dispatcher) executePlaylistAddWithReaction(
	ctx context.Context, msgCtx *MessageContext, originalMsg *chat.Message, trackID string) {
	msgCtx.State = StateAddToPlaylist

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
		d.reactAdded(ctx, msgCtx, originalMsg, trackID)
		return
	}
}

// runQueueAndPlaylistManagement manages queue duration and automatic track filling
func (d *Dispatcher) runQueueAndPlaylistManagement(ctx context.Context) {
	d.logger.Info("Starting queue and playlist management",
		zap.Duration("interval", time.Duration(d.config.App.QueueCheckIntervalSecs)*time.Second),
		zap.Duration("targetDuration", time.Duration(d.config.App.QueueAheadDurationSecs)*time.Second))

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
		}
	}
}

// checkAndManageQueue implements the unified queue and playlist management logic
func (d *Dispatcher) checkAndManageQueue(ctx context.Context) {
	d.logger.Debug("checkAndManageQueue called")

	// Check if there's an active Spotify device before proceeding
	hasActiveDevice, err := d.spotify.HasActiveDevice(ctx)
	if err != nil {
		d.logger.Warn("Failed to check for active Spotify device, skipping queue management", zap.Error(err))
		return
	} else if !hasActiveDevice {
		d.logger.Debug("No active Spotify device found, skipping queue management")

		// Send warning to admins if no warning is already active
		if d.warningManager.ShouldSendWarning(WarningTypeDevice) {
			groupID := d.getGroupID()
			if groupID != "" {
				adminUserIDs, adminErr := d.frontend.GetAdminUserIDs(ctx, groupID)
				if adminErr != nil {
					d.logger.Warn("Failed to get admin user IDs for device warning", zap.Error(adminErr))
				} else if len(adminUserIDs) > 0 {
					deviceWarningMessage := d.localizer.T("admin.no_active_device")
					if warningErr := d.warningManager.SendWarningToAdmins(ctx, WarningTypeDevice, adminUserIDs, deviceWarningMessage); warningErr != nil {
						d.logger.Warn("Failed to send device warning", zap.Error(warningErr))
					}
				}
			}
		}
		return
	}

	// Device is active - clear any existing device warning
	d.warningManager.ClearWarning(ctx, WarningTypeDevice)

	// Check if queue management is already active
	d.queueManagementMutex.Lock()
	if d.queueManagementActive {
		d.queueManagementMutex.Unlock()
		d.logger.Debug("Queue management already active, skipping queue management")
		return
	}
	d.queueManagementActive = true
	d.queueManagementMutex.Unlock()

	defer func() {
		// Always reset the flag when done (successful or failed)
		d.resetQueueManagementFlag()
	}()

	// Calculate target queue duration with approval overhead
	targetDuration := d.calculateTargetQueueDuration()
	d.logger.Debug("Target queue duration calculated",
		zap.Duration("targetDuration", targetDuration))

	// Get current queue remaining duration
	currentDuration, err := d.GetQueueRemainingDurationWithShadow(ctx)
	if err != nil {
		d.logger.Warn("Failed to get current queue duration, skipping queue management", zap.Error(err))
		return
	}

	d.logger.Debug("Current queue duration",
		zap.Duration("currentDuration", currentDuration),
		zap.Duration("targetDuration", targetDuration))

	// Check if we have sufficient queue duration
	if currentDuration >= targetDuration {
		d.logger.Debug("Queue duration sufficient, no action needed")
		return
	}

	// Try to fill queue from existing playlist tracks first
	updatedDuration := d.tryFillFromPlaylistTracks(ctx, targetDuration, currentDuration)

	// Fill queue to target duration if still insufficient
	if updatedDuration < targetDuration {
		d.fillQueueToTargetDuration(ctx, targetDuration, updatedDuration)
	}
}

// calculateTargetQueueDuration calculates the target queue duration including approval overhead
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
// Returns the updated duration after adding playlist tracks (may still be < targetDuration)
func (d *Dispatcher) tryFillFromPlaylistTracks(ctx context.Context, targetDuration, currentDuration time.Duration) time.Duration {
	d.logger.Debug("tryFillFromPlaylistTracks called",
		zap.Duration("targetDuration", targetDuration),
		zap.Duration("currentDuration", currentDuration))

	// Calculate how much duration we need to add
	neededDuration := targetDuration - currentDuration
	if neededDuration <= 0 {
		d.logger.Debug("No additional tracks needed based on duration calculation")
		return currentDuration
	}

	// Get logical playlist position to ensure correct progression after priority songs
	logicalPosition, err := d.getLogicalPlaylistPosition(ctx)
	if err != nil {
		d.logger.Warn("Failed to get logical playlist position", zap.Error(err))
		return currentDuration
	}
	if logicalPosition == nil {
		d.logger.Debug("Current track not found in playlist, cannot determine position for queue filling")
		return currentDuration
	}

	// Get ALL available next tracks from playlist (up to reasonable limit) using logical position
	nextTracks, err := d.spotify.GetNextPlaylistTracksFromPosition(ctx, *logicalPosition, maxTracksToFetch)
	if err != nil {
		d.logger.Warn("Failed to get next playlist tracks", zap.Error(err))
		return currentDuration
	}

	if len(nextTracks) == 0 {
		d.logger.Debug("No more tracks available after current track in playlist")
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

// fillQueueToTargetDuration adds tracks to reach the target queue duration if insufficient
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

	// Check if we've exceeded the rejection limit for auto-approval
	d.queueManagementMutex.RLock()
	rejectionCount := d.queueRejectionCount
	d.queueManagementMutex.RUnlock()

	autoApprove := rejectionCount >= d.config.App.MaxQueueTrackReplacements

	// Always use the unified approval workflow
	trackID, err := d.spotify.GetRecommendedTrack(ctx)
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
	d.queueManagementMutex.Lock()
	d.pendingQueueTracks[trackID] = trackName
	d.queueManagementMutex.Unlock()

	// Send approval message to group (will auto-approve if rejection limit exceeded)
	messageKey := getQueueApprovalMessageKey("bot.queue_management", autoApprove)
	d.sendQueueTrackApprovalMessage(ctx, trackID, track, messageKey, "queue management track", autoApprove)

	if autoApprove {
		d.logger.Info("Auto-approving queue-filling track after rejection limit",
			zap.String("trackID", trackID),
			zap.String("trackName", trackName),
			zap.Int("rejectionCount", rejectionCount),
			zap.Int("maxRejections", d.config.App.MaxQueueTrackReplacements))
	} else {
		d.logger.Info("Requesting approval for queue-filling track",
			zap.String("trackID", trackID),
			zap.String("trackName", trackName),
			zap.Int("currentRejections", rejectionCount),
			zap.Duration("stillNeeded", targetDuration-currentDuration))
	}
}

// addApprovedQueueTrack adds an approved queue track to both queue and playlist
func (d *Dispatcher) addApprovedQueueTrack(ctx context.Context, trackID string) error {
	// Get track details before adding to queue
	track, err := d.spotify.GetTrack(ctx, trackID)
	if err != nil {
		d.logger.Error("Failed to get track details for approved queue track",
			zap.String("trackID", trackID),
			zap.Error(err))
		return err
	}

	// Add the approved track to queue and playlist
	if queueErr := d.AddToQueueWithShadowTracking(ctx, track, sourceQueueFill); queueErr != nil {
		d.logger.Warn("Failed to add approved queue track to queue",
			zap.String("trackID", trackID),
			zap.Error(queueErr))
	}

	if playlistErr := d.spotify.AddToPlaylist(ctx, d.config.Spotify.PlaylistID, trackID); playlistErr != nil {
		d.logger.Warn("Failed to add approved queue track to playlist",
			zap.String("trackID", trackID),
			zap.Error(playlistErr))
	}

	// Add to dedup store
	d.dedup.Add(trackID)

	// Reset rejection counter on approval
	d.queueManagementMutex.Lock()
	d.queueRejectionCount = 0
	d.queueManagementMutex.Unlock()

	// Queue workflow is complete, reset the flag
	d.resetQueueManagementFlag()

	d.logger.Info("Queue track approved and added successfully",
		zap.String("trackID", trackID))
	return nil
}

// handleQueueTrackDecision handles admin decisions on queue track suggestions
func (d *Dispatcher) handleQueueTrackDecision(trackID string, approved bool) {
	ctx := context.Background()

	d.queueManagementMutex.Lock()
	trackName, exists := d.pendingQueueTracks[trackID]
	if exists {
		delete(d.pendingQueueTracks, trackID)
	}

	// Cancel any pending timeout for this track
	var messageToCancel string
	for messageID, approvalCtx := range d.pendingApprovalMessages {
		if approvalCtx.trackID == trackID {
			approvalCtx.cancelFunc() // Cancel the timeout
			delete(d.pendingApprovalMessages, messageID)
			messageToCancel = messageID
			break
		}
	}
	d.queueManagementMutex.Unlock()

	if !exists {
		d.logger.Warn("Received queue decision for unknown track", zap.String("trackID", trackID))
		return
	}

	if messageToCancel != "" {
		d.logger.Debug("Canceled queue approval timeout",
			zap.String("trackID", trackID),
			zap.String("messageID", messageToCancel))
	}

	if approved {
		d.logger.Info("Queue track approved, adding to queue and playlist",
			zap.String("trackID", trackID),
			zap.String("trackName", trackName))

		// Add the approved track using shared logic
		if err := d.addApprovedQueueTrack(ctx, trackID); err != nil {
			d.logger.Error("Failed to add manually approved queue track",
				zap.String("trackID", trackID),
				zap.Error(err))
		}
	} else {
		d.logger.Info("Queue track denied, will not be added",
			zap.String("trackID", trackID),
			zap.String("trackName", trackName))

		// Increment rejection counter
		d.queueManagementMutex.Lock()
		d.queueRejectionCount++
		currentCount := d.queueRejectionCount
		d.queueManagementMutex.Unlock()

		d.logger.Info("Track rejected, incremented rejection counter",
			zap.Int("rejectionCount", currentCount),
			zap.Int("maxRejections", d.config.App.MaxQueueTrackReplacements))

		// Track was never added to anything, so nothing to remove
		// Try to get a new queue-filling track to replace the denied one
		go d.findAndSuggestReplacementTrack(ctx)
	}
}

// resetQueueManagementFlag resets the queue management active flag and rejection counter
func (d *Dispatcher) resetQueueManagementFlag() {
	d.queueManagementMutex.Lock()
	d.queueManagementActive = false
	d.queueRejectionCount = 0 // Reset rejection counter when queue management cycle completes
	d.queueManagementMutex.Unlock()
}

// findAndSuggestReplacementTrack finds a suitable track and suggests it to admin for queue addition
func (d *Dispatcher) findAndSuggestReplacementTrack(ctx context.Context) {
	d.logger.Debug("Finding replacement track for queue")

	// Check if we've exceeded the rejection limit for auto-approval
	d.queueManagementMutex.RLock()
	rejectionCount := d.queueRejectionCount
	d.queueManagementMutex.RUnlock()

	autoApprove := rejectionCount >= d.config.App.MaxQueueTrackReplacements

	// Always use the unified approval workflow
	newTrackID, err := d.spotify.GetRecommendedTrack(ctx)
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
	d.queueManagementMutex.Lock()
	d.pendingQueueTracks[newTrackID] = trackName
	d.queueManagementMutex.Unlock()

	// Send message to chat about the replacement track (will auto-approve if rejection limit exceeded)
	messageKey := getQueueApprovalMessageKey("bot.queue_replacement", autoApprove)
	d.sendQueueTrackApprovalMessage(ctx, newTrackID, track, messageKey, "replacement queue track", autoApprove)

	if autoApprove {
		d.logger.Info("Auto-approving replacement track after rejection limit",
			zap.String("trackID", newTrackID),
			zap.String("trackName", trackName),
			zap.Int("rejectionCount", rejectionCount),
			zap.Int("maxRejections", d.config.App.MaxQueueTrackReplacements))
	} else {
		d.logger.Info("Requesting approval for replacement track",
			zap.String("trackID", newTrackID),
			zap.String("trackName", trackName),
			zap.Int("currentRejections", rejectionCount))
	}
}
