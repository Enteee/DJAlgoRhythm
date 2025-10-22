package core

import (
	"context"
	"fmt"
	"time"

	"go.uber.org/zap"
)

// Shadow Queue Management
// This module handles shadow queue operations for reliable queue duration tracking
// The shadow queue maintains our own view of what's in the Spotify queue to avoid
// relying on Spotify's often inaccurate queue duration API

// getShadowQueueDurationUnsafe returns the total duration of the shadow queue (unsafe - requires lock)
func (d *Dispatcher) getShadowQueueDurationUnsafe() time.Duration {
	var totalDuration time.Duration
	for _, item := range d.shadowQueue {
		totalDuration += item.Duration
	}
	return totalDuration
}

// GetShadowQueueDuration returns the total duration of the shadow queue (thread-safe)
func (d *Dispatcher) GetShadowQueueDuration() time.Duration {
	d.shadowQueueMutex.RLock()
	defer d.shadowQueueMutex.RUnlock()
	return d.getShadowQueueDurationUnsafe()
}

// GetShadowQueueSize returns the number of tracks in the shadow queue (thread-safe)
func (d *Dispatcher) GetShadowQueueSize() int {
	d.shadowQueueMutex.RLock()
	defer d.shadowQueueMutex.RUnlock()
	return len(d.shadowQueue)
}

// addToShadowQueue adds a track to the shadow queue with the specified source
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

	d.logger.Debug("Added track to shadow queue",
		zap.String("trackID", trackID),
		zap.String("source", source),
		zap.Int("position", nextPosition),
		zap.Duration("duration", duration))
}

// checkCurrentTrackChanged checks if the current track has changed and updates shadow queue progression
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

// updateShadowQueueProgression handles the shadow queue updates when current track changes
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

	if currentTrackPosition == -1 {
		d.handleManualTrackPlay(currentTrackID)
	} else if currentTrackPosition == 0 {
		d.handleNormalTrackProgression()
	} else {
		d.handleManualTrackSkip(currentTrackID, currentTrackPosition)
	}

	d.logger.Debug("Shadow queue updated after track change",
		zap.Int("remainingItems", len(d.shadowQueue)))
	d.shadowQueueMutex.Unlock()
}

// handleManualTrackPlay handles when user manually plays a non-queued track
func (d *Dispatcher) handleManualTrackPlay(currentTrackID string) {
	// Current track not in shadow queue - user manually played a non-queued track
	// Keep shadow queue intact as queued tracks will resume playing after manual track
	d.logger.Debug("Current track not in shadow queue, keeping shadow queue intact",
		zap.String("currentTrackID", currentTrackID),
		zap.Int("shadowQueueSize", len(d.shadowQueue)),
		zap.String("reason", "manual track will finish and queued tracks will resume"))
}

// handleNormalTrackProgression handles normal track progression (track was at position 0)
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
}

// handleManualTrackSkip handles when user manually skips to a track at position N
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
}

// AddToQueueWithShadowTracking is an enhanced wrapper around Spotify's AddToQueue that maintains shadow queue state
func (d *Dispatcher) AddToQueueWithShadowTracking(ctx context.Context, track *Track, source string) error {
	// Add to Spotify queue first
	if err := d.spotify.AddToQueue(ctx, track.ID); err != nil {
		return fmt.Errorf("failed to add track to Spotify queue: %w", err)
	}

	// Add to shadow queue with the known track duration
	d.addToShadowQueue(track.ID, source, track.Duration)

	return nil
}

// GetQueueRemainingDurationWithShadow provides reliable queue duration using shadow queue data
// When shadow queue is empty, returns only current track remaining time (no Spotify fallback)
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

// GetShadowQueuePosition finds the position of a track in the shadow queue
// Returns -1 if track is not found
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

// getLogicalPlaylistPosition returns the logical playlist position to use for next track selection
func (d *Dispatcher) getLogicalPlaylistPosition(ctx context.Context) int {
	// Get current track ID
	currentTrackID, err := d.spotify.GetCurrentTrackID(ctx)
	if err != nil {
		d.logger.Debug("No current track playing, using position 0")
		return 0
	}

	// Check if current track is a priority track using the registry
	d.priorityTracksMutex.RLock()
	priorityInfo, isPriorityTrack := d.priorityTracks[currentTrackID]
	d.priorityTracksMutex.RUnlock()

	// Get all playlist tracks to find positions
	playlistTrackIDs, err := d.spotify.GetPlaylistTracks(ctx, d.config.Spotify.PlaylistID)
	if err != nil {
		d.logger.Warn("Failed to get playlist tracks for position tracking", zap.Error(err))
		return 0
	}

	if isPriorityTrack {
		// Priority track is playing, find the resume song position
		resumeSongID := priorityInfo.ResumeSongID
		if resumeSongID == "" {
			// No resume song recorded, default to position after current priority track
			for i, trackID := range playlistTrackIDs {
				if trackID == currentTrackID {
					resumePosition := i + 1
					d.logger.Debug("Priority track playing, no resume song, using next position",
						zap.String("currentTrackID", currentTrackID),
						zap.Int("currentPosition", i),
						zap.Int("resumePosition", resumePosition))
					return resumePosition
				}
			}
		} else {
			// Find where the resume song currently is in the playlist
			for i, trackID := range playlistTrackIDs {
				if trackID == resumeSongID {
					d.logger.Debug("Priority track playing, found resume song position",
						zap.String("currentTrackID", currentTrackID),
						zap.String("resumeSongID", resumeSongID),
						zap.Int("resumePosition", i))
					return i
				}
			}
			// Resume song not found in playlist, fall back to normal logic
			d.logger.Debug("Priority track playing, resume song not found in playlist",
				zap.String("currentTrackID", currentTrackID),
				zap.String("resumeSongID", resumeSongID))
		}
	}

	// Normal track or fallback case - find current track position
	for i, trackID := range playlistTrackIDs {
		if trackID == currentTrackID {
			d.logger.Debug("Normal track playing, using current position",
				zap.String("currentTrackID", currentTrackID),
				zap.Int("position", i))
			return i
		}
	}

	d.logger.Debug("Current track not found in playlist, using position 0")
	return 0
}

// runShadowQueueMaintenance performs periodic maintenance on the shadow queue
func (d *Dispatcher) runShadowQueueMaintenance(ctx context.Context) {
	maintenanceInterval := time.Duration(d.config.App.ShadowQueueMaintenanceIntervalSecs) * time.Second

	d.logger.Info("Starting shadow queue maintenance routine",
		zap.Int("intervalSecs", d.config.App.ShadowQueueMaintenanceIntervalSecs),
		zap.Duration("interval", maintenanceInterval))

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

// removeOldShadowQueueItems removes shadow queue items that exceed the maximum age
// This function handles its own mutex locking
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
	return removedCount
}

// removeOldPriorityItems removes priority tracks that are no longer active from the registry
// This function handles its own mutex locking
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

// performShadowQueueMaintenance performs cleanup and validation of the shadow queue
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
}

// synchronizeWithSpotifyQueue synchronizes the shadow queue with the actual Spotify queue state
// This removes tracks from shadow queue that are no longer in the Spotify queue
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

	if beforeSyncCount != afterSyncCount {
		d.logger.Debug("Shadow queue synchronized with Spotify queue",
			zap.Int("removedItems", beforeSyncCount-afterSyncCount),
			zap.Int("remainingItems", afterSyncCount),
			zap.Int("spotifyQueueItems", len(spotifyTrackIDs)))
	}
}
