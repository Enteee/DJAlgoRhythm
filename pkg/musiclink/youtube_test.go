package musiclink

import (
	"testing"
)

func TestYouTubeResolver_CanResolve(t *testing.T) {
	resolver := NewYouTubeResolver()

	tests := []struct {
		name     string
		url      string
		expected bool
	}{
		{
			name:     "Standard YouTube URL",
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
			name:     "Mobile YouTube URL",
			url:      "https://m.youtube.com/watch?v=dQw4w9WgXcQ",
			expected: true,
		},
		{
			name:     "Non-YouTube URL",
			url:      "https://example.com",
			expected: false,
		},
		{
			name:     "Spotify URL",
			url:      "https://open.spotify.com/track/123",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := resolver.CanResolve(tt.url)
			if result != tt.expected {
				t.Errorf("CanResolve() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestYouTubeResolver_extractVideoID(t *testing.T) {
	resolver := NewYouTubeResolver()

	tests := []struct {
		name       string
		url        string
		expectedID string
		wantError  bool
	}{
		{
			name:       "Standard YouTube URL",
			url:        "https://www.youtube.com/watch?v=dQw4w9WgXcQ",
			expectedID: "dQw4w9WgXcQ",
			wantError:  false,
		},
		{
			name:       "YouTube short URL",
			url:        "https://youtu.be/dQw4w9WgXcQ",
			expectedID: "dQw4w9WgXcQ",
			wantError:  false,
		},
		{
			name:       "YouTube Music URL",
			url:        "https://music.youtube.com/watch?v=dQw4w9WgXcQ",
			expectedID: "dQw4w9WgXcQ",
			wantError:  false,
		},
		{
			name:       "URL with additional parameters",
			url:        "https://www.youtube.com/watch?v=dQw4w9WgXcQ&list=PLrAXtmErZgOeiKm4sgNOknGvNjby9efdf",
			expectedID: "dQw4w9WgXcQ",
			wantError:  false,
		},
		{
			name:      "No video ID",
			url:       "https://www.youtube.com/",
			wantError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			videoID, err := resolver.extractVideoID(tt.url)
			if tt.wantError {
				if err == nil {
					t.Errorf("extractVideoID() expected error but got none")
				}
			} else {
				if err != nil {
					t.Errorf("extractVideoID() unexpected error: %v", err)
				}
				if videoID != tt.expectedID {
					t.Errorf("extractVideoID() = %v, want %v", videoID, tt.expectedID)
				}
			}
		})
	}
}

func TestYouTubeResolver_cleanTitle(t *testing.T) {
	resolver := NewYouTubeResolver()

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "Title with Official Video",
			input:    "Rick Astley - Never Gonna Give You Up (Official Video)",
			expected: "Rick Astley - Never Gonna Give You Up",
		},
		{
			name:     "Title with Lyric Video",
			input:    "Some Song (Lyric Video)",
			expected: "Some Song",
		},
		{
			name:     "Title with HD",
			input:    "Amazing Track [HD]",
			expected: "Amazing Track",
		},
		{
			name:     "Title with multiple markers",
			input:    "Song Title (Official Music Video) [4K]",
			expected: "Song Title",
		},
		{
			name:     "Clean title",
			input:    "Simple Song Title",
			expected: "Simple Song Title",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := resolver.cleanTitle(tt.input)
			if result != tt.expected {
				t.Errorf("cleanTitle() = %v, want %v", result, tt.expected)
			}
		})
	}
}
