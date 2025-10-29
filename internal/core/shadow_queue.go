package core

import (
	"context"
	"fmt"
	"strings"
	"time"

	"go.uber.org/zap"
)

// Queue sync detection constants.
const (
	ConsecutiveRemovalThreshold = 3 // Threshold for consecutive removals to trigger sync warning
	MaxTracksInWarningMessage   = 5 // Maximum tracks to show in warning message
)

// Shadow Queue Management.
// This module handles shadow queue operations for reliable queue duration tracking.
// The shadow queue maintains our own view of what's in the Spotify queue to avoid
// relying on Spotify's often inaccurate queue duration API.

// getShadowQueueDurationUnsafe returns the total duration of the shadow queue (unsafe - requires lock).
func (d *Dispatcher) getShadowQueueDurationUnsafe() time.Duration {
	var totalDuration time.Duration
	for _, item := range d.shadowQueue {
		totalDuration += item.Duration
	}
	return totalDuration
}

// GetShadowQueueDuration returns the total duration of the shadow queue (thread-safe).
func (d *Dispatcher) GetShadowQueueDuration() time.Duration {
	d.shadowQueueMutex.RLock()
	defer d.shadowQueueMutex.RUnlock()
	return d.getShadowQueueDurationUnsafe()
}

// GetShadowQueueSize returns the number of tracks in the shadow queue (thread-safe).
func (d *Dispatcher) GetShadowQueueSize() int {
	d.shadowQueueMutex.RLock()
	defer d.shadowQueueMutex.RUnlock()
	return len(d.shadowQueue)
}

// addToShadowQueue adds a track to the shadow queue with the specified source.
func (d *Dispatcher) addToShadowQueue(trackID, source string, duration time.Duration) {
	d.shadowQueueMutex.Lock()
	defer d.shadowQueueMutex.Unlock()

	// Assign next position in queue
	nextPosition := len(d.shadowQueue)

	item := ShadowQueueItem{
		TrackID:  trackID,
		Position: nextPosition,
		Duration: duration,
		Source:   source,
		AddedAt:  time.Now(),
	}

	d.shadowQueue = append(d.shadowQueue, item)

	// Update modification timestamp
	d.lastShadowQueueModified = time.Now()

	d.logger.Debug("Added track to shadow queue",
		zap.String("trackID", trackID),
		zap.String("source", source),
		zap.Int("position", nextPosition),
		zap.Duration("duration", duration))
}

// checkCurrentTrackChanged checks if the current track has changed and updates shadow queue progression.
func (d *Dispatcher) checkCurrentTrackChanged(ctx context.Context) {
	currentTrackID, err := d.spotify.GetCurrentTrackID(ctx)
	if err != nil {
		return // Skip if we can't get current track
	}

	d.shadowQueueMutex.RLock()
	lastTrackID := d.lastCurrentTrackID
	d.shadowQueueMutex.RUnlock()

	if currentTrackID != lastTrackID {
		d.updateShadowQueueProgression(currentTrackID, lastTrackID)
	}
}

// updateShadowQueueProgression handles the shadow queue updates when current track changes.
func (d *Dispatcher) updateShadowQueueProgression(currentTrackID, lastTrackID string) {
	d.logger.Debug("Current track changed, updating shadow queue progression",
		zap.String("oldTrackID", lastTrackID),
		zap.String("newTrackID", currentTrackID))

	// Update the last known current track ID
	d.shadowQueueMutex.Lock()
	d.lastCurrentTrackID = currentTrackID

	// Smart track change detection: find where current track is in shadow queue
	currentTrackPosition := -1
	for i, item := range d.shadowQueue {
		if item.TrackID == currentTrackID {
			currentTrackPosition = i
			break
		}
	}

	switch currentTrackPosition {
	case -1:
		d.handleManualTrackPlay(currentTrackID)
	case 0:
		d.handleNormalTrackProgression()
	default:
		d.handleManualTrackSkip(currentTrackID, currentTrackPosition)
	}

	d.logger.Debug("Shadow queue updated after track change",
		zap.Int("remainingItems", len(d.shadowQueue)))
	d.shadowQueueMutex.Unlock()
}

