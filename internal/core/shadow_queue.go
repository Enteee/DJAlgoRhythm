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

// populateShadowQueue builds the shadow queue from the current playing track to the end of the playlist
func (d *Dispatcher) populateShadowQueue(ctx context.Context) error {
	d.shadowQueueMutex.Lock()
	defer d.shadowQueueMutex.Unlock()

	d.logger.Debug("Populating shadow queue from current track to playlist end")

	// Get current track
	currentTrackID, err := d.spotify.GetCurrentTrackID(ctx)
	if err != nil {
		d.logger.Debug("No track currently playing, cannot populate shadow queue", zap.Error(err))
		// Clear shadow queue if no track is playing
		d.shadowQueue = d.shadowQueue[:0]
		return nil
	}

	// Check if current track changed - if not, we can skip rebuilding shadow queue
	if currentTrackID == d.lastCurrentTrackID && len(d.shadowQueue) > 0 {
		d.logger.Debug("Current track unchanged, shadow queue still valid",
			zap.String("currentTrackID", currentTrackID),
			zap.Int("shadowQueueLength", len(d.shadowQueue)))
		return nil
	}

	d.logger.Debug("Current track changed or shadow queue empty, rebuilding",
		zap.String("oldTrackID", d.lastCurrentTrackID),
		zap.String("newTrackID", currentTrackID))

	// Update last known current track
	d.lastCurrentTrackID = currentTrackID

	// Get all playlist tracks
	playlistTracks, err := d.spotify.GetPlaylistTracks(ctx, d.config.Spotify.PlaylistID)
	if err != nil {
		return fmt.Errorf("failed to get playlist tracks: %w", err)
	}

	// Find current track position in playlist
	currentPosition := -1
	for i, trackID := range playlistTracks {
		if trackID == currentTrackID {
			currentPosition = i
			break
		}
	}

	if currentPosition == -1 {
		d.logger.Warn("Current track not found in playlist, cannot build shadow queue",
			zap.String("currentTrackID", currentTrackID))
		// Clear shadow queue if current track is not in playlist
		d.shadowQueue = d.shadowQueue[:0]
		return nil
	}

	// Get tracks from current position + 1 to end of playlist
	remainingTracks := playlistTracks[currentPosition+1:]
	if len(remainingTracks) == 0 {
		d.logger.Debug("No remaining tracks in playlist after current track")
		// Clear shadow queue if no tracks remaining
		d.shadowQueue = d.shadowQueue[:0]
		return nil
	}

	// Get durations for all remaining tracks
	totalDuration, err := d.spotify.GetTracksDuration(ctx, remainingTracks)
	var trackDurations []time.Duration
	if err != nil {
		d.logger.Warn("Failed to get track durations for shadow queue, using estimated durations",
			zap.Error(err))
		// Use estimated durations as fallback
		trackDurations = make([]time.Duration, len(remainingTracks))
		for i := range trackDurations {
			trackDurations[i] = time.Duration(estimatedTrackDurationMins) * time.Minute
		}
	} else {
		// Distribute total duration equally among tracks (simplified approach)
		avgDuration := totalDuration / time.Duration(len(remainingTracks))
		trackDurations = make([]time.Duration, len(remainingTracks))
		for i := range trackDurations {
			trackDurations[i] = avgDuration
		}
	}

	// Build new shadow queue
	newShadowQueue := make([]ShadowQueueItem, 0, len(remainingTracks))
	for i, trackID := range remainingTracks {
		var duration time.Duration
		if i < len(trackDurations) {
			duration = trackDurations[i]
		} else {
			duration = time.Duration(estimatedTrackDurationMins) * time.Minute
		}

		item := ShadowQueueItem{
			TrackID:  trackID,
			URI:      fmt.Sprintf("spotify:track:%s", trackID),
			Position: i, // Position in logical queue (0 = next track)
			Duration: duration,
			Source:   "playlist",
			AddedAt:  time.Now(),
		}

		newShadowQueue = append(newShadowQueue, item)
	}

	// Replace shadow queue
	d.shadowQueue = newShadowQueue

	d.logger.Info("Shadow queue populated",
		zap.String("currentTrackID", currentTrackID),
		zap.Int("currentPosition", currentPosition),
		zap.Int("remainingTracks", len(remainingTracks)),
		zap.Int("shadowQueueLength", len(d.shadowQueue)),
		zap.Duration("totalShadowQueueDuration", d.getShadowQueueDurationUnsafe()))

	return nil
}

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

