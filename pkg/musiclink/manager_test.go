package musiclink

import (
	"context"
	"strings"
	"testing"
)

func TestManager_CanResolve(t *testing.T) {
	t.Helper()

	manager := NewManager()

	tests := []struct {
		name     string
		url      string
		expected bool
	}{
		{
			name:     "YouTube standard URL",
			url:      "https://www.youtube.com/watch?v=dQw4w9WgXcQ",
			expected: true,
		},
		{
			name:     "YouTube short URL",
			url:      "https://youtu.be/dQw4w9WgXcQ",
			expected: true,
		},
		{
			name:     "YouTube Music URL",
			url:      "https://music.youtube.com/watch?v=dQw4w9WgXcQ",
			expected: true,
		},
		{
			name:     "Apple Music URL",
			url:      "https://music.apple.com/us/album/test/123?i=456",
			expected: true,
		},
		{
			name:     "Tidal URL",
			url:      "https://tidal.com/track/12345678",
			expected: true,
		},
		{
			name:     "Beatport URL",
			url:      "https://www.beatport.com/track/test/12345",
			expected: true,
		},
		{
			name:     "Amazon Music URL",
			url:      "https://music.amazon.com/albums/B08X123456",
			expected: true,
		},
		{
			name:     "Spotify URL - not supported",
			url:      "https://open.spotify.com/track/123",
			expected: false,
		},
		{
			name:     "Unknown URL",
			url:      "https://example.com",
			expected: false,
		},
		{
			name:     "Empty string",
			url:      "",
			expected: false,
		},
		{
			name:     "Malformed URL",
			url:      "not-a-url",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := manager.CanResolve(tt.url)
			if result != tt.expected {
				t.Errorf("CanResolve() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestManager_Resolve_NoResolverFound(t *testing.T) {
	t.Helper()

	manager := NewManager()
	ctx := context.Background()

	tests := []struct {
		name string
		url  string
	}{
		{
			name: "Spotify URL",
			url:  "https://open.spotify.com/track/123",
		},
		{
			name: "Unknown domain",
			url:  "https://example.com",
		},
		{
			name: "Malformed URL",
			url:  "not-a-url",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := manager.Resolve(ctx, tt.url)
			if err == nil {
				t.Error("Resolve() expected error but got none")
			}
			if !strings.Contains(err.Error(), "no resolver found") {
				t.Errorf("Resolve() error = %v, want error containing 'no resolver found'", err)
			}
		})
	}
}
