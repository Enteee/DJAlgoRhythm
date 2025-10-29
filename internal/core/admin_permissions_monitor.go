package core

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"go.uber.org/zap"
)

// Admin Permissions Monitoring
// This module handles monitoring of bot admin permissions in the group
// and warns admins when bot lacks necessary permissions for proper functionality.

// runAdminPermissionsMonitoring monitors bot admin permissions compliance.
func (d *Dispatcher) runAdminPermissionsMonitoring(ctx context.Context) {
	d.logger.Info("Starting admin permissions monitoring")

	// Run immediately on startup
	d.checkAdminPermissionsCompliance(ctx)

	ticker := time.NewTicker(adminPermissionsCheckInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			d.logger.Info("Admin permissions monitoring stopped")
			return
		case <-ticker.C:
			d.checkAdminPermissionsCompliance(ctx)
		}
	}
}

// checkAdminPermissionsCompliance checks bot admin permissions and sends warnings if needed.
func (d *Dispatcher) checkAdminPermissionsCompliance(ctx context.Context) {
	// Get group ID
	groupID := d.getGroupID()
	if groupID == "" {
		d.logger.Debug("No group ID available for admin permissions check")
		return
	}

	// Check if bot has admin permissions
	hasAdminPermissions, err := d.checkBotAdminPermissions(ctx, groupID)
	if err != nil {
		d.logger.Debug("Could not check bot admin permissions", zap.Error(err))
		return
	}

	if hasAdminPermissions {
		// Bot has admin permissions - clear any pending warnings
		d.warningManager.ClearWarning(ctx, WarningTypePermissions)
		return
	}

	// Admin permissions missing - log warning
	d.logger.Warn("Bot lacks admin permissions in group, some features may not work properly")

	// Check if warning should be sent (avoid spam)
	if !d.warningManager.ShouldSendWarning(WarningTypePermissions) {
		d.logger.Debug("Bot admin permissions missing but warning already active")
		return
	}

	d.logger.Info("Bot admin permissions missing, sending admin warning")

	// Get list of admin user IDs
	adminUserIDs, err := d.frontend.GetAdminUserIDs(ctx, groupID)
	if err != nil {
		d.logger.Warn("Failed to get admin user IDs for permissions warning", zap.Error(err))
		return
	}

	if len(adminUserIDs) == 0 {
		d.logger.Warn("No admin user IDs found for permissions warning")
		return
	}

	// Generate admin permissions warning message
	warningMessage := d.generateAdminPermissionsWarningMessage()

	// Send permissions warning to admins using warning manager
	if err := d.warningManager.SendWarningToAdmins(ctx, WarningTypePermissions, adminUserIDs, warningMessage); err != nil {
		d.logger.Warn("Failed to send admin permissions warning", zap.Error(err))
		return
	}

	d.logger.Info("Sent admin permissions warning message")
}

// checkBotAdminPermissions checks if the bot has admin permissions in the group.
func (d *Dispatcher) checkBotAdminPermissions(ctx context.Context, groupID string) (bool, error) {
	// Get bot's user ID using GetMe
	botUser, err := d.frontend.GetMe(ctx)
	if err != nil {
		return false, fmt.Errorf("failed to get bot user info: %w", err)
	}

	// Convert group ID string to int64
	groupIDInt, err := strconv.ParseInt(groupID, 10, 64)
	if err != nil {
		return false, fmt.Errorf("invalid group ID format: %w", err)
	}

	// Check bot's membership status in the group
	member, err := d.frontend.GetChatMember(ctx, groupIDInt, botUser.ID)
	if err != nil {
		return false, fmt.Errorf("failed to get bot chat member info: %w", err)
	}

	// Check if bot is admin (creator or administrator)
	isAdmin := member.Status == "creator" || member.Status == "administrator"

	d.logger.Debug("Bot admin status check completed",
		zap.Int64("botUserID", botUser.ID),
		zap.String("groupID", groupID),
		zap.String("memberStatus", member.Status),
		zap.Bool("isAdmin", isAdmin))

	return isAdmin, nil
}

// generateAdminPermissionsWarningMessage creates a warning message for admin permission issues.
func (d *Dispatcher) generateAdminPermissionsWarningMessage() string {
	return d.localizer.T("admin.insufficient_permissions")
}