// handleManualTrackPlay handles when user manually plays a non-queued track.
func (d *Dispatcher) handleManualTrackPlay(currentTrackID string) {
	// Current track not in shadow queue - user manually played a non-queued track
	// Keep shadow queue intact as queued tracks will resume playing after manual track
	d.logger.Debug("Current track not in shadow queue, keeping shadow queue intact",
		zap.String("currentTrackID", currentTrackID),
		zap.Int("shadowQueueSize", len(d.shadowQueue)),
		zap.String("reason", "manual track will finish and queued tracks will resume"))
}

// handleNormalTrackProgression handles normal track progression (track was at position 0).
func (d *Dispatcher) handleNormalTrackProgression() {
	// Normal progression: current track was at position 0, remove it
	completedTrack := d.shadowQueue[0]
	d.logger.Debug("Normal track progression, removing completed track",
		zap.String("completedTrackID", completedTrack.TrackID),
		zap.String("source", completedTrack.Source))

	d.shadowQueue = d.shadowQueue[1:]
	for i := range d.shadowQueue {
		d.shadowQueue[i].Position = i
	}

	// Update modification timestamp
	d.lastShadowQueueModified = time.Now()
}

// handleManualTrackSkip handles when user manually skips to a track at position N.
func (d *Dispatcher) handleManualTrackSkip(currentTrackID string, currentTrackPosition int) {
	// Manual skip: current track was at position N, remove all tracks before it
	skippedTracks := d.shadowQueue[:currentTrackPosition]
	d.logger.Debug("Manual track skip detected, removing skipped tracks",
		zap.String("currentTrackID", currentTrackID),
		zap.Int("currentTrackPosition", currentTrackPosition),
		zap.Int("skippedTracksCount", len(skippedTracks)))

	// Log the skipped tracks for debugging
	for _, skipped := range skippedTracks {
		d.logger.Debug("Removing skipped track from shadow queue",
			zap.String("skippedTrackID", skipped.TrackID),
			zap.String("source", skipped.Source))
	}

	// Remove all tracks up to and including current position
	d.shadowQueue = d.shadowQueue[currentTrackPosition+1:]
	for i := range d.shadowQueue {
		d.shadowQueue[i].Position = i
	}

	// Update modification timestamp
	d.lastShadowQueueModified = time.Now()
}

// AddToQueueWithShadowTracking is an enhanced wrapper around Spotify's AddToQueue that maintains shadow queue state.
func (d *Dispatcher) AddToQueueWithShadowTracking(ctx context.Context, track *Track, source string) error {
	// Add to Spotify queue first
	if err := d.spotify.AddToQueue(ctx, track.ID); err != nil {
		return fmt.Errorf("failed to add track to Spotify queue: %w", err)
	}

	// Add to shadow queue with the known track duration
	d.addToShadowQueue(track.ID, source, track.Duration)

	return nil
}

// GetQueueRemainingDurationWithShadow provides reliable queue duration using shadow queue data.
// When shadow queue is empty, returns only current track remaining time (no Spotify fallback).
func (d *Dispatcher) GetQueueRemainingDurationWithShadow(ctx context.Context) (time.Duration, error) {
	// Get shadow queue duration first (more reliable than Spotify API)
	shadowQueueDuration := d.GetShadowQueueDuration()

	// Get current track remaining time to add to shadow queue duration
	currentTrackRemaining, _ := d.spotify.GetCurrentTrackRemainingTime(ctx)

	// If shadow queue has data, prefer it over Spotify API
	if shadowQueueDuration > 0 {
		totalDuration := currentTrackRemaining + shadowQueueDuration

		d.logger.Debug("Using shadow queue for duration calculation",
			zap.Duration("currentTrackRemaining", currentTrackRemaining),
			zap.Duration("shadowQueueDuration", shadowQueueDuration),
			zap.Duration("totalDuration", totalDuration))

		return totalDuration, nil
	}

	// Shadow queue is empty, return only current track remaining time
	// This is more reliable than Spotify's often inaccurate queue duration
	d.logger.Debug("Shadow queue empty, using current track remaining time only",
		zap.Duration("currentTrackRemaining", currentTrackRemaining))

	return currentTrackRemaining, nil
}

