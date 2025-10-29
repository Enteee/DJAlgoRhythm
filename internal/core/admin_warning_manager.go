package core

import (
	"context"
	"fmt"
	"sync"

	"go.uber.org/zap"

	"djalgorhythm/internal/chat"
)

// WarningType represents different types of admin warnings.
type WarningType string

// Warning type constants for different admin notification categories.
const (
	WarningTypeDevice      WarningType = "device"      // No active Spotify device found
	WarningTypePermissions WarningType = "permissions" // Bot lacks admin permissions
	WarningTypeSettings    WarningType = "settings"    // Playback settings not optimal
	WarningTypeQueueSync   WarningType = "queue_sync"  // Shadow queue out of sync with Spotify queue
)

// AdminWarningManager manages admin warning messages with automatic cleanup.
type AdminWarningManager struct {
	// Per-warning-type state tracking
	activeWarnings  map[WarningType]bool              // type -> active status
	warningMessages map[WarningType]map[string]string // type -> userID -> messageID
	mutex           sync.RWMutex                      // protects all warning state
	frontend        chat.Frontend                     // for sending/deleting messages
	logger          *zap.Logger                       // for logging
}

// NewAdminWarningManager creates a new admin warning manager.
func NewAdminWarningManager(frontend chat.Frontend, logger *zap.Logger) *AdminWarningManager {
	return &AdminWarningManager{
		activeWarnings:  make(map[WarningType]bool),
		warningMessages: make(map[WarningType]map[string]string),
		frontend:        frontend,
		logger:          logger,
	}
}

// ShouldSendWarning checks if a warning should be sent for the given type.
func (m *AdminWarningManager) ShouldSendWarning(warningType WarningType) bool {
	m.mutex.RLock()
	defer m.mutex.RUnlock()

	return !m.activeWarnings[warningType] // Send warning only if no warning is currently active
}

// SendWarningToAdmins sends a warning message to all admin users and tracks message IDs for cleanup.
func (m *AdminWarningManager) SendWarningToAdmins(
	ctx context.Context,
	warningType WarningType,
	adminUserIDs []string,
	message string,
) error {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	// Initialize warning messages map for this type if needed
	if m.warningMessages[warningType] == nil {
		m.warningMessages[warningType] = make(map[string]string)
	}

	successCount := 0
	var errors []string

	// Send messages and track IDs for later deletion
	for _, adminUserID := range adminUserIDs {
		msgID, err := m.frontend.SendDirectMessage(ctx, adminUserID, message)
		if err != nil {
			m.logger.Warn("Failed to send admin warning",
				zap.String("warningType", string(warningType)),
				zap.String("adminUserID", adminUserID),
				zap.Error(err))
			errors = append(errors, err.Error())
		} else {
			m.warningMessages[warningType][adminUserID] = msgID
			successCount++
		}
	}

	// Mark warning as active
	m.activeWarnings[warningType] = true

	m.logger.Info("Admin warning sent",
		zap.String("warningType", string(warningType)),
		zap.Int("successCount", successCount),
		zap.Int("totalAdmins", len(adminUserIDs)),
		zap.Strings("errors", errors))

	if len(errors) > 0 {
		return fmt.Errorf("failed to send %d/%d warnings", len(errors), len(adminUserIDs))
	}

	return nil
}

// ClearWarning clears the warning state and deletes sent messages when the issue is resolved.
func (m *AdminWarningManager) ClearWarning(ctx context.Context, warningType WarningType) {
	m.mutex.Lock()
	wasActive := m.activeWarnings[warningType]
	messagesToDelete := make(map[string]string)

	// Copy messages to delete before clearing
	if m.warningMessages[warningType] != nil {
		for userID, msgID := range m.warningMessages[warningType] {
			messagesToDelete[userID] = msgID
		}
	}

	// Clear the state
	m.activeWarnings[warningType] = false
	if m.warningMessages[warningType] != nil {
		m.warningMessages[warningType] = make(map[string]string)
	}
	m.mutex.Unlock()

	if wasActive && len(messagesToDelete) > 0 {
		m.logger.Info("Clearing admin warning messages",
			zap.String("warningType", string(warningType)),
			zap.Int("messageCount", len(messagesToDelete)))

		// Delete messages from user DMs
		for userID, msgID := range messagesToDelete {
			if err := m.frontend.DeleteMessage(ctx, userID, msgID); err != nil {
				m.logger.Debug("Failed to delete admin warning message",
					zap.String("warningType", string(warningType)),
					zap.String("userID", userID),
					zap.String("messageID", msgID),
					zap.Error(err))
			}
		}

		m.logger.Debug("Admin warning cleared",
			zap.String("warningType", string(warningType)))
	}
}

// IsWarningActive checks if a warning is currently active.
func (m *AdminWarningManager) IsWarningActive(warningType WarningType) bool {
	m.mutex.RLock()
	defer m.mutex.RUnlock()

	return m.activeWarnings[warningType]
}
