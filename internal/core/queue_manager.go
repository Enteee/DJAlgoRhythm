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

	d.executePlaylistAddWithReaction(ctx, msgCtx, originalMsg, trackID, false)
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
		d.executePlaylistAddWithReaction(ctx, msgCtx, originalMsg, trackID, false)
		return
	}

	// Add priority track to queue (will play next)
	queueErr := d.AddToQueueWithShadowTracking(ctx, track, sourcePriority)
	if queueErr != nil {
		d.logger.Warn("Failed to add priority track to queue, proceeding with playlist only",
			zap.String("trackID", trackID),
			zap.Error(queueErr))
		// If queue fails, fall back to regular playlist addition
		d.executePlaylistAddWithReaction(ctx, msgCtx, originalMsg, trackID, false)
		return
	}

	d.logger.Info("Priority track added to queue",
		zap.String("trackID", trackID))

	// Register priority track in the registry for position tracking
	d.priorityTracksMutex.Lock()
	d.priorityTracks[trackID] = true
	d.priorityTracksMutex.Unlock()

	d.logger.Debug("Registered priority track in registry",
		zap.String("trackID", trackID))

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

// executePlaylistAddWithReaction performs the actual playlist addition with appropriate reaction based on approval status
func (d *Dispatcher) executePlaylistAddWithReaction(
	ctx context.Context, msgCtx *MessageContext, originalMsg *chat.Message, trackID string, wasAdminApproved bool) {
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

		if wasAdminApproved {
			d.reactAddedAfterApproval(ctx, msgCtx, originalMsg, trackID)
		} else {
			d.reactAdded(ctx, msgCtx, originalMsg, trackID)
		}
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

	// Fill queue from existing playlist tracks first
	d.fillQueueFromPlaylist(ctx, targetDuration, currentDuration)

	// Check again after playlist filling
	updatedDuration, err := d.GetQueueRemainingDurationWithShadow(ctx)
	if err != nil {
		d.logger.Warn("Failed to get updated queue duration", zap.Error(err))
		updatedDuration = currentDuration // Use previous value as fallback
	}

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

// fillQueueFromPlaylist attempts to fill the queue with tracks from the existing playlist
func (d *Dispatcher) fillQueueFromPlaylist(ctx context.Context, targetDuration, currentDuration time.Duration) {
	d.logger.Debug("fillQueueFromPlaylist called",
		zap.Duration("targetDuration", targetDuration),
		zap.Duration("currentDuration", currentDuration))

	// Calculate how much duration we need to add
	neededDuration := targetDuration - currentDuration
	if neededDuration <= 0 {
		d.logger.Debug("No additional tracks needed based on duration calculation")
		return
	}

	// Get logical playlist position to ensure correct progression after priority songs
	logicalPosition := d.getLogicalPlaylistPosition(ctx)

	// Get ALL available next tracks from playlist (up to reasonable limit) using logical position
	nextTracks, err := d.spotify.GetNextPlaylistTracksFromPosition(ctx, logicalPosition, maxTracksToFetch)
	if err != nil {
		d.logger.Warn("Failed to get next playlist tracks, falling back to queue-fill", zap.Error(err))
		d.fillQueueToTargetDuration(ctx, targetDuration, currentDuration)
		return
	}

	if len(nextTracks) == 0 {
		d.logger.Debug("No more tracks available after current track in playlist, falling back to queue-fill")
		d.fillQueueToTargetDuration(ctx, targetDuration, currentDuration)
		return
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

	// If we ran out of playlist tracks but still need more duration, call fillQueueToTargetDuration
	finalDuration := currentDuration + addedDuration
	if finalDuration < targetDuration {
		d.logger.Debug("Playlist tracks exhausted but still need more duration, falling back to queue-fill",
			zap.Duration("currentDuration", finalDuration),
			zap.Duration("targetDuration", targetDuration))
		d.fillQueueToTargetDuration(ctx, targetDuration, finalDuration)
	}
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

	// Check if we've exceeded the rejection limit and should auto-add without approval
	d.queueManagementMutex.RLock()
	rejectionCount := d.queueRejectionCount
	d.queueManagementMutex.RUnlock()

	if rejectionCount >= d.config.App.MaxQueueTrackReplacements {
		d.logger.Info("Rejection limit exceeded, auto-adding track without approval",
			zap.Int("rejectionCount", rejectionCount),
			zap.Int("maxRejections", d.config.App.MaxQueueTrackReplacements))

		// Get a track and add it directly
		trackID, err := d.spotify.GetQueueManagementTrack(ctx)
		if err != nil {
			d.logger.Warn("Failed to get queue-filling track for auto-add", zap.Error(err))
			return
		}

		// Get track info for logging
		track, trackErr := d.spotify.GetTrack(ctx, trackID)
		if trackErr != nil {
			d.logger.Warn("Could not get track info for auto-add track", zap.Error(trackErr))
			track = &Track{Title: unknownTrack, Artist: unknownArtist, URL: ""}
		}

		// Add track directly without approval
		if queueErr := d.AddToQueueWithShadowTracking(ctx, track, sourceQueueFill); queueErr != nil {
			d.logger.Warn("Failed to auto-add queue track", zap.Error(queueErr))
			return
		}

		if playlistErr := d.spotify.AddToPlaylist(ctx, d.config.Spotify.PlaylistID, trackID); playlistErr != nil {
			d.logger.Warn("Failed to add auto-added track to playlist", zap.Error(playlistErr))
		}

		// Add to dedup store
		d.dedup.Add(trackID)

		// Reset rejection counter after successful auto-add
		d.queueManagementMutex.Lock()
		d.queueRejectionCount = 0
		d.queueManagementMutex.Unlock()

		d.logger.Info("Auto-added track after rejection limit exceeded",
			zap.String("trackID", trackID),
			zap.String("trackName", fmt.Sprintf("%s - %s", track.Artist, track.Title)))
		return
	}

	// Normal approval workflow - request one track for approval at a time
	trackID, err := d.spotify.GetQueueManagementTrack(ctx)
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

	// Track this queue-filling track for approval (DO NOT add to queue/playlist yet)
	trackName := fmt.Sprintf("%s - %s", track.Artist, track.Title)
	d.queueManagementMutex.Lock()
	d.pendingQueueTracks[trackID] = trackName
	d.queueManagementMutex.Unlock()

	// Send approval message to group (track will be added only after approval)
	d.sendQueueTrackApprovalMessage(ctx, trackID, track, "bot.queue_management", "queue management track")

	d.logger.Info("Requesting approval for queue-filling track",
		zap.String("trackID", trackID),
		zap.String("trackName", trackName),
		zap.Int("currentRejections", rejectionCount),
		zap.Duration("stillNeeded", targetDuration-currentDuration))
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

		// Get track details before adding to queue
		track, err := d.spotify.GetTrack(ctx, trackID)
		if err != nil {
			d.logger.Error("Failed to get track details for approved queue track",
				zap.String("trackID", trackID),
				zap.Error(err))
			return
		}

		// Now actually add the approved track to queue and playlist
		if queueErr := d.AddToQueueWithShadowTracking(ctx, track, sourceQueueFill); queueErr != nil {
			d.logger.Warn("Failed to add approved queue track to queue",
				zap.String("trackID", trackID),
				zap.Error(queueErr))
		} else {
			d.logger.Info("Added approved track to queue", zap.String("trackID", trackID))
		}

		if playlistErr := d.spotify.AddToPlaylist(ctx, d.config.Spotify.PlaylistID, trackID); playlistErr != nil {
			d.logger.Warn("Failed to add approved queue track to playlist",
				zap.String("trackID", trackID),
				zap.Error(playlistErr))
		} else {
			d.logger.Info("Added approved track to playlist", zap.String("trackID", trackID))
		}

		// Add to dedup store
		d.dedup.Add(trackID)

		// Reset rejection counter on approval
		d.queueManagementMutex.Lock()
		d.queueRejectionCount = 0
		d.queueManagementMutex.Unlock()

		// Queue workflow is complete, reset the flag
		d.resetQueueManagementFlag()

		d.logger.Info("Track approved and added, rejection counter reset")
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

	// Get a replacement track directly from Spotify's queue management
	newTrackID, err := d.spotify.GetQueueManagementTrack(ctx)
	if err != nil {
		d.logger.Warn("Failed to get replacement queue track", zap.Error(err))
		d.resetQueueManagementFlag()
		return
	}

	// Get track info for the replacement message (before adding anything)
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

	// Send message to chat about the replacement track with approval buttons
	d.sendQueueTrackApprovalMessage(ctx, newTrackID, track, "bot.queue_replacement", "replacement queue track")

	d.logger.Info("Requesting approval for replacement track",
		zap.String("trackID", newTrackID),
		zap.String("artist", track.Artist),
		zap.String("title", track.Title))
}