// GetShadowQueuePosition finds the position of a track in the shadow queue.
// Returns -1 if track is not found.
func (d *Dispatcher) GetShadowQueuePosition(trackID string) int {
	d.shadowQueueMutex.RLock()
	defer d.shadowQueueMutex.RUnlock()

	for i, item := range d.shadowQueue {
		if item.TrackID == trackID {
			return i
		}
	}

	return -1 // Track not found in shadow queue
}

// getLogicalPlaylistPosition returns the logical playlist position to use for next track selection.
// Returns (position, error). Position is nil if current track is not found in playlist.
func (d *Dispatcher) getLogicalPlaylistPosition(ctx context.Context) (*int, error) {
	// Get current track ID
	currentTrackID, err := d.spotify.GetCurrentTrackID(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get current track ID: %w", err)
	}

	// Check if current track is a priority track using the registry
	d.priorityTracksMutex.RLock()
	priorityInfo, isPriorityTrack := d.priorityTracks[currentTrackID]
	d.priorityTracksMutex.RUnlock()

	// Get all playlist tracks to find positions
	playlistTracks, err := d.spotify.GetPlaylistTracksWithDetails(ctx, d.config.Spotify.PlaylistID)
	if err != nil {
		return nil, fmt.Errorf("failed to get playlist tracks: %w", err)
	}

	if isPriorityTrack {
		if pos := d.getPriorityTrackResumePosition(currentTrackID, &priorityInfo, playlistTracks); pos != nil {
			return pos, nil
		}
		// Fall through to normal logic if resume position not found
	}

	// Normal track or fallback case - find current track position
	for i, track := range playlistTracks {
		if track.ID == currentTrackID {
			d.logger.Debug("Normal track playing, using current position",
				zap.String("currentTrackID", currentTrackID),
				zap.Int("position", i))
			return &i, nil
		}
	}

	// Current track not found in playlist - return nil instead of fallback
	d.logger.Warn("Current track not found in playlist",
		zap.String("currentTrackID", currentTrackID))
	return nil, nil
}

// getPriorityTrackResumePosition finds the resume position when a priority track is playing.
func (d *Dispatcher) getPriorityTrackResumePosition(
	currentTrackID string, priorityInfo *PriorityTrackInfo, playlistTracks []Track) *int {
	resumeSongID := priorityInfo.ResumeSongID
	if resumeSongID == "" {
		// No resume song recorded, default to position after current priority track
		for i, track := range playlistTracks {
			if track.ID == currentTrackID {
				resumePosition := i + 1
				d.logger.Debug("Priority track playing, no resume song, using next position",
					zap.String("currentTrackID", currentTrackID),
					zap.Int("currentPosition", i),
					zap.Int("resumePosition", resumePosition))
				return &resumePosition
			}
		}
		// Current priority track not found in playlist
		d.logger.Warn("Priority track not found in playlist",
			zap.String("currentTrackID", currentTrackID))
		return nil
	}

	// Find where the resume song currently is in the playlist
	for i, track := range playlistTracks {
		if track.ID == resumeSongID {
			d.logger.Debug("Priority track playing, found resume song position",
				zap.String("currentTrackID", currentTrackID),
				zap.String("resumeSongID", resumeSongID),
				zap.Int("resumePosition", i))
			return &i
		}
	}
	// Resume song not found in playlist, fall back to normal logic
	d.logger.Debug("Priority track playing, resume song not found in playlist",
		zap.String("currentTrackID", currentTrackID),
		zap.String("resumeSongID", resumeSongID))
	return nil
}

