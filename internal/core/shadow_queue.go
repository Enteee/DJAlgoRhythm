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
		URI:      fmt.Sprintf("spotify:track:%s", trackID),
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
			// Current track not in shadow queue - user manually played a non-queued track
			// Keep shadow queue intact as queued tracks will resume playing after manual track
			d.logger.Debug("Current track not in shadow queue, keeping shadow queue intact",
				zap.String("currentTrackID", currentTrackID),
				zap.Int("shadowQueueSize", len(d.shadowQueue)),
				zap.String("reason", "manual track will finish and queued tracks will resume"))
		} else if currentTrackPosition == 0 {
			// Normal progression: current track was at position 0, remove it
			d.logger.Debug("Normal track progression, removing completed track",
				zap.String("completedTrackID", d.shadowQueue[0].TrackID),
				zap.String("source", d.shadowQueue[0].Source))

			d.shadowQueue = d.shadowQueue[1:]
			for i := range d.shadowQueue {
				d.shadowQueue[i].Position = i
			}
		} else {
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

		d.logger.Debug("Shadow queue updated after track change",
			zap.Int("remainingItems", len(d.shadowQueue)))
		d.shadowQueueMutex.Unlock()
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
