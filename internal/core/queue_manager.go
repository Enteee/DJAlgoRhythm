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
// priority queue management, queue filling, and prevention track logic

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

	d.executePlaylistAdd(ctx, msgCtx, originalMsg, trackID)
}

// executePriorityQueue adds priority track to queue and playlist
func (d *Dispatcher) executePriorityQueue(ctx context.Context, msgCtx *MessageContext, originalMsg *chat.Message, trackID string) {
	msgCtx.State = StateAddToPlaylist

	// Add priority track to queue (will play next)
	queueErr := d.AddToQueueWithShadowTracking(ctx, trackID, "priority")
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

	// Track this as a priority track for queue position calculation
	d.queuePositionMutex.Lock()
	d.priorityTracks[trackID] = true
	d.queuePositionMutex.Unlock()

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

// executePlaylistAdd performs the actual playlist addition
func (d *Dispatcher) executePlaylistAdd(ctx context.Context, msgCtx *MessageContext, originalMsg *chat.Message, trackID string) {
	d.executePlaylistAddWithReaction(ctx, msgCtx, originalMsg, trackID, false)
}

// executePlaylistAddWithReaction performs the actual playlist addition
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

	// Check if current track has changed and update shadow queue if needed
	d.checkCurrentTrackChanged(ctx)

	// Check if queue prevention is already active
	d.queueManagementMutex.Lock()
	if d.queueManagementActive {
		d.queueManagementMutex.Unlock()
		d.logger.Debug("Queue prevention already active, skipping queue management")
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

	// Add prevention tracks if still insufficient
	if updatedDuration < targetDuration {
		d.addPreventionTracksIfNeeded(ctx, targetDuration, updatedDuration)
	}
}

// calculateTargetQueueDuration calculates the target queue duration including approval overhead
func (d *Dispatcher) calculateTargetQueueDuration() time.Duration {
	baseDuration := time.Duration(d.config.App.QueueAheadDurationSecs) * time.Second

	// Approval overhead: max_replacements * approval_timeout
	approvalOverhead := time.Duration(d.config.App.MaxQueueTrackReplacements) *
		time.Duration(d.config.App.QueueTrackApprovalTimeoutSecs) * time.Second

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

	// Get current track ID
	currentTrackID, err := d.spotify.GetCurrentTrackID(ctx)
	if err != nil {
		d.logger.Debug("No current track playing, cannot fill queue from playlist", zap.Error(err))
		return
	}

	// Get playlist tracks
	playlistTracks, err := d.spotify.GetPlaylistTracks(ctx, d.config.Spotify.PlaylistID)
	if err != nil {
		d.logger.Warn("Failed to get playlist tracks for queue filling", zap.Error(err))
		return
	}

	// Find current track position
	currentPosition := -1
	for i, trackID := range playlistTracks {
		if trackID == currentTrackID {
			currentPosition = i
			break
		}
	}

	if currentPosition == -1 {
		d.logger.Debug("Current track not found in playlist, cannot fill queue from playlist",
			zap.String("currentTrackID", currentTrackID))
		return
	}

	// Get tracks that should come after current track
	nextTracks := playlistTracks[currentPosition+1:]
	if len(nextTracks) == 0 {
		d.logger.Debug("No more tracks after current track in playlist")
		return
	}

	// Calculate how many tracks we need to add
	neededDuration := targetDuration - currentDuration
	estimatedTrackDuration := time.Duration(estimatedTrackDurationMins) * time.Minute
	tracksNeeded := int(neededDuration/estimatedTrackDuration) + 1 // Add 1 for safety

	if tracksNeeded <= 0 {
		d.logger.Debug("No additional tracks needed based on duration calculation")
		return
	}

	// Limit to available tracks
	if tracksNeeded > len(nextTracks) {
		tracksNeeded = len(nextTracks)
	}

	// Add tracks to queue
	tracksToAdd := nextTracks[:tracksNeeded]
	if len(tracksToAdd) > 0 {
		d.logger.Info("Adding playlist tracks to queue",
			zap.Int("tracksToAdd", len(tracksToAdd)),
			zap.Duration("neededDuration", neededDuration))

		err := d.AddMultipleToQueueWithShadowTracking(ctx, tracksToAdd, "playlist")
		if err != nil {
			d.logger.Warn("Failed to add some tracks to queue from playlist", zap.Error(err))
		} else {
			d.logger.Info("Successfully added playlist tracks to queue",
				zap.Int("tracksAdded", len(tracksToAdd)))
		}
	}
}

// addPreventionTracksIfNeeded adds prevention tracks if queue duration is still insufficient
func (d *Dispatcher) addPreventionTracksIfNeeded(ctx context.Context, targetDuration, currentDuration time.Duration) {
	if currentDuration >= targetDuration {
		d.logger.Debug("Queue duration sufficient after playlist filling")
		return
	}

	neededDuration := targetDuration - currentDuration
	d.logger.Debug("Queue still needs more duration, adding prevention tracks",
		zap.Duration("neededDuration", neededDuration),
		zap.Duration("currentDuration", currentDuration),
		zap.Duration("targetDuration", targetDuration))

	// Calculate how many tracks we might need
	estimatedTrackDuration := time.Duration(estimatedTrackDurationMins) * time.Minute
	maxTracksNeeded := int(neededDuration/estimatedTrackDuration) + 1

	// Limit to configuration maximum
	if maxTracksNeeded > d.config.App.MaxQueueTrackReplacements {
		maxTracksNeeded = d.config.App.MaxQueueTrackReplacements
	}

	d.logger.Info("Need to add prevention tracks to queue",
		zap.Duration("neededDuration", neededDuration),
		zap.Int("maxTracksNeeded", maxTracksNeeded))

	// Add prevention tracks directly (simplified approach)
	for i := 0; i < maxTracksNeeded; i++ {
		// Get a prevention track
		trackID, err := d.spotify.GetQueueManagementTrack(ctx)
		if err != nil {
			d.logger.Warn("Failed to get queue prevention track", zap.Error(err))
			continue
		}

		// Add to queue immediately
		if queueErr := d.AddToQueueWithShadowTracking(ctx, trackID, "prevention"); queueErr != nil {
			d.logger.Warn("Failed to add prevention track to queue", zap.Error(queueErr))
			continue
		}
		d.logger.Info("Added prevention track to queue",
			zap.String("trackID", trackID))

		// Add to dedup store and playlist
		d.dedup.Add(trackID)
		if err := d.spotify.AddToPlaylist(ctx, d.config.Spotify.PlaylistID, trackID); err != nil {
			d.logger.Warn("Failed to add prevention track to playlist", zap.Error(err))
		}
	}
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
		d.logger.Info("Queue track approved",
			zap.String("trackID", trackID),
			zap.String("trackName", trackName))
		// Queue workflow is complete, reset the flag
		d.resetQueueManagementFlag()
	} else {
		d.logger.Info("Queue track denied, removing from playlist",
			zap.String("trackID", trackID),
			zap.String("trackName", trackName))

		// Remove the denied track from the playlist
		if err := d.spotify.RemoveFromPlaylist(ctx, d.config.Spotify.PlaylistID, trackID); err != nil {
			d.logger.Warn("Failed to remove denied queue track from playlist",
				zap.String("trackID", trackID),
				zap.Error(err))
		} else {
			// Remove from dedup store so it can be added again if requested
			d.dedup.Remove(trackID)
		}

		// Try to get a new queue prevention track to replace the denied one
		go d.findAndSuggestReplacementTrack(ctx)
	}
}

