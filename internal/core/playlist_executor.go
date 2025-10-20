package core

import (
	"context"
	"fmt"
	"time"

	"go.uber.org/zap"
)

// Playlist Execution and Control
// This module handles playlist switching, volume fading, and playlist state management
// It provides smooth transitions when switching playlists and manages playback control

// loadPlaylistSnapshot loads existing tracks from the playlist
func (d *Dispatcher) loadPlaylistSnapshot(ctx context.Context) error {
	trackIDs, err := d.spotify.GetPlaylistTracks(ctx, d.config.Spotify.PlaylistID)
	if err != nil {
		return err
	}

	d.dedup.Load(trackIDs)
	d.logger.Info("Loaded playlist snapshot", zap.Int("tracks", len(trackIDs)))
	return nil
}

// fadeVolumeOut gradually reduces volume to 0 over fade duration
func (d *Dispatcher) fadeVolumeOut(ctx context.Context, originalVolume int) {
	for step := fadeSteps; step >= 0; step-- {
		targetVolume := (originalVolume * step) / fadeSteps
		if err := d.spotify.SetVolume(ctx, targetVolume); err != nil {
			d.logger.Warn("Failed to set volume during fade out",
				zap.Int("targetVolume", targetVolume),
				zap.Error(err))
			// Continue despite errors
		}

		if step > 0 {
			time.Sleep(time.Duration(fadeStepDurationMs) * time.Millisecond)
		}
	}
}

// fadeVolumeIn gradually increases volume from 0 to target over fade duration
func (d *Dispatcher) fadeVolumeIn(ctx context.Context, targetVolume int) {
	for step := 0; step <= fadeSteps; step++ {
		currentVolume := (targetVolume * step) / fadeSteps
		if err := d.spotify.SetVolume(ctx, currentVolume); err != nil {
			d.logger.Warn("Failed to set volume during fade in",
				zap.Int("currentVolume", currentVolume),
				zap.Error(err))
			// Continue despite errors
		}

		if step < fadeSteps {
			time.Sleep(time.Duration(fadeStepDurationMs) * time.Millisecond)
		}
	}
}

// switchToCorrectPlaylist performs the complete playlist switch with fade
func (d *Dispatcher) switchToCorrectPlaylist(ctx context.Context) error {
	// Get the last regular track (if any)
	d.queuePositionMutex.RLock()
	lastTrackID := d.lastRegularTrackID
	d.queuePositionMutex.RUnlock()

	// Get current volume
	originalVolume, err := d.spotify.GetCurrentVolume(ctx)
	if err != nil {
		d.logger.Warn("Failed to get current volume, using default", zap.Error(err))
		originalVolume = 50 // Default volume
	}

	d.logger.Info("Starting playlist switch with fade",
		zap.Int("originalVolume", originalVolume),
		zap.String("lastRegularTrack", lastTrackID))

	// Fade volume out
	d.fadeVolumeOut(ctx, originalVolume)

	// Get playlist tracks to find the next track
	playlistTracks, err := d.spotify.GetPlaylistTracks(ctx, d.config.Spotify.PlaylistID)
	if err != nil {
		return fmt.Errorf("failed to get playlist tracks: %w", err)
	}

	if len(playlistTracks) == 0 {
		return fmt.Errorf("playlist is empty")
	}

	// Determine the next track to play
	var nextTrackID string
	var nextTrackReason string

	if lastTrackID != "" {
		// Find the position of the last regular track
		lastTrackPosition := -1
		for i, trackID := range playlistTracks {
			if trackID == lastTrackID {
				lastTrackPosition = i
				break
			}
		}

		// If we found the last track and there's a next track after it
		if lastTrackPosition >= 0 && lastTrackPosition+1 < len(playlistTracks) {
			nextTrackID = playlistTracks[lastTrackPosition+1]
			nextTrackReason = fmt.Sprintf("next track after lastRegularTrackID (position %d)", lastTrackPosition)
		}
	}

	// Fallback: play towards the end of the playlist at EndOfPlaylistThreshold
	if nextTrackID == "" {
		// Calculate position near the end (EndOfPlaylistThreshold tracks from end)
		endPosition := len(playlistTracks) - endOfPlaylistThreshold
		if endPosition < 0 {
			endPosition = 0 // If playlist is shorter than threshold, start from beginning
		}
		nextTrackID = playlistTracks[endPosition]
		if lastTrackID == "" {
			nextTrackReason = fmt.Sprintf("no lastRegularTrackID, resuming at EndOfPlaylistThreshold (position %d)", endPosition)
		} else {
			nextTrackReason = fmt.Sprintf("no next track after lastRegularTrackID, resuming at EndOfPlaylistThreshold (position %d)", endPosition)
		}
	}

	d.logger.Info("Selected track for playlist switch",
		zap.String("nextTrackID", nextTrackID),
		zap.String("reason", nextTrackReason),
		zap.Int("playlistLength", len(playlistTracks)))

	// Set playlist context and start playing the track (this does both operations)
	if err := d.spotify.SetPlaylistContext(ctx, d.config.Spotify.PlaylistID, nextTrackID); err != nil {
		return fmt.Errorf("failed to set playlist context and play track %s: %w", nextTrackID, err)
	}

	// Small delay to ensure playback starts
	time.Sleep(time.Duration(playbackStartDelayMs) * time.Millisecond)

	// Fade volume back in
	d.fadeVolumeIn(ctx, originalVolume)

	d.logger.Info("Successfully switched to correct playlist",
		zap.String("nextTrackID", nextTrackID),
		zap.Int("restoredVolume", originalVolume))

	return nil
}

