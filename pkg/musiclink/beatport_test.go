package musiclink

import (
	"testing"
)

func TestBeatportResolver_CanResolve(t *testing.T) {
	t.Helper()

	resolver := NewBeatportResolver()

	tests := []struct {
		name     string
		url      string
		expected bool
	}{
		{
			name:     "Standard Beatport track URL",
			url:      "https://www.beatport.com/track/love-songs-feat-kosmo-kint/21977538",
			expected: true,
		},
		{
			name:     "Beatport without www",
			url:      "https://beatport.com/track/some-track/12345",
			expected: true,
		},
		{
			name:     "Non-Beatport URL",
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

func TestBeatportResolver_extractFromTitleTag(t *testing.T) {
	t.Helper()

	resolver := NewBeatportResolver()

	tests := []struct {
		name           string
		html           string
		expectedTitle  string
		expectedArtist string
	}{
		{
			name: "Standard Beatport title format",
			html: `<title data-next-head="">Prospa, Kosmo Kint - Love Songs (feat. Kosmo Kint) ` +
				`(Extended Mix) [CircoLoco Records] | Music &amp; Downloads on Beatport</title>`,
			expectedTitle:  "Love Songs (feat. Kosmo Kint) (Extended Mix) [CircoLoco Records]",
			expectedArtist: "Prospa, Kosmo Kint",
		},
		{
			name:           "Single artist",
			html:           `<title>Artist Name - Track Title | Music &amp; Downloads on Beatport</title>`,
			expectedTitle:  "Track Title",
			expectedArtist: "Artist Name",
		},
		{
			name: "Multiple artists",
			html: `<title>Artist One, Artist Two, Artist Three - Track Name (Original Mix) ` +
				`| Music &amp; Downloads on Beatport</title>`,
			expectedTitle:  "Track Name (Original Mix)",
			expectedArtist: "Artist One, Artist Two, Artist Three",
		},
		{
			name:           "Track with label in brackets",
			html:           `<title>DJ Name - Song Title [Label Name] | Music &amp; Downloads on Beatport</title>`,
			expectedTitle:  "Song Title [Label Name]",
			expectedArtist: "DJ Name",
		},
		{
			name:           "No title tag",
			html:           `<html><body>No title here</body></html>`,
			expectedTitle:  "",
			expectedArtist: "",
		},
		{
			name:           "Title without proper format",
			html:           `<title>Just a random title</title>`,
			expectedTitle:  "",
			expectedArtist: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			title, artist := resolver.extractFromTitleTag(tt.html)
			if title != tt.expectedTitle {
				t.Errorf("extractFromTitleTag() title = %q, want %q", title, tt.expectedTitle)
			}
			if artist != tt.expectedArtist {
				t.Errorf("extractFromTitleTag() artist = %q, want %q", artist, tt.expectedArtist)
			}
		})
	}
}
