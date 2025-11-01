package musiclink

import (
	"testing"
)

//nolint:dupl // CanResolve tests intentionally follow same pattern across all resolvers for consistency.
func TestSoundCloudResolver_CanResolve(t *testing.T) {
	t.Helper()

	resolver := NewSoundCloudResolver()

	tests := []struct {
		name     string
		url      string
		expected bool
	}{
		{
			name:     "Valid soundcloud.com URL",
			url:      "https://soundcloud.com/artist/track-name",
			expected: true,
		},
		{
			name:     "Valid www.soundcloud.com URL",
			url:      "https://www.soundcloud.com/artist/track-name",
			expected: true,
		},
		{
			name:     "Valid mobile soundcloud.com URL",
			url:      "https://m.soundcloud.com/artist/track-name",
			expected: true,
		},
		{
			name:     "Valid short soundcloud.com URL",
			url:      "https://on.soundcloud.com/abc123",
			expected: true,
		},
		{
			name:     "Valid with query parameters",
			url:      "https://soundcloud.com/artist/track?in=artist/sets/playlist",
			expected: true,
		},
		{
			name:     "Invalid - non-SoundCloud URL",
			url:      "https://example.com",
			expected: false,
		},
		{
			name:     "Invalid - Spotify URL",
			url:      "https://open.spotify.com/track/123",
			expected: false,
		},
		{
			name:     "Invalid - YouTube URL",
			url:      "https://www.youtube.com/watch?v=123",
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

func TestSoundCloudResolver_parseTrackInfo_StandardCases(t *testing.T) {
	t.Helper()

	resolver := NewSoundCloudResolver()

	tests := []struct {
		name           string
		response       *SoundCloudOEmbedResponse
		expectedTitle  string
		expectedArtist string
	}{
		{
			name: "Standard format with 'by' separator",
			response: &SoundCloudOEmbedResponse{
				Title:      "Never Gonna Give You Up by Rick Astley",
				AuthorName: "Rick Astley",
				AuthorURL:  "https://soundcloud.com/rick-astley-official",
			},
			expectedTitle:  "Never Gonna Give You Up",
			expectedArtist: "Rick Astley",
		},
		{
			name: "Track title without 'by' separator",
			response: &SoundCloudOEmbedResponse{
				Title:      "Some Track Title",
				AuthorName: "Artist Name",
				AuthorURL:  "https://soundcloud.com/artist-name",
			},
			expectedTitle:  "Some Track Title",
			expectedArtist: "Artist Name",
		},
		{
			name: "Multiple 'by' occurrences - splits on first",
			response: &SoundCloudOEmbedResponse{
				Title:      "Track by Artist by Remixer",
				AuthorName: "Artist",
				AuthorURL:  "https://soundcloud.com/artist",
			},
			expectedTitle:  "Track",
			expectedArtist: "Artist by Remixer",
		},
		{
			name: "Title with extra whitespace",
			response: &SoundCloudOEmbedResponse{
				Title:      "  Track Title  by  Artist Name  ",
				AuthorName: "Artist Name",
				AuthorURL:  "https://soundcloud.com/artist",
			},
			expectedTitle:  "Track Title",
			expectedArtist: "Artist Name",
		},
		{
			name: "Title with featuring artist",
			response: &SoundCloudOEmbedResponse{
				Title:      "Track Title (feat. Featured Artist) by Main Artist",
				AuthorName: "Main Artist",
				AuthorURL:  "https://soundcloud.com/main-artist",
			},
			expectedTitle:  "Track Title (feat. Featured Artist)",
			expectedArtist: "Main Artist",
		},
		{
			name: "Title with remix info",
			response: &SoundCloudOEmbedResponse{
				Title:      "Track Title (Remix) by Artist Name",
				AuthorName: "Artist Name",
				AuthorURL:  "https://soundcloud.com/artist",
			},
			expectedTitle:  "Track Title (Remix)",
			expectedArtist: "Artist Name",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			title, artist := resolver.parseTrackInfo(tt.response)
			if title != tt.expectedTitle {
				t.Errorf("parseTrackInfo() title = %q, want %q", title, tt.expectedTitle)
			}
			if artist != tt.expectedArtist {
				t.Errorf("parseTrackInfo() artist = %q, want %q", artist, tt.expectedArtist)
			}
		})
	}
}

func TestSoundCloudResolver_parseTrackInfo_EdgeCases(t *testing.T) {
	t.Helper()

	resolver := NewSoundCloudResolver()

	tests := []struct {
		name           string
		response       *SoundCloudOEmbedResponse
		expectedTitle  string
		expectedArtist string
	}{
		{
			name: "Empty title falls back to author_name",
			response: &SoundCloudOEmbedResponse{
				Title:      "",
				AuthorName: "Artist Name",
				AuthorURL:  "https://soundcloud.com/artist",
			},
			expectedTitle:  "",
			expectedArtist: "Artist Name",
		},
		{
			name: "Different author_name than parsed artist",
			response: &SoundCloudOEmbedResponse{
				Title:      "Track Name by Actual Artist",
				AuthorName: "Uploader Name",
				AuthorURL:  "https://soundcloud.com/uploader",
			},
			expectedTitle:  "Track Name",
			expectedArtist: "Actual Artist",
		},
		{
			name: "Case sensitive - 'BY' should not match",
			response: &SoundCloudOEmbedResponse{
				Title:      "Track Title BY Artist Name",
				AuthorName: "Artist Name",
				AuthorURL:  "https://soundcloud.com/artist",
			},
			expectedTitle:  "Track Title BY Artist Name",
			expectedArtist: "Artist Name",
		},
		{
			name: "Title with dash and by separator",
			response: &SoundCloudOEmbedResponse{
				Title:      "Artist - Track Title by Artist",
				AuthorName: "Artist",
				AuthorURL:  "https://soundcloud.com/artist",
			},
			expectedTitle:  "Artist - Track Title",
			expectedArtist: "Artist",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			title, artist := resolver.parseTrackInfo(tt.response)
			if title != tt.expectedTitle {
				t.Errorf("parseTrackInfo() title = %q, want %q", title, tt.expectedTitle)
			}
			if artist != tt.expectedArtist {
				t.Errorf("parseTrackInfo() artist = %q, want %q", artist, tt.expectedArtist)
			}
		})
	}
}