// checkCurrentTrackChanged checks if the current track has changed and triggers shadow queue repopulation if needed
func (d *Dispatcher) checkCurrentTrackChanged(ctx context.Context) {
	currentTrackID, err := d.spotify.GetCurrentTrackID(ctx)
	if err != nil {
		return // Skip if we can't get current track
	}

	d.shadowQueueMutex.RLock()
	lastTrackID := d.lastCurrentTrackID
	d.shadowQueueMutex.RUnlock()

	if currentTrackID != lastTrackID {
		d.logger.Debug("Current track changed, repopulating shadow queue",
			zap.String("oldTrackID", lastTrackID),
			zap.String("newTrackID", currentTrackID))

		if err := d.populateShadowQueue(ctx); err != nil {
			d.logger.Warn("Failed to repopulate shadow queue after track change", zap.Error(err))
		}
	}
}

// AddToQueueWithShadowTracking is an enhanced wrapper around Spotify's AddToQueue that maintains shadow queue state
func (d *Dispatcher) AddToQueueWithShadowTracking(ctx context.Context, trackID, source string) error {
	// Add to Spotify queue first
	if err := d.spotify.AddToQueue(ctx, trackID); err != nil {
		return fmt.Errorf("failed to add track to Spotify queue: %w", err)
	}

	// Get track duration for shadow queue
	track, err := d.spotify.GetTrack(ctx, trackID)
	if err != nil {
		d.logger.Warn("Failed to get track duration for shadow queue, using estimated duration",
			zap.String("trackID", trackID),
			zap.Error(err))
		// Use estimated duration as fallback
		track = &Track{Duration: time.Duration(estimatedTrackDurationMins) * time.Minute}
	}

	// Add to shadow queue
	d.addToShadowQueue(trackID, source, track.Duration)

	return nil
}

