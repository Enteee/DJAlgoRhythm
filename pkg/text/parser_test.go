package text

import (
	"testing"

	"djalgorhythm/internal/core"
)

// runStringTransformationTest is a helper to run tests for string transformation functions.
func runStringTransformationTest(t *testing.T, testName string,
	transformFunc func(string) string, testCases []struct {
		name     string
		input    string
		expected string
	}) {
	t.Helper()
	for _, tt := range testCases {
		t.Run(tt.name, func(t *testing.T) {
			result := transformFunc(tt.input)
			if result != tt.expected {
				t.Errorf("%s() = %q, want %q", testName, result, tt.expected)
			}
		})
	}
}

// runBooleanTest is a helper to run tests for boolean functions.
func runBooleanTest(t *testing.T, testName string,
	testFunc func(string) bool, testCases []struct {
		name     string
		input    string
		expected bool
	}) {
	t.Helper()
	for _, tt := range testCases {
		t.Run(tt.name, func(t *testing.T) {
			result := testFunc(tt.input)
			if result != tt.expected {
				t.Errorf("%s() = %v, want %v", testName, result, tt.expected)
			}
		})
	}
}

// getParseMessageTestData returns test cases for the ParseMessage function.
func getParseMessageTestData() []struct {
	name     string
	input    string
	expected core.MessageType
	urls     []string
} {
	return []struct {
		name     string
		input    string
		expected core.MessageType
		urls     []string
	}{
		{
			"Spotify track link",
			"Check this out: https://open.spotify.com/track/4uLU6hMCjMI75M1A2tKUQC",
			core.MessageTypeSpotifyLink,
			[]string{"https://open.spotify.com/track/4uLU6hMCjMI75M1A2tKUQC"},
		},
		{"Spotify URI", "spotify:track:4uLU6hMCjMI75M1A2tKUQC", core.MessageTypeSpotifyLink, []string{}},
		{
			"Spotify shortened link",
			"Check this out: https://spotify.link/ie2dPfjkzXb",
			core.MessageTypeSpotifyLink,
			[]string{"https://spotify.link/ie2dPfjkzXb"},
		},
		{
			"YouTube link",
			"https://www.youtube.com/watch?v=dQw4w9WgXcQ",
			core.MessageTypeNonSpotifyLink,
			[]string{"https://www.youtube.com/watch?v=dQw4w9WgXcQ"},
		},
		{
			"YouTube short link",
			"https://youtu.be/dQw4w9WgXcQ",
			core.MessageTypeNonSpotifyLink,
			[]string{"https://youtu.be/dQw4w9WgXcQ"},
		},
		{
			"Apple Music link",
			"https://music.apple.com/us/album/nevermind/1440783617",
			core.MessageTypeNonSpotifyLink,
			[]string{"https://music.apple.com/us/album/nevermind/1440783617"},
		},
		{"Free text song request", "play never gonna give you up by rick astley", core.MessageTypeFreeText, []string{}},
		{
			"Text with regular URL",
			"Check out this website: https://example.com",
			core.MessageTypeFreeText,
			[]string{"https://example.com"},
		},
		{"Empty message", "", core.MessageTypeFreeText, []string{}},
		{"Whitespace only", "   \n\t  ", core.MessageTypeFreeText, []string{}},
	}
}

func TestParser_ParseMessage(t *testing.T) {
	parser := NewParser()
	tests := getParseMessageTestData()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parser.ParseMessage(tt.input)

			if result.Type != tt.expected {
				t.Errorf("ParseMessage() type = %v, want %v", result.Type, tt.expected)
			}

			if len(result.URLs) != len(tt.urls) {
				t.Errorf("ParseMessage() URLs count = %d, want %d", len(result.URLs), len(tt.urls))
			}

			for i, url := range tt.urls {
				if i < len(result.URLs) && result.URLs[i] != url {
					t.Errorf("ParseMessage() URL[%d] = %s, want %s", i, result.URLs[i], url)
				}
			}
		})
	}
}

// getExtractSpotifyTrackIDTestData returns test cases for the ExtractSpotifyTrackID function.
func getExtractSpotifyTrackIDTestData() []struct {
	name     string
	input    string
	expected string
	hasError bool
} {
	return []struct {
		name     string
		input    string
		expected string
		hasError bool
	}{
		{"Standard Spotify URL", "https://open.spotify.com/track/4uLU6hMCjMI75M1A2tKUQC", "4uLU6hMCjMI75M1A2tKUQC", false},
		{
			"Spotify URL with query params",
			"https://open.spotify.com/track/4uLU6hMCjMI75M1A2tKUQC?si=abc123",
			"4uLU6hMCjMI75M1A2tKUQC",
			false,
		},
		{"Spotify URI format", "spotify:track:4uLU6hMCjMI75M1A2tKUQC", "4uLU6hMCjMI75M1A2tKUQC", false},
		{"Non-Spotify URL", "https://www.youtube.com/watch?v=dQw4w9WgXcQ", "", false},
		{"Invalid URL", "not-a-url", "", true},
		{"Spotify album URL", "https://open.spotify.com/album/1234567890", "", false},
	}
}

