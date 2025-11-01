package musiclink

import (
	"testing"
)

func TestTidalResolver_CanResolve(t *testing.T) {
	t.Helper()

	resolver := NewTidalResolver()

	tests := []struct {
		name     string
		url      string
		expected bool
	}{
		{
			name:     "Valid tidal.com URL",
			url:      "https://tidal.com/track/12345678",
			expected: true,
		},
		{
			name:     "Valid www.tidal.com URL",
			url:      "https://www.tidal.com/track/12345678",
			expected: true,
		},
		{
			name:     "Valid with browse path",
			url:      "https://tidal.com/browse/track/87654321",
			expected: true,
		},
		{
			name:     "Invalid - non-Tidal URL",
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
			url:      "not-a-url",
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

func TestTidalResolver_extractFromTitleTag(t *testing.T) {
	t.Helper()

	resolver := NewTidalResolver()

	tests := []struct {
		name           string
		html           string
		expectedTitle  string
		expectedArtist string
	}{
		{
			name:           "En dash separator with TIDAL suffix",
			html:           `<title>Never Gonna Give You Up – Rick Astley | TIDAL</title>`,
			expectedTitle:  "Never Gonna Give You Up",
			expectedArtist: "Rick Astley",
		},
		{
			name:           "Hyphen separator with TIDAL suffix",
			html:           `<title>Track Title - Artist Name | TIDAL</title>`,
			expectedTitle:  "Track Title",
			expectedArtist: "Artist Name",
		},
		{
			name:           "Without TIDAL suffix",
			html:           `<title>Track – Artist</title>`,
			expectedTitle:  "Track",
			expectedArtist: "Artist",
		},
		{
			name:           "No separator - title only",
			html:           `<title>Just Track Title | TIDAL</title>`,
			expectedTitle:  "Just Track Title",
			expectedArtist: "",
		},
		{
			name:           "No title tag",
			html:           `<html><body>No title</body></html>`,
			expectedTitle:  "",
			expectedArtist: "",
		},
		{
			name:           "Multiple title tags",
			html:           `<title>First – Artist | TIDAL</title><title>Second</title>`,
			expectedTitle:  "First",
			expectedArtist: "Artist",
		},
		{
			name:           "With extra whitespace between words",
			html:           `<title>Track  –  Artist | TIDAL</title>`,
			expectedTitle:  "Track",
			expectedArtist: "Artist",
		},
		{
			name:           "Mixed separators prefers en dash",
			html:           `<title>Track – Artist - More Text | TIDAL</title>`,
			expectedTitle:  "Track",
			expectedArtist: "Artist - More Text",
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

func TestTidalResolver_extractFromMetaTags(t *testing.T) {
	t.Helper()

	resolver := NewTidalResolver()

	tests := []struct {
		name           string
		html           string
		expectedTitle  string
		expectedArtist string
	}{
		{
			name: "With og:title and og:description",
			html: `<meta property="og:title" content="Never Gonna Give You Up">` +
				`<meta property="og:description" content="by Rick Astley">`,
			expectedTitle:  "Never Gonna Give You Up",
			expectedArtist: "Rick Astley",
		},
		{
			name:           "Only og:title",
			html:           `<meta property="og:title" content="Track Title">`,
			expectedTitle:  "Track Title",
			expectedArtist: "",
		},
		{
			name:           "Only og:description with by",
			html:           `<meta property="og:description" content="Song by Artist Name">`,
			expectedTitle:  "",
			expectedArtist: "Artist Name",
		},
		{
			name:           "No meta tags",
			html:           `<html><body>No meta tags</body></html>`,
			expectedTitle:  "",
			expectedArtist: "",
		},
		{
			name: "Description without by",
			html: `<meta property="og:title" content="Track">` +
				`<meta property="og:description" content="Just a description">`,
			expectedTitle:  "Track",
			expectedArtist: "",
		},
		{
			name: "Description with lowercase by",
			html: `<meta property="og:title" content="Track">` +
				`<meta property="og:description" content="by Artist Name">`,
			expectedTitle:  "Track",
			expectedArtist: "Artist Name",
		},
		{
			name: "Multiple og:title tags",
			html: `<meta property="og:title" content="First Title">` +
				`<meta property="og:title" content="Second Title">`,
			expectedTitle:  "First Title",
			expectedArtist: "",
		},
		{
			name: "Malformed meta tags",
			html: `<meta property="og:title">` +
				`<meta content="Track Title">`,
			expectedTitle:  "",
			expectedArtist: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			title, artist := resolver.extractFromMetaTags(tt.html)
			if title != tt.expectedTitle {
				t.Errorf("extractFromMetaTags() title = %q, want %q", title, tt.expectedTitle)
			}
			if artist != tt.expectedArtist {
				t.Errorf("extractFromMetaTags() artist = %q, want %q", artist, tt.expectedArtist)
			}
		})
	}
}
