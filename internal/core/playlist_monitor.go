package core

import (
	"context"
	"fmt"
	"time"

	"go.uber.org/zap"
)

// Playlist Monitoring and Compliance
// This module handles playlist monitoring, compliance checking, and warning generation
// It ensures the bot is playing from the correct playlist and maintains proper playback settings

// runPlaylistMonitoring monitors if we're playing from the correct playlist
func (d *Dispatcher) runPlaylistMonitoring(ctx context.Context) {
	d.logger.Info("Starting playlist monitoring")

	ticker := time.NewTicker(playlistCheckInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			d.logger.Info("Playlist monitoring stopped")
			return
		case <-ticker.C:
			d.checkPlaylistCompliance(ctx)
		}
	}
}

// updateLastRegularTrack tracks the currently playing track if it's not a priority track
func (d *Dispatcher) updateLastRegularTrack(ctx context.Context) {
	// Get currently playing track ID
	currentTrackID, err := d.spotify.GetCurrentTrackID(ctx)
	if err != nil {
		// No track playing or error getting track, keep the last known regular track
		d.logger.Debug("No current track or error getting current track", zap.Error(err))
		return
	}

	d.logger.Debug("updateLastRegularTrack called",
		zap.String("currentTrackID", currentTrackID))

	d.queuePositionMutex.Lock()
	defer d.queuePositionMutex.Unlock()

	// Check if this is a priority track
	if d.priorityTracks[currentTrackID] {
		// This is a priority track, don't update lastRegularTrackID
		d.logger.Debug("Currently playing track is a priority track, not updating last regular track",
			zap.String("currentTrackID", currentTrackID))
		return
	}

	// Check if the currently playing track is in our target playlist
	// This now runs every 30 seconds to reduce API call frequency
	d.logger.Debug("Checking if current track is in target playlist",
		zap.String("currentTrackID", currentTrackID),
		zap.String("playlistID", d.config.Spotify.PlaylistID))

	playlistTracks, err := d.spotify.GetPlaylistTracks(ctx, d.config.Spotify.PlaylistID)
	if err != nil {
		d.logger.Warn("Failed to get playlist tracks for regular track validation",
			zap.String("currentTrackID", currentTrackID),
			zap.String("playlistID", d.config.Spotify.PlaylistID),
			zap.Error(err))
		return
	}

	d.logger.Debug("Retrieved playlist tracks for validation",
		zap.String("playlistID", d.config.Spotify.PlaylistID),
		zap.Int("trackCount", len(playlistTracks)))

	// Check if current track is in the playlist
	trackInPlaylist := false
	for i, trackID := range playlistTracks {
		if trackID == currentTrackID {
			trackInPlaylist = true
			d.logger.Debug("Found current track in playlist",
				zap.String("currentTrackID", currentTrackID),
				zap.Int("position", i))
			break
		}
	}

	if !trackInPlaylist {
		// Currently playing track is not in our target playlist
		d.logger.Warn("Currently playing track is not in target playlist - not updating lastRegularTrackID",
			zap.String("currentTrackID", currentTrackID),
			zap.String("playlistID", d.config.Spotify.PlaylistID),
			zap.Int("playlistTrackCount", len(playlistTracks)))
		return
	}

	// This is a regular track in our playlist, update the last regular track ID
	if d.lastRegularTrackID != currentTrackID {
		d.logger.Debug("Updating last regular track",
			zap.String("previousRegularTrackID", d.lastRegularTrackID),
			zap.String("newRegularTrackID", currentTrackID))
		d.lastRegularTrackID = currentTrackID

		// Clean up old priority tracks that are no longer relevant
		d.cleanupOldPriorityTracks(ctx)
	}
}

// getLastRegularTrackID returns the last non-priority track that was playing
func (d *Dispatcher) getLastRegularTrackID() string {
	d.queuePositionMutex.RLock()
	defer d.queuePositionMutex.RUnlock()
	return d.lastRegularTrackID
}

