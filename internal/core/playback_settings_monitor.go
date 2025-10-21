package core

import (
	"context"
	"time"

	"go.uber.org/zap"
)

// Playback Settings Monitoring
// This module handles monitoring of Spotify playback settings (shuffle/repeat)
// and warns admins when settings are not optimal for auto-DJing

// runPlaybackSettingsMonitoring monitors playback settings compliance
func (d *Dispatcher) runPlaybackSettingsMonitoring(ctx context.Context) {
	d.logger.Info("Starting playback settings monitoring")

	ticker := time.NewTicker(playbackSettingsCheckInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			d.logger.Info("Playback settings monitoring stopped")
			return
		case <-ticker.C:
			d.checkPlaybackSettingsCompliance(ctx)
		}
	}
}

// checkPlaybackSettingsCompliance checks playback settings and sends warnings if needed
func (d *Dispatcher) checkPlaybackSettingsCompliance(ctx context.Context) {
	// Check playback settings compliance
	compliance, err := d.spotify.CheckPlaybackCompliance(ctx)
	if err != nil {
		d.logger.Debug("Could not check playback compliance", zap.Error(err))
		return
	}

	if compliance.IsOptimalForAutoDJ() {
		// Settings are optimal - clear any unconfirmed warning flag
		d.settingsWarningMutex.Lock()
		if d.hasUnconfirmedSettingsWarning {
			d.hasUnconfirmedSettingsWarning = false
			d.logger.Debug("Playback settings compliance issues resolved, cleared unconfirmed warning flag")
		}
		d.settingsWarningMutex.Unlock()
		return
	}

	// Settings issues detected - attempt auto-correction
	d.logger.Info("Playback settings compliance issues detected, attempting auto-correction",
		zap.Strings("issues", compliance.Issues))

	corrected := d.attemptPlaybackSettingsCorrection(ctx, compliance)
	if corrected {
		d.logger.Info("Successfully auto-corrected playback settings")
		return
	}
	// Auto-correction failed, fall back to warning
	d.logger.Warn("Auto-correction failed, falling back to admin warning")

	// Check if there's already an unconfirmed warning to avoid spam
	d.settingsWarningMutex.RLock()
	hasUnconfirmed := d.hasUnconfirmedSettingsWarning
	d.settingsWarningMutex.RUnlock()

	if hasUnconfirmed {
		// Already have an unconfirmed warning, don't send another
		d.logger.Debug("Playback settings compliance issues detected but unconfirmed warning already exists",
			zap.Strings("issues", compliance.Issues))
		return
	}

	d.logger.Info("Playback settings compliance issues detected, sending admin warning",
		zap.Strings("issues", compliance.Issues))

	// Get group ID and admin IDs
	groupID := d.getGroupID()
	if groupID == "" {
		d.logger.Warn("No group ID available for settings warning")
		return
	}

	// Get list of admin user IDs
	adminUserIDs, err := d.frontend.GetAdminUserIDs(ctx, groupID)
	if err != nil {
		d.logger.Warn("Failed to get admin user IDs for settings warning", zap.Error(err))
		return
	}

	if len(adminUserIDs) == 0 {
		d.logger.Warn("No admin user IDs found for settings warning")
		return
	}

	// Generate settings warning message for admins
	detailedMessage := d.generatePlaybackSettingsWarningMessage(compliance)

	// Send settings warning to admins
	d.sendPlaybackSettingsWarningToAdmins(ctx, adminUserIDs, detailedMessage)

	// Update warning state
	d.settingsWarningMutex.Lock()
	d.hasUnconfirmedSettingsWarning = true
	d.settingsWarningMutex.Unlock()

	d.logger.Info("Sent playback settings compliance warning message")
}

// attemptPlaybackSettingsCorrection tries to automatically fix playback settings
// Returns true if all settings were successfully corrected, false otherwise
func (d *Dispatcher) attemptPlaybackSettingsCorrection(ctx context.Context, compliance *PlaybackCompliance) bool {
	correctionSuccess := true

	// Fix shuffle setting if incorrect
	if !compliance.IsCorrectShuffle {
		d.logger.Info("Attempting to disable shuffle for auto-DJing")
		if err := d.spotify.SetShuffle(ctx, false); err != nil {
			d.logger.Error("Failed to disable shuffle", zap.Error(err))
			correctionSuccess = false
		} else {
			d.logger.Info("Successfully disabled shuffle")
		}
	}

	// Fix repeat setting if incorrect
	if !compliance.IsCorrectRepeat {
		d.logger.Info("Attempting to set repeat to off for auto-DJing")
		if err := d.spotify.SetRepeat(ctx, "off"); err != nil {
			d.logger.Error("Failed to set repeat to off", zap.Error(err))
			correctionSuccess = false
		} else {
			d.logger.Info("Successfully set repeat to off")
		}
	}

	return correctionSuccess
}

// generatePlaybackSettingsWarningMessage creates a warning message based on settings issues
func (d *Dispatcher) generatePlaybackSettingsWarningMessage(compliance *PlaybackCompliance) string {
	var parts []string

	// Add appropriate warning based on specific issues
	if !compliance.IsCorrectShuffle {
		parts = append(parts, d.localizer.T("bot.shuffle_warning"))
	}

	if !compliance.IsCorrectRepeat {
		parts = append(parts, d.localizer.T("bot.repeat_warning"))
	}

	// If we have multiple issues, combine them; otherwise use the single issue message
	if len(parts) > 1 {
		// Multiple issues - use comprehensive warning
		return d.localizer.T("bot.playback_settings_warning")
	} else if len(parts) == 1 {
		// Single issue
		return parts[0]
	}

	// Fallback (shouldn't happen as this function should only be called when there are issues)
	return d.localizer.T("bot.playback_settings_warning")
}

// sendPlaybackSettingsWarningToAdmins sends settings warning messages to all admin users via DM
func (d *Dispatcher) sendPlaybackSettingsWarningToAdmins(ctx context.Context, adminUserIDs []string, message string) {
	successCount := 0
	var errors []string
	for _, adminUserID := range adminUserIDs {
		if _, err := d.frontend.SendDirectMessage(ctx, adminUserID, message); err != nil {
			d.logger.Warn("Failed to send playback settings warning to admin",
				zap.String("adminUserID", adminUserID),
				zap.Error(err))
			errors = append(errors, err.Error())
		} else {
			successCount++
			d.logger.Debug("Successfully sent playback settings warning to admin",
				zap.String("adminUserID", adminUserID))
		}
	}

	d.logger.Info("Sent playback settings warning to admins",
		zap.Int("successCount", successCount),
		zap.Int("totalAdmins", len(adminUserIDs)),
		zap.Strings("errors", errors))
}