// AddMultipleToQueueWithShadowTracking adds multiple tracks to queue with shadow tracking
func (d *Dispatcher) AddMultipleToQueueWithShadowTracking(ctx context.Context, trackIDs []string, source string) error {
	if len(trackIDs) == 0 {
		return nil
	}

	// Get track durations for shadow queue
	totalDuration, err := d.spotify.GetTracksDuration(ctx, trackIDs)
	var trackDurations []time.Duration

	if err != nil {
		d.logger.Warn("Failed to get track durations for shadow queue, using estimated durations",
			zap.Error(err),
			zap.Int("trackCount", len(trackIDs)))
		// Use estimated durations as fallback
		for range trackIDs {
			trackDurations = append(trackDurations, time.Duration(estimatedTrackDurationMins)*time.Minute)
		}
	} else {
		// Distribute total duration equally among tracks (simplified approach)
		avgDuration := totalDuration / time.Duration(len(trackIDs))
		for range trackIDs {
			trackDurations = append(trackDurations, avgDuration)
		}
	}

	// Add each track to both Spotify queue and shadow queue
	for i, trackID := range trackIDs {
		// Add to Spotify queue
		if err := d.spotify.AddToQueue(ctx, trackID); err != nil {
			d.logger.Warn("Failed to add track to Spotify queue, continuing with next track",
				zap.String("trackID", trackID),
				zap.Error(err))
			continue
		}

		// Add to shadow queue
		d.addToShadowQueue(trackID, source, trackDurations[i])
	}

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

// getLastRegularTrackID returns the last regular track ID for position calculations
// A regular track is any track that's not from the priority source

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

// GetPlaylistPositionWithShadow calculates track position using shadow queue first, then playlist fallback
// This is a shadow queue-aware replacement for spotify.GetPlaylistPositionRelativeTo
func (d *Dispatcher) GetPlaylistPositionWithShadow(ctx context.Context, trackID, referenceTrackID string) (int, error) {
	// First, try to find position in shadow queue
	shadowPosition := d.GetShadowQueuePosition(trackID)
	if shadowPosition >= 0 {
		d.logger.Debug("Found track position in shadow queue",
			zap.String("trackID", trackID),
			zap.String("referenceTrackID", referenceTrackID),
			zap.Int("shadowPosition", shadowPosition))
		return shadowPosition, nil
	}

	d.logger.Debug("Track not found in shadow queue, falling back to playlist-based calculation",
		zap.String("trackID", trackID),
		zap.String("referenceTrackID", referenceTrackID))

	// If no reference track is provided, we need current track info or shadow queue data
	if referenceTrackID == "" {
		if len(d.shadowQueue) == 0 {
			// No current track and no shadow queue data - cannot determine position reliably
			d.logger.Debug("No current track and no shadow queue data, cannot determine position",
				zap.String("trackID", trackID))

			return -1, fmt.Errorf("cannot determine track position without current track or shadow queue data")
		}
		// Use current track as reference if available
		referenceTrackID = d.lastCurrentTrackID
	}

	return d.calculatePlaylistPositionFromShadow(ctx, trackID, referenceTrackID)
}

// calculatePlaylistPositionFromShadow calculates position based on playlist tracks and current state
// This provides a fallback when the track is not in the shadow queue
func (d *Dispatcher) calculatePlaylistPositionFromShadow(ctx context.Context, trackID, referenceTrackID string) (int, error) {
	// Get playlist tracks
	playlistTracks, err := d.spotify.GetPlaylistTracks(ctx, d.config.Spotify.PlaylistID)
	if err != nil {
		return -1, fmt.Errorf("failed to get playlist tracks: %w", err)
	}

	// Find both tracks in playlist
	trackPosition := -1
	referencePosition := -1

	for i, pTrackID := range playlistTracks {
		if pTrackID == trackID {
			trackPosition = i
		}
		if pTrackID == referenceTrackID {
			referencePosition = i
		}
	}

	if trackPosition == -1 {
		return -1, fmt.Errorf("track %s not found in playlist", trackID)
	}

	if referenceTrackID == "" || referencePosition == -1 {
		// If no valid reference track, return absolute position in playlist
		return trackPosition, nil
	}

	// Calculate relative position
	relativePosition := trackPosition - referencePosition - 1

	if relativePosition < 0 {
		// Track is before reference track in playlist
		return -1, fmt.Errorf("track %s appears before reference track %s in playlist", trackID, referenceTrackID)
	}

	d.logger.Debug("Calculated playlist position from shadow queue context",
		zap.String("trackID", trackID),
		zap.String("referenceTrackID", referenceTrackID),
		zap.Int("trackPosition", trackPosition),
		zap.Int("referencePosition", referencePosition),
		zap.Int("relativePosition", relativePosition))

	return relativePosition, nil
}

// runShadowQueueMaintenance performs periodic maintenance on the shadow queue
func (d *Dispatcher) runShadowQueueMaintenance(ctx context.Context) {
	d.logger.Info("Starting shadow queue maintenance routine")

	// Use configuration for maintenance interval
	maintenanceInterval := time.Duration(d.config.App.ShadowQueueMaintenanceIntervalMins) * time.Minute
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

// performShadowQueueMaintenance performs cleanup and validation of the shadow queue
func (d *Dispatcher) performShadowQueueMaintenance(ctx context.Context) {
	d.logger.Debug("Performing shadow queue maintenance")

	// Skip maintenance if dispatcher is shutting down
	if ctx.Err() != nil {
		return
	}

	d.shadowQueueMutex.Lock()
	defer d.shadowQueueMutex.Unlock()

	beforeCount := len(d.shadowQueue)

	// Remove old shadow queue items (tracks added long ago that should have played by now)
	maxAge := time.Duration(d.config.App.ShadowQueueMaxAgeHours) * time.Hour
	now := time.Now()
	cleanedQueue := make([]ShadowQueueItem, 0, len(d.shadowQueue))

	for _, item := range d.shadowQueue {
		age := now.Sub(item.AddedAt)
		if age > maxAge {
			d.logger.Debug("Removing old shadow queue item",
				zap.String("trackID", item.TrackID),
				zap.String("source", item.Source),
				zap.Duration("age", age),
				zap.Duration("maxAge", maxAge))
			continue
		}
		cleanedQueue = append(cleanedQueue, item)
	}

	// Update positions for remaining items
	for i := range cleanedQueue {
		cleanedQueue[i].Position = i
	}

	d.shadowQueue = cleanedQueue
	afterCount := len(d.shadowQueue)

	if beforeCount != afterCount {
		d.logger.Debug("Shadow queue maintenance completed",
			zap.Int("removedItems", beforeCount-afterCount),
			zap.Int("remainingItems", afterCount))
	}
}