// cleanupOldPriorityTracks removes priority tracks that are no longer in the current playlist from tracking
func (d *Dispatcher) cleanupOldPriorityTracks(ctx context.Context) {
	// Get current playlist tracks to see which priority tracks are still relevant
	playlistTracks, err := d.spotify.GetPlaylistTracks(ctx, d.config.Spotify.PlaylistID)
	if err != nil {
		d.logger.Debug("Could not get playlist tracks for priority track cleanup", zap.Error(err))
		return
	}

	// Create a set of current playlist tracks for fast lookup
	playlistTrackSet := make(map[string]bool)
	for _, trackID := range playlistTracks {
		playlistTrackSet[trackID] = true
	}

	// Remove priority tracks that are no longer in the playlist
	var removedCount int
	for trackID := range d.priorityTracks {
		if !playlistTrackSet[trackID] {
			delete(d.priorityTracks, trackID)
			removedCount++
		}
	}

	if removedCount > 0 {
		d.logger.Debug("Cleaned up old priority tracks",
			zap.Int("removedCount", removedCount),
			zap.Int("remainingPriorityTracks", len(d.priorityTracks)))
	}
}

// checkPlaylistCompliance checks if we're playing from the correct playlist and sends warnings if needed
func (d *Dispatcher) checkPlaylistCompliance(ctx context.Context) {
	// Update last regular track for queue position calculation
	d.updateLastRegularTrack(ctx)

	// Check comprehensive playback compliance
	compliance, err := d.spotify.CheckPlaybackCompliance(ctx)
	if err != nil {
		d.logger.Debug("Could not check playback compliance", zap.Error(err))
		return
	}

	if compliance.IsOptimalForAutoDJ() {
		// Everything is fine - clear any unconfirmed warning flag and delete warning messages
		d.playlistWarningMutex.Lock()
		if d.hasUnconfirmedWarning {
			d.hasUnconfirmedWarning = false

			// Delete any existing warning messages since issues are resolved
			messagesToDelete := make(map[string]string)
			for messageID, adminUserID := range d.playlistWarningMessages {
				messagesToDelete[messageID] = adminUserID
			}
			d.playlistWarningMessages = make(map[string]string)

			d.logger.Debug("Compliance issues resolved, cleared unconfirmed warning flag and deleting warning messages")

			// Delete messages outside the lock to avoid blocking
			go func() {
				for messageID, adminUserID := range messagesToDelete {
					if deleteErr := d.frontend.DeleteMessage(ctx, adminUserID, messageID); deleteErr != nil {
						d.logger.Debug("Failed to delete resolved playlist warning message",
							zap.String("messageID", messageID),
							zap.String("adminUserID", adminUserID),
							zap.Error(deleteErr))
					}
				}
			}()
		}
		d.playlistWarningMutex.Unlock()
		return
	}

	// Not playing from correct playlist - check if there's already an unconfirmed warning
	d.playlistWarningMutex.RLock()
	hasUnconfirmed := d.hasUnconfirmedWarning
	d.playlistWarningMutex.RUnlock()

	if hasUnconfirmed {
		// Already have an unconfirmed warning, don't send another
		d.logger.Debug("Playback compliance issues detected but unconfirmed warning already exists",
			zap.Strings("issues", compliance.Issues))
		return
	}

	d.logger.Info("Playback compliance issues detected, sending admin warning",
		zap.Strings("issues", compliance.Issues))

	// Get group ID and admin IDs
	groupID := d.getGroupID()
	if groupID == "" {
		d.logger.Warn("No group ID available for playlist warning")
		return
	}

	// Get list of admin user IDs
	adminUserIDs, err := d.frontend.GetAdminUserIDs(ctx, groupID)
	if err != nil {
		d.logger.Warn("Failed to get admin user IDs for playlist warning", zap.Error(err))
		return
	}

	if len(adminUserIDs) == 0 {
		d.logger.Warn("No admin user IDs found for playlist warning")
		return
	}

	// Generate Spotify playlist URL for easy recovery
	playlistURL := fmt.Sprintf("https://open.spotify.com/playlist/%s", d.config.Spotify.PlaylistID)

	// Generate detailed warning message for admins
	detailedMessage := d.generateComplianceWarningMessage(compliance, playlistURL)

	// Check if this is a playlist issue - if so, send message with buttons to admins
	if !compliance.IsCorrectPlaylist {
		// Send detailed message with playlist switch buttons to admins
		d.sendPlaylistWarningWithButtonsToAdmins(ctx, adminUserIDs, detailedMessage)
	} else {
		// For other compliance issues (shuffle/repeat), send regular message to admins
		d.sendComplianceWarningToAdmins(ctx, adminUserIDs, detailedMessage)
	}

	// Update last warning time
	d.playlistWarningMutex.Lock()
	d.hasUnconfirmedWarning = true
	d.playlistWarningMutex.Unlock()

	d.logger.Info("Sent playlist compliance warning message")
}

