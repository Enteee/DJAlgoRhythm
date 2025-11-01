package musiclink

import (
	"testing"
)

//nolint:dupl // CanResolve tests intentionally follow same pattern across all resolvers for consistency.
func TestAppleMusicResolver_CanResolve(t *testing.T) {
	t.Helper()

	resolver := NewAppleMusicResolver()

	tests := []struct {
		name     string
		url      string
		expected bool
	}{
		{
			name:     "Valid music.apple.com URL",
			url:      "https://music.apple.com/us/album/never-gonna-give-you-up/123456?i=789",
			expected: true,
		},
		{
			name:     "Valid itunes.apple.com URL (legacy)",
			url:      "https://itunes.apple.com/us/album/some-album/id123",
			expected: true,
		},
		{
			name:     "Valid with different country code",
			url:      "https://music.apple.com/gb/album/track/123",
			expected: true,
		},
		{
			name:     "Valid direct song link",
			url:      "https://music.apple.com/us/song/track-name/123456789",
			expected: true,
		},
		{
			name:     "Invalid - regular apple.com",
			url:      "https://apple.com",
			expected: false,
		},
		{
			name:     "Invalid - www.apple.com",
			url:      "https://www.apple.com/music",
			expected: false,
		},
		{
			name:     "Invalid - non-Apple URL",
			url:      "https://example.com",
			expected: false,
		},
		{
			name:     "Invalid - Spotify URL",
			url:      "https://open.spotify.com/track/123",
			expected: false,
		},
		{
			name:     "Invalid - malformed URL",
			url:      "not-a-valid-url",
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

func TestAppleMusicResolver_extractTrackID(t *testing.T) {
	t.Helper()

	resolver := NewAppleMusicResolver()

	tests := []struct {
		name       string
		url        string
		expectedID string
		wantError  bool
	}{
		{
			name:       "Query parameter format with i=",
			url:        "https://music.apple.com/us/album/album-name/123456?i=789012345",
			expectedID: "789012345",
			wantError:  false,
		},
		{
			name:       "Direct song link format",
			url:        "https://music.apple.com/us/song/track-name/987654321",
			expectedID: "987654321",
			wantError:  false,
		},
		{
			name:       "Song link with multiple path segments",
			url:        "https://music.apple.com/gb/song/artist-song-title/555666777",
			expectedID: "555666777",
			wantError:  false,
		},
		{
			name:       "Query parameter with other params",
			url:        "https://music.apple.com/us/album/test/123?app=music&i=456789",
			expectedID: "456789",
			wantError:  false,
		},
		{
			name:      "Album link without i= parameter",
			url:       "https://music.apple.com/us/album/album-name/123456",
			wantError: true,
		},
		{
			name:      "No track ID in URL",
			url:       "https://music.apple.com/us/browse",
			wantError: true,
		},
		{
			name:      "Empty query parameter",
			url:       "https://music.apple.com/us/album/test/123?i=",
			wantError: true,
		},
		{
			name:       "Song path extracts last segment",
			url:        "https://music.apple.com/us/song/",
			expectedID: "song",
			wantError:  false,
		},
		{
			name:      "Malformed URL",
			url:       "not-a-url",
			wantError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			trackID, err := resolver.extractTrackID(tt.url)
			if tt.wantError {
				if err == nil {
					t.Errorf("extractTrackID() expected error but got none")
				}
			} else {
				if err != nil {
					t.Errorf("extractTrackID() unexpected error: %v", err)
				}
				if trackID != tt.expectedID {
					t.Errorf("extractTrackID() = %v, want %v", trackID, tt.expectedID)
				}
			}
		})
	}
}
