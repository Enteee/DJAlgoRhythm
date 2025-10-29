package core

import (
	"testing"
)

// getQueueApprovalMessageKeyTestData returns test cases for the message key function.
func getQueueApprovalMessageKeyTestData() []struct {
	name        string
	baseKey     string
	autoApprove bool
	expectedKey string
} {
	return []struct {
		name        string
		baseKey     string
		autoApprove bool
		expectedKey string
	}{
		{"Manual approval - queue management", "bot.queue_management", false, "bot.queue_management"},
		{"Manual approval - queue replacement", "bot.queue_replacement", false, "bot.queue_replacement"},
		{"Auto approval - queue management", "bot.queue_management", true, "bot.queue_management_auto"},
		{"Auto approval - queue replacement", "bot.queue_replacement", true, "bot.queue_replacement_auto"},
		{"Manual approval - unknown key", "bot.unknown_key", false, "bot.unknown_key"},
		{"Auto approval - unknown key returns original", "bot.unknown_key", true, "bot.unknown_key"},
		{"Manual approval - empty key", "", false, ""},
		{"Auto approval - empty key", "", true, ""},
		{"Manual approval - similar but different key", "bot.queue_management_extra", false, "bot.queue_management_extra"},
		{"Auto approval - similar but different key", "bot.queue_management_extra", true, "bot.queue_management_extra"},
		{"Manual approval - case sensitive test", "bot.Queue_Management", false, "bot.Queue_Management"},
		{"Auto approval - case sensitive test", "bot.Queue_Management", true, "bot.Queue_Management"},
	}
}

func TestGetQueueApprovalMessageKey(t *testing.T) {
	tests := getQueueApprovalMessageKeyTestData()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := getQueueApprovalMessageKey(tt.baseKey, tt.autoApprove)
			if result != tt.expectedKey {
				t.Errorf("getQueueApprovalMessageKey(%q, %v) = %q, expected %q",
					tt.baseKey, tt.autoApprove, result, tt.expectedKey)
			}
		})
	}
}

func TestGetQueueApprovalMessageKey_AllKnownKeys(t *testing.T) {
	// Test all known base keys with both auto approval modes
	knownKeys := []string{
		"bot.queue_management",
		"bot.queue_replacement",
	}

	for _, baseKey := range knownKeys {
		t.Run(baseKey, func(t *testing.T) {
			// Test manual approval returns original key
			manualResult := getQueueApprovalMessageKey(baseKey, false)
			if manualResult != baseKey {
				t.Errorf("Manual approval for %q should return %q, got %q", baseKey, baseKey, manualResult)
			}

			// Test auto approval returns modified key
			autoResult := getQueueApprovalMessageKey(baseKey, true)
			expectedAutoKey := baseKey + "_auto"
			if autoResult != expectedAutoKey {
				t.Errorf("Auto approval for %q should return %q, got %q", baseKey, expectedAutoKey, autoResult)
			}
		})
	}
}
