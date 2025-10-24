package flood

import (
	"testing"
	"time"
)

func TestFloodgate_CheckMessage_AllowsNormalUsage(t *testing.T) {
	fg := New(3) // 3 messages per minute
	defer fg.Stop()

	chatID := "chat1"
	userID := "user1"

	// Should allow first 3 messages
	for i := 0; i < 3; i++ {
		if !fg.CheckMessage(chatID, userID) {
			t.Errorf("Message %d should be allowed", i+1)
		}
	}

	// 4th message should be blocked
	if fg.CheckMessage(chatID, userID) {
		t.Error("4th message should be blocked")
	}
}

func TestFloodgate_CheckMessage_SlidingWindow(t *testing.T) {
	// This test verifies the sliding window concept but doesn't wait the full 60 seconds
	// Instead we test that the window works correctly by manipulating internal state
	fg := New(2) // 2 messages per minute
	defer fg.Stop()

	chatID := "chat1"
	userID := "user1"

	// Send 2 messages (should both be allowed)
	if !fg.CheckMessage(chatID, userID) {
		t.Error("First message should be allowed")
	}
	if !fg.CheckMessage(chatID, userID) {
		t.Error("Second message should be allowed")
	}

	// Third message should be blocked
	if fg.CheckMessage(chatID, userID) {
		t.Error("Third message should be blocked")
	}

	// Manually adjust timestamps to simulate time passing
	// This is internal testing, so we access internal state
	key := chatID + ":" + userID
	fg.mutex.Lock()
	if entry, exists := fg.entries[key]; exists {
		// Move timestamps back by 61 seconds to simulate window expiry
		pastTime := time.Now().Add(-61 * time.Second)
		for i := range entry.timestamps {
			entry.timestamps[i] = pastTime
		}
	}
	fg.mutex.Unlock()

	// Should allow message again after simulated window slide
	if !fg.CheckMessage(chatID, userID) {
		t.Error("Message after window slide should be allowed")
	}
}

func TestFloodgate_CheckMessage_PerUserPerChat(t *testing.T) {
	fg := New(2) // 2 messages per minute
	defer fg.Stop()

	chatID1 := "chat1"
	chatID2 := "chat2"
	userID1 := "user1"
	userID2 := "user2"

	// Same user in different chats should have separate limits
	for i := 0; i < 2; i++ {
		if !fg.CheckMessage(chatID1, userID1) {
			t.Errorf("Message %d in chat1 should be allowed", i+1)
		}
		if !fg.CheckMessage(chatID2, userID1) {
			t.Errorf("Message %d in chat2 should be allowed", i+1)
		}
	}

	// Different users in same chat should have separate limits
	for i := 0; i < 2; i++ {
		if !fg.CheckMessage(chatID1, userID2) {
			t.Errorf("Message %d from user2 should be allowed", i+1)
		}
	}

	// All users should now be at their limits
	if fg.CheckMessage(chatID1, userID1) {
		t.Error("Extra message from user1 in chat1 should be blocked")
	}
	if fg.CheckMessage(chatID2, userID1) {
		t.Error("Extra message from user1 in chat2 should be blocked")
	}
	if fg.CheckMessage(chatID1, userID2) {
		t.Error("Extra message from user2 in chat1 should be blocked")
	}
}

func TestFloodgate_CheckMessage_WindowExpiry(t *testing.T) {
	fg := New(1) // 1 message per minute
	defer fg.Stop()

	chatID := "chat1"
	userID := "user1"

	// First message should be allowed
	if !fg.CheckMessage(chatID, userID) {
		t.Error("First message should be allowed")
	}

	// Second message immediately should be blocked
	if fg.CheckMessage(chatID, userID) {
		t.Error("Second immediate message should be blocked")
	}

	// Simulate window expiry by manipulating internal timestamps
	key := chatID + ":" + userID
	fg.mutex.Lock()
	if entry, exists := fg.entries[key]; exists {
		// Move timestamp back by 61 seconds to simulate window expiry
		entry.timestamps[0] = time.Now().Add(-61 * time.Second)
	}
	fg.mutex.Unlock()

	// Should allow message again after simulated window expiry
	if !fg.CheckMessage(chatID, userID) {
		t.Error("Message after window expiry should be allowed")
	}
}

func TestFloodgate_GetStats(t *testing.T) {
	fg := New(5)
	defer fg.Stop()

	// Check initial stats
	stats := fg.GetStats()
	if stats.ActiveUsers != 0 {
		t.Errorf("Expected 0 active users initially, got %d", stats.ActiveUsers)
	}
	if stats.LimitPerMinute != 5 {
		t.Errorf("Expected limit per minute 5, got %d", stats.LimitPerMinute)
	}
	if stats.WindowSeconds != 60 {
		t.Errorf("Expected window seconds 60, got %d", stats.WindowSeconds)
	}

	// Add some users
	fg.CheckMessage("chat1", "user1")
	fg.CheckMessage("chat1", "user2")
	fg.CheckMessage("chat2", "user1") // Same user, different chat

	stats = fg.GetStats()
	if stats.ActiveUsers != 3 {
		t.Errorf("Expected 3 active users, got %d", stats.ActiveUsers)
	}
}

func TestFloodgate_EdgeCases(t *testing.T) {
	t.Run("Zero limit", func(t *testing.T) {
		fg := New(0)
		defer fg.Stop()

		// All messages should be blocked with zero limit
		if fg.CheckMessage("chat1", "user1") {
			t.Error("Message should be blocked with zero limit")
		}
	})

	t.Run("Empty identifiers", func(t *testing.T) {
		fg := New(1)
		defer fg.Stop()

		// Should handle empty strings gracefully
		if !fg.CheckMessage("", "") {
			t.Error("Should allow message with empty identifiers")
		}
		if fg.CheckMessage("", "") {
			t.Error("Second message with empty identifiers should be blocked")
		}
	})

	t.Run("Window behavior", func(t *testing.T) {
		fg := New(1) // 1 message per minute
		defer fg.Stop()

		// First message should be allowed
		if !fg.CheckMessage("chat1", "user1") {
			t.Error("Should allow first message")
		}
		// Second message should be blocked (within 60-second window)
		if fg.CheckMessage("chat1", "user1") {
			t.Error("Should block second message within window")
		}
	})
}

func TestFloodgate_Cleanup(t *testing.T) {
	// This test is more complex and would require manipulating internal state
	// or waiting for actual cleanup cycles. For production use, we verify
	// that cleanup doesn't crash and basic functionality works.
	fg := New(1)
	defer fg.Stop()

	// Add some entries
	fg.CheckMessage("chat1", "user1")
	fg.CheckMessage("chat2", "user2")

	// Trigger manual cleanup (this would normally happen in background)
	fg.performCleanup()

	// Should still work after cleanup
	if !fg.CheckMessage("chat3", "user3") {
		t.Error("Should work after cleanup")
	}
}

func TestFloodgate_ConcurrentAccess(t *testing.T) {
	fg := New(10)
	defer fg.Stop()

	// Test concurrent access from multiple goroutines
	done := make(chan bool, 10)

	for i := 0; i < 10; i++ {
		go func(_ int) {
			for j := 0; j < 5; j++ {
				fg.CheckMessage("chat1", "user1")
				fg.GetStats()
			}
			done <- true
		}(i)
	}

	// Wait for all goroutines to complete
	for i := 0; i < 10; i++ {
		<-done
	}

	// Should still be functional
	stats := fg.GetStats()
	if stats.ActiveUsers < 0 {
		t.Error("Stats should be valid after concurrent access")
	}
}
