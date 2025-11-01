package musiclink

import (
	"testing"
)

//nolint:dupl // CanResolve tests intentionally follow same pattern across all resolvers for consistency.
func TestAmazonMusicResolver_CanResolve(t *testing.T) {
	t.Helper()

	resolver := NewAmazonMusicResolver()

	tests := []struct {
		name     string
		url      string
		expected bool
	}{
		{
			name:     "Valid Amazon Music US URL",
			url:      "https://music.amazon.com/albums/B08X123456",
			expected: true,
		},
		{
			name:     "Valid Amazon Music UK URL",
			url:      "https://music.amazon.co.uk/albums/B08X123456",
			expected: true,
		},
		{
			name:     "Valid Amazon Music DE URL",
			url:      "https://music.amazon.de/albums/B08X123456",
			expected: true,
		},
		{
			name:     "Valid Amazon Music with tracks",
			url:      "https://music.amazon.com/tracks/B08X123456",
			expected: true,
		},
		{
			name:     "Invalid - regular Amazon not music subdomain",
			url:      "https://amazon.com/product/123",
			expected: false,
		},
		{
			name:     "Invalid - www.amazon.com not music",
			url:      "https://www.amazon.com/music/player",
			expected: false,
		},
		{
			name:     "Invalid - non-Amazon URL",
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

func TestAmazonMusicResolver_extractArtistFromDescription(t *testing.T) {
	t.Helper()

	resolver := NewAmazonMusicResolver()

	tests := []struct {
		name     string
		desc     string
		expected string
	}{
		{
			name:     "Standard format with on Amazon Music",
			desc:     "Never Gonna Give You Up by Rick Astley on Amazon Music",
			expected: "Rick Astley",
		},
		{
			name:     "Without on Amazon Music suffix",
			desc:     "Some Song by Artist Name",
			expected: "Artist Name",
		},
		{
			name:     "With extra text after on Amazon Music",
			desc:     "Track by Artist on Amazon Music and more text",
			expected: "Artist",
		},
		{
			name:     "No by separator",
			desc:     "Just some description text",
			expected: "",
		},
		{
			name:     "Multiple by occurrences",
			desc:     "Song by Artist by Another Person on Amazon Music",
			expected: "Artist by Another Person",
		},
		{
			name:     "Empty description",
			desc:     "",
			expected: "",
		},
		{
			name:     "Whitespace trimming",
			desc:     "Track by   Artist Name   on Amazon Music",
			expected: "Artist Name",
		},
		{
			name:     "Case insensitive by (uppercase BY)",
			desc:     "Track BY Artist Name on Amazon Music",
			expected: "Artist Name",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := resolver.extractArtistFromDescription(tt.desc)
			if result != tt.expected {
				t.Errorf("extractArtistFromDescription() = %q, want %q", result, tt.expected)
			}
		})
	}
}

func TestAmazonMusicResolver_extractFromTitleTag(t *testing.T) {
	t.Helper()

	resolver := NewAmazonMusicResolver()

	tests := []struct {
		name           string
		html           string
		expectedTitle  string
		expectedArtist string
	}{
		{
			name:           "Standard Amazon Music format",
			html:           `<title>Never Gonna Give You Up by Rick Astley on Amazon Music</title>`,
			expectedTitle:  "Never Gonna Give You Up",
			expectedArtist: "Rick Astley",
		},
		{
			name:           "Without on Amazon Music suffix",
			html:           `<title>Track Title by Artist Name</title>`,
			expectedTitle:  "Track Title",
			expectedArtist: "Artist Name",
		},
		{
			name:           "No by separator",
			html:           `<title>Just Track Title on Amazon Music</title>`,
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
			html:           `<title>First Title by First Artist</title><title>Second</title>`,
			expectedTitle:  "First Title",
			expectedArtist: "First Artist",
		},
		{
			name:           "With extra whitespace between words",
			html:           `<title>Track  by  Artist on Amazon Music</title>`,
			expectedTitle:  "Track",
			expectedArtist: "Artist",
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

func TestAmazonMusicResolver_extractFromMetaTags(t *testing.T) {
	t.Helper()

	resolver := NewAmazonMusicResolver()

	tests := []struct {
		name           string
		html           string
		expectedTitle  string
		expectedArtist string
	}{
		{
			name: "With og:title and og:description",
			html: `<meta property="og:title" content="Never Gonna Give You Up">` +
				`<meta property="og:description" content="Song by Rick Astley on Amazon Music">`,
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
			name:           "Only og:description",
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
