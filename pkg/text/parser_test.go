package text

import (
	"testing"

	"whatdj/internal/core"
)

func TestParser_ParseMessage(t *testing.T) {
	parser := NewParser()

	tests := []struct {
		name     string
		input    string
		expected core.MessageType
		urls     []string
	}{
		{
			name:     "Spotify track link",
			input:    "Check this out: https://open.spotify.com/track/4uLU6hMCjMI75M1A2tKUQC",
			expected: core.MessageTypeSpotifyLink,
			urls:     []string{"https://open.spotify.com/track/4uLU6hMCjMI75M1A2tKUQC"},
		},
		{
			name:     "Spotify URI",
			input:    "spotify:track:4uLU6hMCjMI75M1A2tKUQC",
			expected: core.MessageTypeSpotifyLink,
			urls:     []string{},
		},
		{
			name:     "YouTube link",
			input:    "https://www.youtube.com/watch?v=dQw4w9WgXcQ",
			expected: core.MessageTypeNonSpotifyLink,
			urls:     []string{"https://www.youtube.com/watch?v=dQw4w9WgXcQ"},
		},
		{
			name:     "YouTube short link",
			input:    "https://youtu.be/dQw4w9WgXcQ",
			expected: core.MessageTypeNonSpotifyLink,
			urls:     []string{"https://youtu.be/dQw4w9WgXcQ"},
		},
		{
			name:     "Apple Music link",
			input:    "https://music.apple.com/us/album/nevermind/1440783617",
			expected: core.MessageTypeNonSpotifyLink,
			urls:     []string{"https://music.apple.com/us/album/nevermind/1440783617"},
		},
		{
			name:     "Free text song request",
			input:    "play never gonna give you up by rick astley",
			expected: core.MessageTypeFreeText,
			urls:     []string{},
		},
		{
			name:     "Text with regular URL",
			input:    "Check out this website: https://example.com",
			expected: core.MessageTypeFreeText,
			urls:     []string{"https://example.com"},
		},
		{
			name:     "Empty message",
			input:    "",
			expected: core.MessageTypeFreeText,
			urls:     []string{},
		},
		{
			name:     "Whitespace only",
			input:    "   \n\t  ",
			expected: core.MessageTypeFreeText,
			urls:     []string{},
		},
	}

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

func TestParser_ExtractSpotifyTrackID(t *testing.T) {
	parser := NewParser()

	tests := []struct {
		name     string
		input    string
		expected string
		hasError bool
	}{
		{
			name:     "Standard Spotify URL",
			input:    "https://open.spotify.com/track/4uLU6hMCjMI75M1A2tKUQC",
			expected: "4uLU6hMCjMI75M1A2tKUQC",
			hasError: false,
		},
		{
			name:     "Spotify URL with query params",
			input:    "https://open.spotify.com/track/4uLU6hMCjMI75M1A2tKUQC?si=abc123",
			expected: "4uLU6hMCjMI75M1A2tKUQC",
			hasError: false,
		},
		{
			name:     "Spotify URI format",
			input:    "spotify:track:4uLU6hMCjMI75M1A2tKUQC",
			expected: "4uLU6hMCjMI75M1A2tKUQC",
			hasError: false,
		},
		{
			name:     "Non-Spotify URL",
			input:    "https://www.youtube.com/watch?v=dQw4w9WgXcQ",
			expected: "",
			hasError: false,
		},
		{
			name:     "Invalid URL",
			input:    "not-a-url",
			expected: "",
			hasError: true,
		},
		{
			name:     "Spotify album URL",
			input:    "https://open.spotify.com/album/1234567890",
			expected: "",
			hasError: false,
		},
	}

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

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parser.normalizeText(tt.input)

			if result != tt.expected {
				t.Errorf("normalizeText() = %q, want %q", result, tt.expected)
			}
		})
	}
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

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parser.cleanURL(tt.input)

			if result != tt.expected {
				t.Errorf("cleanURL() = %q, want %q", result, tt.expected)
			}
		})
	}
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

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parser.isSpotifyURL(tt.input)

			if result != tt.expected {
				t.Errorf("isSpotifyURL() = %v, want %v", result, tt.expected)
			}
		})
	}
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

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parser.isMusicURL(tt.input)

			if result != tt.expected {
				t.Errorf("isMusicURL() = %v, want %v", result, tt.expected)
			}
		})
	}
}
