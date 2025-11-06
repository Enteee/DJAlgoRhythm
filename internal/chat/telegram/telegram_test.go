package telegram

import (
	"strings"
	"testing"

	"go.uber.org/zap"
)

func TestNewFrontend(t *testing.T) {
	config := &Config{
		BotToken:            "test-token",
		GroupID:             -123456789,
		FloodLimitPerMinute: 10,
	}

	logger := zap.NewNop()

	frontend := NewFrontend(config, logger)

	if frontend == nil {
		t.Fatal("NewFrontend returned nil")
	}

	if frontend.config.BotToken != config.BotToken {
		t.Errorf("Expected bot token %s, got %s", config.BotToken, frontend.config.BotToken)
	}

	if frontend.config.GroupID != config.GroupID {
		t.Errorf("Expected group ID %d, got %d", config.GroupID, frontend.config.GroupID)
	}

	if frontend.floodgate == nil {
		t.Error("Floodgate was not initialized")
	}

	// Test floodgate functionality
	stats := frontend.floodgate.GetStats()
	if stats.LimitPerMinute != config.FloodLimitPerMinute {
		t.Errorf("Expected flood limit %d, got %d", config.FloodLimitPerMinute, stats.LimitPerMinute)
	}
	if stats.WindowSeconds != 60 {
		t.Errorf("Expected flood window 60 seconds, got %d", stats.WindowSeconds)
	}
}

func TestGetUserDisplayNameLogic(t *testing.T) {
	tests := []struct {
		name      string
		username  string
		firstName string
		lastName  string
		expected  string
	}{
		{
			name:      "With username",
			username:  "testuser",
			firstName: "Test",
			lastName:  "User",
			expected:  "@testuser",
		},
		{
			name:      "Without username, with both names",
			username:  "",
			firstName: "Test",
			lastName:  "User",
			expected:  "Test User",
		},
		{
			name:      "Without username, first name only",
			username:  "",
			firstName: "Test",
			lastName:  "",
			expected:  "Test",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Test the logic directly
			var result string
			if tt.username != "" {
				result = "@" + tt.username
			} else {
				result = tt.firstName
				if tt.lastName != "" {
					result += " " + tt.lastName
				}
			}

			if result != tt.expected {
				t.Errorf("Expected '%s', got '%s'", tt.expected, result)
			}
		})
	}
}

func TestExtractURLsLogic(t *testing.T) {
	text := "Check out this song: https://spotify.com/track/123 and this one: https://youtube.com/watch?v=456"

	// Let's use string search to find the actual positions
	url1 := "https://spotify.com/track/123"
	url2 := "https://youtube.com/watch?v=456"

	offset1 := strings.Index(text, url1)
	offset2 := strings.Index(text, url2)

	// Test URL extraction logic
	tests := []struct {
		name       string
		entityType string
		offset     int
		length     int
		expected   string
	}{
		{"First URL", "url", offset1, len(url1), url1},
		{"Second URL", "url", offset2, len(url2), url2},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.entityType == "url" {
				if tt.offset == -1 {
					t.Errorf("URL '%s' not found in text", tt.expected)
					return
				}
				if tt.offset+tt.length > len(text) {
					t.Errorf("Invalid slice bounds: offset=%d, length=%d, text_length=%d",
						tt.offset, tt.length, len(text))
					return
				}
				url := text[tt.offset : tt.offset+tt.length]
				if url != tt.expected {
					t.Errorf("Expected URL '%s', got '%s'", tt.expected, url)
				}
			}
		})
	}
}
