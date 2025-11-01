package musiclink

import (
	"testing"
)

func TestExtractTitleAndArtistFromTitleTag_StandardCases(t *testing.T) {
	t.Helper()

	tests := []struct {
		name           string
		html           string
		serviceSuffix  string
		separator      string
		expectedTitle  string
		expectedArtist string
	}{
		{
			name:           "Standard format with suffix and separator",
			html:           `<title>Never Gonna Give You Up by Rick Astley on YouTube</title>`,
			serviceSuffix:  " on YouTube",
			separator:      " by ",
			expectedTitle:  "Never Gonna Give You Up",
			expectedArtist: "Rick Astley",
		},
		{
			name:           "With service suffix but no separator",
			html:           `<title>Some Track Name on Spotify</title>`,
			serviceSuffix:  " on Spotify",
			separator:      " by ",
			expectedTitle:  "Some Track Name",
			expectedArtist: "",
		},
		{
			name:           "No service suffix",
			html:           `<title>Track Title by Artist Name</title>`,
			serviceSuffix:  "",
			separator:      " by ",
			expectedTitle:  "Track Title",
			expectedArtist: "Artist Name",
		},
		{
			name:           "No separator provided",
			html:           `<title>Just a Title</title>`,
			serviceSuffix:  "",
			separator:      "",
			expectedTitle:  "Just a Title",
			expectedArtist: "",
		},
		{
			name:           "Multiple spaces should be trimmed",
			html:           `<title>  Track Name  by  Artist Name  on Service  </title>`,
			serviceSuffix:  "  on Service  ",
			separator:      " by ",
			expectedTitle:  "Track Name",
			expectedArtist: "Artist Name",
		},
		{
			name:           "Title tag with multiple separators",
			html:           `<title>Artist by Someone by Another on Service</title>`,
			serviceSuffix:  " on Service",
			separator:      " by ",
			expectedTitle:  "Artist",
			expectedArtist: "Someone by Another",
		},
		{
			name:           "Separator not present in title",
			html:           `<title>Track Title on Service</title>`,
			serviceSuffix:  " on Service",
			separator:      " by ",
			expectedTitle:  "Track Title",
			expectedArtist: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			title, artist := extractTitleAndArtistFromTitleTag(tt.html, tt.serviceSuffix, tt.separator)
			if title != tt.expectedTitle {
				t.Errorf("extractTitleAndArtistFromTitleTag() title = %q, want %q", title, tt.expectedTitle)
			}
			if artist != tt.expectedArtist {
				t.Errorf("extractTitleAndArtistFromTitleTag() artist = %q, want %q", artist, tt.expectedArtist)
			}
		})
	}
}

func TestExtractTitleAndArtistFromTitleTag_EdgeCases(t *testing.T) {
	t.Helper()

	tests := []struct {
		name           string
		html           string
		serviceSuffix  string
		separator      string
		expectedTitle  string
		expectedArtist string
	}{
		{
			name:           "No title tag found",
			html:           `<html><body>No title here</body></html>`,
			serviceSuffix:  " on Service",
			separator:      " by ",
			expectedTitle:  "",
			expectedArtist: "",
		},
		{
			name:           "Empty HTML",
			html:           ``,
			serviceSuffix:  " on Service",
			separator:      " by ",
			expectedTitle:  "",
			expectedArtist: "",
		},
		{
			name:           "Multiple title tags takes first",
			html:           `<title>First Title by First Artist</title><title>Second Title</title>`,
			serviceSuffix:  "",
			separator:      " by ",
			expectedTitle:  "First Title",
			expectedArtist: "First Artist",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			title, artist := extractTitleAndArtistFromTitleTag(tt.html, tt.serviceSuffix, tt.separator)
			if title != tt.expectedTitle {
				t.Errorf("extractTitleAndArtistFromTitleTag() title = %q, want %q", title, tt.expectedTitle)
			}
			if artist != tt.expectedArtist {
				t.Errorf("extractTitleAndArtistFromTitleTag() artist = %q, want %q", artist, tt.expectedArtist)
			}
		})
	}
}
