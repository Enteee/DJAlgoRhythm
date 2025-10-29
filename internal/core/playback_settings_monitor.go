package core

import (
	"context"
	"time"

	"go.uber.org/zap"
)

// Playback Settings Monitoring
// This module handles monitoring of Spotify playback settings (shuffle/repeat)
// and warns admins when settings are not optimal for auto-DJing

// runPlaybackSettingsMonitoring monitors playback settings compliance.
func (d *Dispatcher) runPlaybackSettingsMonitoring(ctx context.Context) {
	d.logger.Info("Starting playback settings monitoring")

	// Run immediately on startup
	d.checkPlaybackSettingsCompliance(ctx)

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

// checkPlaybackSettingsCompliance checks playback settings and sends warnings if needed.
func (d *Dispatcher) checkPlaybackSettingsCompliance(ctx context.Context) {
	// Check playback settings compliance
	compliance, err := d.spotify.CheckPlaybackCompliance(ctx)
	if err != nil {
		d.logger.Debug("Could not check playback compliance", zap.Error(err))
		return
	}

	if compliance.IsOptimalForAutoDJ() {
		// Settings are optimal - clear any pending warnings
		d.warningManager.ClearWarning(ctx, WarningTypeSettings)
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

	// Check if warning should be sent (avoid spam)
	if !d.warningManager.ShouldSendWarning(WarningTypeSettings) {
		d.logger.Debug("Playback settings compliance issues detected but warning already active",
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

	// Send settings warning to admins using warning manager
	if err := d.warningManager.SendWarningToAdmins(ctx, WarningTypeSettings, adminUserIDs, detailedMessage); err != nil {
		d.logger.Warn("Failed to send playback settings warning", zap.Error(err))
		return
	}

	d.logger.Info("Sent playback settings compliance warning message")
}

// attemptPlaybackSettingsCorrection tries to automatically fix playback settings.
// Returns true if all settings were successfully corrected, false otherwise.
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

// generatePlaybackSettingsWarningMessage creates a warning message based on settings issues.
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