// handlePlaylistSwitchDecision processes playlist switch approval/denial
func (d *Dispatcher) handlePlaylistSwitchDecision(approved bool) {
	ctx := context.Background()

	// Delete all playlist warning messages and clear the unconfirmed warning flag since admin has responded
	d.playlistWarningMutex.Lock()
	d.hasUnconfirmedWarning = false
	messagesToDelete := make(map[string]string)
	for messageID, adminUserID := range d.playlistWarningMessages {
		messagesToDelete[messageID] = adminUserID
	}
	// Clear the map
	d.playlistWarningMessages = make(map[string]string)
	d.playlistWarningMutex.Unlock()

	// Delete all warning messages
	for messageID, adminUserID := range messagesToDelete {
		if err := d.frontend.DeleteMessage(ctx, adminUserID, messageID); err != nil {
			d.logger.Warn("Failed to delete playlist warning message",
				zap.String("messageID", messageID),
				zap.String("adminUserID", adminUserID),
				zap.Error(err))
		} else {
			d.logger.Debug("Deleted playlist warning message",
				zap.String("messageID", messageID),
				zap.String("adminUserID", adminUserID))
		}
	}

	groupID := d.getGroupID()

	if approved {
		d.logger.Info("Admin approved playlist switch, initiating switch")

		// Get track info for success message (before switch, so we can show what will be played)
		nextArtist, nextTitle := d.getNextTrackForWarning()
		var successMsg string
		if nextArtist != "" && nextTitle != "" {
			successMsg = d.localizer.T("callback.playlist_switched", nextArtist, nextTitle)
		} else {
			successMsg = d.localizer.T("callback.playlist_switched", "Unknown", "Track")
		}

		// Send success message to admins immediately (before the actual switch)
		adminIDs, err := d.frontend.GetAdminUserIDs(ctx, groupID)
		if err != nil {
			d.logger.Warn("Failed to get admin user IDs for switch notification", zap.Error(err))
		} else {
			for _, adminID := range adminIDs {
				if err := d.frontend.SendDirectMessage(ctx, adminID, successMsg); err != nil {
					d.logger.Warn("Failed to send switch success message to admin",
						zap.String("adminID", adminID),
						zap.Error(err))
				}
			}
		}

		// Now perform the playlist switch with fade
		if err := d.switchToCorrectPlaylist(ctx); err != nil {
			d.logger.Error("Failed to switch to correct playlist", zap.Error(err))

			// Send error message
			errorMsg := d.localizer.T("error.generic")
			if _, sendErr := d.frontend.SendText(ctx, groupID, "", errorMsg); sendErr != nil {
				d.logger.Warn("Failed to send switch error message", zap.Error(sendErr))
			}
			return
		}
	} else {
		d.logger.Info("Admin denied playlist switch")

		// Send "staying current" message to admins instead of the group
		stayMsg := d.localizer.T("callback.playlist_stay")
		adminIDs, err := d.frontend.GetAdminUserIDs(ctx, groupID)
		if err != nil {
			d.logger.Warn("Failed to get admin user IDs for stay notification", zap.Error(err))
		} else {
			for _, adminID := range adminIDs {
				if err := d.frontend.SendDirectMessage(ctx, adminID, stayMsg); err != nil {
					d.logger.Warn("Failed to send stay message to admin",
						zap.String("adminID", adminID),
						zap.Error(err))
				}
			}
		}
	}
}