// generateComplianceWarningMessage creates a detailed warning message based on compliance issues
func (d *Dispatcher) generateComplianceWarningMessage(compliance *PlaybackCompliance, playlistURL string) string {
	var parts []string

	// Add appropriate warning based on specific issues
	if !compliance.IsCorrectPlaylist {
		parts = append(parts, d.buildPlaylistWarningMessage(playlistURL))
	}

	if !compliance.IsCorrectShuffle {
		parts = append(parts, d.localizer.T("bot.shuffle_warning"))
	}

	if !compliance.IsCorrectRepeat {
		parts = append(parts, d.localizer.T("bot.repeat_warning"))
	}

	// If we have multiple issues, combine them; otherwise use the single issue message
	if len(parts) > 1 {
		// Multiple issues - use comprehensive warning
		return d.localizer.T("bot.playback_compliance_warning", playlistURL)
	} else if len(parts) == 1 {
		// Single issue
		return parts[0]
	}

	// Fallback (shouldn't happen)
	nextArtist, nextTitle := d.getNextTrackForWarning()

	// Use available track info, with fallbacks for missing parts
	if nextArtist == "" {
		nextArtist = unknownArtist
	}
	if nextTitle == "" {
		nextTitle = unknownTrack
	}

	return d.localizer.T("bot.playlist_warning", playlistURL, nextArtist, nextTitle)
}

// buildPlaylistWarningMessage creates the playlist warning message
func (d *Dispatcher) buildPlaylistWarningMessage(playlistURL string) string {
	// Get next track information for playlist warning
	nextArtist, nextTitle := d.getNextTrackForWarning()

	// Use fallbacks for missing track info
	if nextArtist == "" {
		nextArtist = unknownArtist
	}
	if nextTitle == "" {
		nextTitle = unknownTrack
	}

	return d.localizer.T("bot.playlist_warning", playlistURL, nextArtist, nextTitle)
}

// getNextTrackForWarning returns the next track that will be played after playlist switch
func (d *Dispatcher) getNextTrackForWarning() (nextArtist, nextTitle string) {
	d.queuePositionMutex.RLock()
	lastTrackID := d.lastRegularTrackID
	d.queuePositionMutex.RUnlock()

	d.logger.Debug("Getting next track info for playlist warning",
		zap.String("lastRegularTrackID", lastTrackID))

	// Get track information
	ctx, cancel := context.WithTimeout(context.Background(), trackInfoTimeoutSecs*time.Second)
	defer cancel()

	// Get playlist tracks
	playlistTracks, err := d.spotify.GetPlaylistTracks(ctx, d.config.Spotify.PlaylistID)
	if err != nil {
		d.logger.Debug("Failed to get playlist tracks for warning", zap.Error(err))
		return "", ""
	}
	if len(playlistTracks) == 0 {
		d.logger.Debug("Playlist is empty, cannot provide track info for warning")
		return "", ""
	}

	// Determine next track using same logic as switchToCorrectPlaylist
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
			nextTrackReason = "next track after lastRegularTrackID"
		}
	}

	// Fallback: use endOfPlaylistThreshold position
	if nextTrackID == "" {
		endPosition := len(playlistTracks) - endOfPlaylistThreshold
		if endPosition < 0 {
			endPosition = 0 // If playlist is shorter than threshold, use first track
		}
		nextTrackID = playlistTracks[endPosition]
		nextTrackReason = "endOfPlaylistThreshold position"
	}

	d.logger.Debug("Selected next track for warning",
		zap.String("nextTrackID", nextTrackID),
		zap.String("reason", nextTrackReason),
		zap.Int("playlistLength", len(playlistTracks)))

	// Get next track info
	nextTrack, err := d.spotify.GetTrack(ctx, nextTrackID)
	if err != nil {
		d.logger.Warn("Failed to get next track info for warning message",
			zap.String("trackID", nextTrackID),
			zap.Error(err))
		return "", ""
	}

	return nextTrack.Artist, nextTrack.Title
}