// checkQueueSyncStatus detects potential queue sync issues and warns admins.
func (d *Dispatcher) checkQueueSyncStatus(ctx context.Context) {
	d.shadowQueueMutex.RLock()
	shadowQueueSize := len(d.shadowQueue)
	lastModified := d.lastShadowQueueModified
	lastSync := d.lastSuccessfulSync
	consecutiveRemovals := d.consecutiveSyncRemovals
	d.shadowQueueMutex.RUnlock()

	// Skip if shadow queue is empty (no sync issue)
	if shadowQueueSize == 0 {
		d.warningManager.ClearWarning(ctx, WarningTypeQueueSync)
		return
	}

	timeoutDuration := time.Duration(d.config.App.QueueSyncWarningTimeoutMinutes) * time.Minute
	timeSinceLastModified := time.Since(lastModified)
	timeSinceLastSync := time.Since(lastSync)

	// Detect sync issues
	shouldWarn := false
	var reason string

	// Case 1: No modifications for a long time but shadow queue has items
	if timeSinceLastModified > timeoutDuration {
		shouldWarn = true
		reason = "no queue activity"
	}

	// Case 2: Consecutive sync operations removing items (persistent desync)
	if consecutiveRemovals >= ConsecutiveRemovalThreshold {
		shouldWarn = true
		reason = "persistent desync detected"
	}

	// Case 3: Shadow queue has items but sync hasn't worked recently
	if timeSinceLastSync > timeoutDuration && shadowQueueSize > 0 {
		shouldWarn = true
		reason = "sync failure"
	}

	if shouldWarn && d.warningManager.ShouldSendWarning(WarningTypeQueueSync) {
		d.logger.Warn("Queue sync issue detected",
			zap.String("reason", reason),
			zap.Int("shadowQueueSize", shadowQueueSize),
			zap.Duration("timeSinceLastModified", timeSinceLastModified),
			zap.Duration("timeSinceLastSync", timeSinceLastSync),
			zap.Int("consecutiveRemovals", consecutiveRemovals))

		d.sendQueueSyncWarning(ctx)
	} else if !shouldWarn {
		// Clear warning if sync issue is resolved
		d.warningManager.ClearWarning(ctx, WarningTypeQueueSync)
	}
}

// sendQueueSyncWarning sends a warning to admins about queue sync issues.
func (d *Dispatcher) sendQueueSyncWarning(ctx context.Context) {
	groupID := d.getGroupID()
	if groupID == "" {
		return
	}

	// Get admin user IDs
	adminUserIDs, err := d.frontend.GetAdminUserIDs(ctx, groupID)
	if err != nil {
		d.logger.Warn("Failed to get admin user IDs for queue sync warning", zap.Error(err))
		return
	}

	if len(adminUserIDs) == 0 {
		d.logger.Debug("No admin users found for queue sync warning")
		return
	}

	// Generate warning message with current queue tracks
	warningMessage := d.generateQueueSyncWarningMessage(ctx)

	// Send warning to admins
	if err := d.warningManager.SendWarningToAdmins(ctx, WarningTypeQueueSync, adminUserIDs, warningMessage); err != nil {
		d.logger.Warn("Failed to send queue sync warning", zap.Error(err))
	}
}

