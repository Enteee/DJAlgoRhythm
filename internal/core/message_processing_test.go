package core

import (
	"testing"
)

func TestDispatcher_isExactMatch(t *testing.T) {
	// Create a minimal dispatcher instance for method calls
	d := &Dispatcher{}

	tests := []struct {
		name     string
		track1   *Track
		track2   *Track
		expected bool
	}{
		{
			name: "Exact match - same artist and title",
			track1: &Track{
				Artist: "The Beatles",
				Title:  "Yesterday",
			},
			track2: &Track{
				Artist: "The Beatles",
				Title:  "Yesterday",
			},
			expected: true,
		},
		{
			name: "Different artist",
			track1: &Track{
				Artist: "The Beatles",
				Title:  "Yesterday",
			},
			track2: &Track{
				Artist: "The Rolling Stones",
				Title:  "Yesterday",
			},
			expected: false,
		},
		{
			name: "Different title",
			track1: &Track{
				Artist: "The Beatles",
				Title:  "Yesterday",
			},
			track2: &Track{
				Artist: "The Beatles",
				Title:  "Hey Jude",
			},
			expected: false,
		},
		{
			name: "Case sensitive - different case",
			track1: &Track{
				Artist: "The Beatles",
				Title:  "Yesterday",
			},
			track2: &Track{
				Artist: "the beatles",
				Title:  "yesterday",
			},
			expected: false,
		},
		{
			name: "Empty strings",
			track1: &Track{
				Artist: "",
				Title:  "",
			},
			track2: &Track{
				Artist: "",
				Title:  "",
			},
			expected: true,
		},
		{
			name: "One empty, one filled",
			track1: &Track{
				Artist: "",
				Title:  "",
			},
			track2: &Track{
				Artist: "Artist",
				Title:  "Title",
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := d.isExactMatch(tt.track1, tt.track2)
			if result != tt.expected {
				t.Errorf("isExactMatch() = %v, expected %v", result, tt.expected)
			}
		})
	}
}

func TestDispatcher_isCaseInsensitiveMatch(t *testing.T) {
	d := &Dispatcher{}

	tests := []struct {
		name     string
		track1   *Track
		track2   *Track
		expected bool
	}{
		{
			name: "Case insensitive match - different cases",
			track1: &Track{
				Artist: "The Beatles",
				Title:  "Yesterday",
			},
			track2: &Track{
				Artist: "the beatles",
				Title:  "yesterday",
			},
			expected: true,
		},
		{
			name: "Case insensitive match - mixed cases",
			track1: &Track{
				Artist: "ThE BeAtLeS",
				Title:  "YeStErDaY",
			},
			track2: &Track{
				Artist: "tHe BeAtLeS",
				Title:  "yEsTeRdAy",
			},
			expected: true,
		},
		{
			name: "Different artists (case insensitive)",
			track1: &Track{
				Artist: "The Beatles",
				Title:  "Yesterday",
			},
			track2: &Track{
				Artist: "the rolling stones",
				Title:  "yesterday",
			},
			expected: false,
		},
		{
			name: "Different titles (case insensitive)",
			track1: &Track{
				Artist: "The Beatles",
				Title:  "Yesterday",
			},
			track2: &Track{
				Artist: "the beatles",
				Title:  "hey jude",
			},
			expected: false,
		},
		{
			name: "Unicode characters",
			track1: &Track{
				Artist: "Björk",
				Title:  "Jóga",
			},
			track2: &Track{
				Artist: "björk",
				Title:  "jóga",
			},
			expected: true,
		},
		{
			name: "Empty strings case insensitive",
			track1: &Track{
				Artist: "",
				Title:  "",
			},
			track2: &Track{
				Artist: "",
				Title:  "",
			},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := d.isCaseInsensitiveMatch(tt.track1, tt.track2)
			if result != tt.expected {
				t.Errorf("isCaseInsensitiveMatch() = %v, expected %v", result, tt.expected)
			}
		})
	}
}

func TestDispatcher_isPartialMatch(t *testing.T) {
	d := &Dispatcher{}

	tests := []struct {
		name     string
		track1   *Track
		track2   *Track
		expected bool
	}{
		{
			name: "Partial match - artist contains",
			track1: &Track{
				Artist: "The Beatles",
				Title:  "Yesterday",
			},
			track2: &Track{
				Artist: "Beatles",
				Title:  "Yesterday",
			},
			expected: true,
		},
		{
			name: "Partial match - title contains",
			track1: &Track{
				Artist: "The Beatles",
				Title:  "Yesterday (Remastered)",
			},
			track2: &Track{
				Artist: "The Beatles",
				Title:  "Yesterday",
			},
			expected: true,
		},
		{
			name: "Partial match - both directions",
			track1: &Track{
				Artist: "Beatles",
				Title:  "Yesterday",
			},
			track2: &Track{
				Artist: "The Beatles",
				Title:  "Yesterday (Remastered)",
			},
			expected: true,
		},
		{
			name: "No partial match - completely different",
			track1: &Track{
				Artist: "The Beatles",
				Title:  "Yesterday",
			},
			track2: &Track{
				Artist: "The Rolling Stones",
				Title:  "Paint It Black",
			},
			expected: false,
		},
		{
			name: "Partial match with case differences",
			track1: &Track{
				Artist: "The Beatles",
				Title:  "Yesterday",
			},
			track2: &Track{
				Artist: "beatles",
				Title:  "yesterday",
			},
			expected: true,
		},
		{
			name: "Edge case - empty vs non-empty",
			track1: &Track{
				Artist: "",
				Title:  "",
			},
			track2: &Track{
				Artist: "Artist",
				Title:  "Title",
			},
			expected: true, // strings.Contains("", "") returns true, and strings.Contains("artist", "") returns true
		},
		{
			name: "Edge case - both empty",
			track1: &Track{
				Artist: "",
				Title:  "",
			},
			track2: &Track{
				Artist: "",
				Title:  "",
			},
			expected: true,
		},
		{
			name: "Single character matches",
			track1: &Track{
				Artist: "A",
				Title:  "B",
			},
			track2: &Track{
				Artist: "ABC",
				Title:  "BCD",
			},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := d.isPartialMatch(tt.track1, tt.track2)
			if result != tt.expected {
				t.Errorf("isPartialMatch() = %v, expected %v", result, tt.expected)
			}
		})
	}
}