func TestParser_ExtractSpotifyTrackID(t *testing.T) {
	parser := NewParser()
	tests := getExtractSpotifyTrackIDTestData()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := parser.ExtractSpotifyTrackID(tt.input)

			if tt.hasError && err == nil {
				t.Errorf("ExtractSpotifyTrackID() expected error but got none")
			}

			if !tt.hasError && err != nil {
				t.Errorf("ExtractSpotifyTrackID() unexpected error: %v", err)
			}

			if result != tt.expected {
				t.Errorf("ExtractSpotifyTrackID() = %s, want %s", result, tt.expected)
			}
		})
	}
}

func TestParser_normalizeText(t *testing.T) {
	parser := NewParser()

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "Basic text",
			input:    "hello world",
			expected: "hello world",
		},
		{
			name:     "Multiple spaces",
			input:    "hello    world",
			expected: "hello world",
		},
		{
			name:     "Leading and trailing whitespace",
			input:    "  hello world  ",
			expected: "hello world",
		},
		{
			name:     "Multiple lines",
			input:    "hello\n\nworld\n",
			expected: "hello world",
		},
		{
			name:     "Mixed whitespace",
			input:    " hello \t\n world \r\n ",
			expected: "hello world",
		},
		{
			name:     "Empty lines",
			input:    "hello\n\n\nworld",
			expected: "hello world",
		},
	}

	runStringTransformationTest(t, "normalizeText", parser.normalizeText, tests)
}

func TestParser_cleanURL(t *testing.T) {
	parser := NewParser()

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "Clean URL",
			input:    "https://open.spotify.com/track/123",
			expected: "https://open.spotify.com/track/123",
		},
		{
			name:     "URL with UTM parameters",
			input:    "https://example.com?utm_source=test&utm_medium=social&other=keep",
			expected: "https://example.com?other=keep",
		},
		{
			name:     "URL with Spotify si parameter",
			input:    "https://open.spotify.com/track/123?si=abc123&other=keep",
			expected: "https://open.spotify.com/track/123?other=keep",
		},
		{
			name:     "URL with trailing punctuation",
			input:    "https://example.com!",
			expected: "https://example.com",
		},
		{
			name:     "URL with multiple trailing punctuation",
			input:    "https://example.com.,!?;",
			expected: "https://example.com",
		},
		{
			name:     "Invalid URL",
			input:    "not-a-url",
			expected: "",
		},
	}

	runStringTransformationTest(t, "cleanURL", parser.cleanURL, tests)
}

func TestParser_isSpotifyURL(t *testing.T) {
	parser := NewParser()

	tests := []struct {
		name     string
		input    string
		expected bool
	}{
		{
			name:     "Spotify track URL",
			input:    "https://open.spotify.com/track/123",
			expected: true,
		},
		{
			name:     "Spotify track URI",
			input:    "spotify:track:123",
			expected: true,
		},
		{
			name:     "Spotify shortened link",
			input:    "https://spotify.link/ie2dPfjkzXb",
			expected: true,
		},
		{
			name:     "Spotify album URL",
			input:    "https://open.spotify.com/album/123",
			expected: false,
		},
		{
			name:     "YouTube URL",
			input:    "https://www.youtube.com/watch?v=123",
			expected: false,
		},
		{
			name:     "Non-URL",
			input:    "hello world",
			expected: false,
		},
	}

	runBooleanTest(t, "isSpotifyURL", parser.isSpotifyURL, tests)
}

func TestParser_isMusicURL(t *testing.T) {
	parser := NewParser()

	tests := []struct {
		name     string
		input    string
		expected bool
	}{
		{
			name:     "YouTube URL",
			input:    "https://www.youtube.com/watch?v=123",
			expected: true,
		},
		{
			name:     "YouTube short URL",
			input:    "https://youtu.be/123",
			expected: true,
		},
		{
			name:     "Apple Music URL",
			input:    "https://music.apple.com/album/123",
			expected: true,
		},
		{
			name:     "SoundCloud URL",
			input:    "https://soundcloud.com/artist/track",
			expected: true,
		},
		{
			name:     "Spotify URL",
			input:    "https://open.spotify.com/track/123",
			expected: false,
		},
		{
			name:     "Regular website",
			input:    "https://example.com",
			expected: false,
		},
	}

	runBooleanTest(t, "isMusicURL", parser.isMusicURL, tests)
}