// generateQueueSyncWarningMessage creates a warning message with current queue tracks.
func (d *Dispatcher) generateQueueSyncWarningMessage(ctx context.Context) string {
	d.shadowQueueMutex.RLock()
	shadowQueue := make([]ShadowQueueItem, len(d.shadowQueue))
	copy(shadowQueue, d.shadowQueue)
	d.shadowQueueMutex.RUnlock()

	if len(shadowQueue) == 0 {
		return d.localizer.T("admin.queue_sync_warning", "No tracks currently queued")
	}

	// Build track list with Spotify links
	var trackList strings.Builder
	for i, item := range shadowQueue {
		if i >= MaxTracksInWarningMessage { // Limit to first 5 tracks to avoid very long messages
			remaining := len(shadowQueue) - i
			trackList.WriteString(fmt.Sprintf("... and %d more tracks", remaining))
			break
		}

		// Get track details
		track, err := d.spotify.GetTrack(ctx, item.TrackID)
		if err != nil {
			trackList.WriteString(fmt.Sprintf("• Unknown Track (ID: %s)\n", item.TrackID))
			continue
		}

		// Format track with Spotify link
		trackList.WriteString(fmt.Sprintf("• %s - %s 🔗 %s\n", track.Artist, track.Title, track.URL))
	}

	return d.localizer.T("admin.queue_sync_warning", trackList.String())
}

// runShadowQueueMaintenance performs periodic maintenance on the shadow queue.
func (d *Dispatcher) runShadowQueueMaintenance(ctx context.Context) {
	maintenanceInterval := time.Duration(d.config.App.ShadowQueueMaintenanceIntervalSecs) * time.Second

	d.logger.Info("Starting shadow queue maintenance routine",
		zap.Int("intervalSecs", d.config.App.ShadowQueueMaintenanceIntervalSecs),
		zap.Duration("interval", maintenanceInterval))

	// Run immediately on startup
	d.performShadowQueueMaintenance(ctx)

	ticker := time.NewTicker(maintenanceInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			d.logger.Info("Shadow queue maintenance stopped")
			return
		case <-ticker.C:
			d.performShadowQueueMaintenance(ctx)
		}
	}
}

// removeOldShadowQueueItems removes shadow queue items that exceed the maximum age.
// This function handles its own mutex locking.
func (d *Dispatcher) removeOldShadowQueueItems() int {
	d.shadowQueueMutex.Lock()
	defer d.shadowQueueMutex.Unlock()
	maxAge := time.Duration(d.config.App.ShadowQueueMaxAgeHours) * time.Hour
	now := time.Now()
	cleanedQueue := make([]ShadowQueueItem, 0, len(d.shadowQueue))

	removedCount := 0
	for _, item := range d.shadowQueue {
		age := now.Sub(item.AddedAt)
		if age > maxAge {
			d.logger.Debug("Removing old shadow queue item",
				zap.String("trackID", item.TrackID),
				zap.String("source", item.Source),
				zap.Duration("age", age),
				zap.Duration("maxAge", maxAge))
			removedCount++
			continue
		}
		cleanedQueue = append(cleanedQueue, item)
	}

	// Update positions for remaining items
	for i := range cleanedQueue {
		cleanedQueue[i].Position = i
	}

	d.shadowQueue = cleanedQueue

	// Update modification timestamp if items were removed
	if removedCount > 0 {
		d.lastShadowQueueModified = time.Now()
	}

	return removedCount
}

// removeOldPriorityItems removes priority tracks that are no longer active from the registry.
// This function handles its own mutex locking.
func (d *Dispatcher) removeOldPriorityItems(ctx context.Context) int {
	d.priorityTracksMutex.Lock()
	defer d.priorityTracksMutex.Unlock()

	// Get current track ID to avoid removing currently playing priority track
	currentTrackID, err := d.spotify.GetCurrentTrackID(ctx)
	if err != nil {
		currentTrackID = "" // If we can't get current track, proceed with cleanup
	}

	// Create map of shadow queue track IDs for efficient lookup
	d.shadowQueueMutex.RLock()
	shadowQueueTrackIDs := make(map[string]bool)
	for _, item := range d.shadowQueue {
		shadowQueueTrackIDs[item.TrackID] = true
	}
	d.shadowQueueMutex.RUnlock()

	removedCount := 0
	for trackID := range d.priorityTracks {
		// Keep priority track if it's currently playing OR still in shadow queue
		if trackID == currentTrackID || shadowQueueTrackIDs[trackID] {
			continue
		}

		// Priority track is no longer active, remove it from registry
		delete(d.priorityTracks, trackID)
		removedCount++
		d.logger.Debug("Removed inactive priority track from registry",
			zap.String("trackID", trackID),
			zap.String("reason", "no longer in shadow queue or currently playing"))
	}

	if removedCount > 0 {
		d.logger.Debug("Priority track registry cleanup completed",
			zap.Int("removedCount", removedCount),
			zap.Int("remainingCount", len(d.priorityTracks)))
	}

	return removedCount
}

