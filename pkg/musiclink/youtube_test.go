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

func TestYouTubeResolver_extractArtist(t *testing.T) {
	t.Helper()

	resolver := NewYouTubeResolver()

	tests := []struct {
		name       string
		title      string
		authorName string
		expected   string
	}{
		{
			name:       "VEVO channel",
			title:      "Never Gonna Give You Up",
			authorName: "RickAstleyVEVO",
			expected:   "Rick Astley",
		},
		{
			name:       "Topic channel",
			title:      "Some Song",
			authorName: "Artist Name - Topic",
			expected:   "Artist Name",
		},
		{
			name:       "Title with separator - first part is artist",
			title:      "Rick Astley - Never Gonna Give You Up",
			authorName: "RickAstleyVEVO",
			expected:   "Rick Astley",
		},
		{
			name:       "Title with separator from non-VEVO channel",
			title:      "Artist Name - Track Title",
			authorName: "Random Channel",
			expected:   "Artist Name",
		},
		{
			name:       "No separator returns authorName",
			title:      "Just a song title",
			authorName: "Channel Name",
			expected:   "Channel Name",
		},
		{
			name:       "Multiple separators takes first",
			title:      "Artist - Song - Extended Mix",
			authorName: "Music Channel",
			expected:   "Artist",
		},
		{
			name:       "VEVO with CamelCase",
			title:      "Some Track",
			authorName: "JohnDoeVEVO",
			expected:   "John Doe",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := resolver.extractArtist(tt.title, tt.authorName)
			if result != tt.expected {
				t.Errorf("extractArtist() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestYouTubeResolver_splitCamelCase(t *testing.T) {
	t.Helper()

	resolver := NewYouTubeResolver()

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "Simple CamelCase",
			input:    "RickAstley",
			expected: "Rick Astley",
		},
		{
			name:     "Multiple words",
			input:    "JohnDoeSmith",
			expected: "John Doe Smith",
		},
		{
			name:     "Already spaced",
			input:    "Rick Astley",
			expected: "Rick Astley",
		},
		{
			name:     "Single word",
			input:    "Artist",
			expected: "Artist",
		},
		{
			name:     "All lowercase",
			input:    "rickastley",
			expected: "rickastley",
		},
		{
			name:     "Multiple consecutive capitals - no change",
			input:    "ABCTest",
			expected: "ABCTest",
		},
		{
			name:     "Empty string",
			input:    "",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := resolver.splitCamelCase(tt.input)
			if result != tt.expected {
				t.Errorf("splitCamelCase() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestYouTubeResolver_parseTrackInfo(t *testing.T) {
	t.Helper()

	resolver := NewYouTubeResolver()

	tests := []struct {
		name           string
		response       *YouTubeOEmbedResponse
		expectedTitle  string
		expectedArtist string
	}{
		{
			name: "Standard video with VEVO channel",
			response: &YouTubeOEmbedResponse{
				Title:      "Rick Astley - Never Gonna Give You Up (Official Video)",
				AuthorName: "RickAstleyVEVO",
			},
			expectedTitle:  "Rick Astley - Never Gonna Give You Up",
			expectedArtist: "Rick Astley",
		},
		{
			name: "Topic channel",
			response: &YouTubeOEmbedResponse{
				Title:      "Some Song Title",
				AuthorName: "Artist Name - Topic",
			},
			expectedTitle:  "Some Song Title",
			expectedArtist: "Artist Name",
		},
		{
			name: "Title with separators and markers",
			response: &YouTubeOEmbedResponse{
				Title:      "Artist - Track Title (Official Music Video) [4K]",
				AuthorName: "Music Channel",
			},
			expectedTitle:  "Artist - Track Title",
			expectedArtist: "Artist",
		},
		{
			name: "Clean title no markers",
			response: &YouTubeOEmbedResponse{
				Title:      "Simple Title",
				AuthorName: "Channel Name",
			},
			expectedTitle:  "Simple Title",
			expectedArtist: "Channel Name",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			title, artist := resolver.parseTrackInfo(tt.response)
			if title != tt.expectedTitle {
				t.Errorf("parseTrackInfo() title = %v, want %v", title, tt.expectedTitle)
			}
			if artist != tt.expectedArtist {
				t.Errorf("parseTrackInfo() artist = %v, want %v", artist, tt.expectedArtist)
			}
		})
	}
}