// resetQueueManagementFlag resets the queue management active flag
func (d *Dispatcher) resetQueueManagementFlag() {
	d.queueManagementMutex.Lock()
	d.queueManagementActive = false
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

	// Add the replacement track to the playlist
	if addErr := d.spotify.AddToPlaylist(ctx, d.config.Spotify.PlaylistID, newTrackID); addErr != nil {
		d.logger.Warn("Failed to add replacement queue track", zap.Error(addErr))
		d.resetQueueManagementFlag()
		return
	}

	// Add to dedup store
	d.dedup.Add(newTrackID)

	// Get track info for the replacement message
	track, err := d.spotify.GetTrack(ctx, newTrackID)
	if err != nil {
		d.logger.Warn("Could not get track info for replacement queue track", zap.Error(err))
		track = &Track{Title: unknownTrack, Artist: unknownArtist}
	}

	// Track this replacement for approval
	trackName := fmt.Sprintf("%s - %s", track.Artist, track.Title)
	d.queueManagementMutex.Lock()
	d.pendingQueueTracks[newTrackID] = trackName
	d.queueManagementMutex.Unlock()

	// Send message to chat about the replacement track with approval buttons
	d.sendQueueTrackApprovalMessage(ctx, newTrackID, track, "bot.queue_replacement", "replacement queue track")

	d.logger.Info("Added replacement track for approval",
		zap.String("trackID", newTrackID),
		zap.String("artist", track.Artist),
		zap.String("title", track.Title))
}