// performShadowQueueMaintenance performs cleanup and validation of the shadow queue.
func (d *Dispatcher) performShadowQueueMaintenance(ctx context.Context) {
	// Skip maintenance if dispatcher is shutting down
	if ctx.Err() != nil {
		return
	}

	// Check if current track has changed and update shadow queue progression
	d.checkCurrentTrackChanged(ctx)

	// Synchronize shadow queue with actual Spotify queue state
	d.synchronizeWithSpotifyQueue(ctx)

	// Remove old shadow queue items (tracks added long ago that should have played by now)
	d.removeOldShadowQueueItems()

	// Remove old priority items (priority tracks no longer active)
	d.removeOldPriorityItems(ctx)

	// Check for queue sync issues and warn admins if needed
	d.checkQueueSyncStatus(ctx)
}

// synchronizeWithSpotifyQueue synchronizes the shadow queue with the actual Spotify queue state.
// This removes tracks from shadow queue that are no longer in the Spotify queue.
func (d *Dispatcher) synchronizeWithSpotifyQueue(ctx context.Context) {
	d.logger.Debug("Synchronizing shadow queue with Spotify queue state")

	// Get actual Spotify queue state
	queueTrackIDs, err := d.spotify.GetQueueTrackIDs(ctx)
	if err != nil {
		d.logger.Warn("Failed to get Spotify queue for synchronization, skipping queue sync",
			zap.Error(err))
		return
	}

	// Convert track IDs to map for efficient lookup
	spotifyTrackIDs := make(map[string]bool)
	for _, trackID := range queueTrackIDs {
		spotifyTrackIDs[trackID] = true
	}

	d.logger.Debug("Retrieved Spotify queue state for synchronization",
		zap.Int("spotifyQueueItems", len(queueTrackIDs)),
		zap.Int("validTrackIDs", len(spotifyTrackIDs)))

	d.shadowQueueMutex.Lock()
	defer d.shadowQueueMutex.Unlock()

	// Filter shadow queue to only include tracks that exist in Spotify queue
	beforeSyncCount := len(d.shadowQueue)
	syncedQueue := make([]ShadowQueueItem, 0, len(d.shadowQueue))

	for _, item := range d.shadowQueue {
		if spotifyTrackIDs[item.TrackID] {
			syncedQueue = append(syncedQueue, item)
		} else {
			d.logger.Debug("Removing shadow queue item not found in Spotify queue",
				zap.String("trackID", item.TrackID),
				zap.String("source", item.Source),
				zap.Int("originalPosition", item.Position))
		}
	}

	// Update positions for remaining items
	for i := range syncedQueue {
		syncedQueue[i].Position = i
	}

	d.shadowQueue = syncedQueue
	afterSyncCount := len(d.shadowQueue)

	// Update sync tracking
	removedItems := beforeSyncCount - afterSyncCount
	d.lastSuccessfulSync = time.Now()

	if removedItems > 0 {
		d.consecutiveSyncRemovals++
		d.lastShadowQueueModified = time.Now()
		d.logger.Debug("Shadow queue synchronized with Spotify queue",
			zap.Int("removedItems", removedItems),
			zap.Int("remainingItems", afterSyncCount),
			zap.Int("spotifyQueueItems", len(spotifyTrackIDs)),
			zap.Int("consecutiveSyncRemovals", d.consecutiveSyncRemovals))
	} else {
		// Reset consecutive removals counter if no items were removed
		d.consecutiveSyncRemovals = 0
	}
}
