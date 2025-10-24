package core

import (
	"testing"
)

func TestGetQueueApprovalMessageKey(t *testing.T) {
	tests := []struct {
		name        string
		baseKey     string
		autoApprove bool
		expectedKey string
	}{
		{
			name:        "Manual approval - queue management",
			baseKey:     "bot.queue_management",
			autoApprove: false,
			expectedKey: "bot.queue_management",
		},
		{
			name:        "Manual approval - queue replacement",
			baseKey:     "bot.queue_replacement",
			autoApprove: false,
			expectedKey: "bot.queue_replacement",
		},
		{
			name:        "Auto approval - queue management",
			baseKey:     "bot.queue_management",
			autoApprove: true,
			expectedKey: "bot.queue_management_auto",
		},
		{
			name:        "Auto approval - queue replacement",
			baseKey:     "bot.queue_replacement",
			autoApprove: true,
			expectedKey: "bot.queue_replacement_auto",
		},
		{
			name:        "Manual approval - unknown key",
			baseKey:     "bot.unknown_key",
			autoApprove: false,
			expectedKey: "bot.unknown_key",
		},
		{
			name:        "Auto approval - unknown key returns original",
			baseKey:     "bot.unknown_key",
			autoApprove: true,
			expectedKey: "bot.unknown_key",
		},
		{
			name:        "Manual approval - empty key",
			baseKey:     "",
			autoApprove: false,
			expectedKey: "",
		},
		{
			name:        "Auto approval - empty key",
			baseKey:     "",
			autoApprove: true,
			expectedKey: "",
		},
		{
			name:        "Manual approval - similar but different key",
			baseKey:     "bot.queue_management_extra",
			autoApprove: false,
			expectedKey: "bot.queue_management_extra",
		},
		{
			name:        "Auto approval - similar but different key",
			baseKey:     "bot.queue_management_extra",
			autoApprove: true,
			expectedKey: "bot.queue_management_extra",
		},
		{
			name:        "Manual approval - case sensitive test",
			baseKey:     "bot.Queue_Management",
			autoApprove: false,
			expectedKey: "bot.Queue_Management",
		},
		{
			name:        "Auto approval - case sensitive test",
			baseKey:     "bot.Queue_Management",
			autoApprove: true,
			expectedKey: "bot.Queue_Management",
		},
	}

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
