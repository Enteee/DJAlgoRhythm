package core

import (
	"testing"
)

// getExactMatchTestData returns test cases for the exact match function.
func getExactMatchTestData() []struct {
	name     string
	track1   *Track
	track2   *Track
	expected bool
} {
	return []struct {
		name     string
		track1   *Track
		track2   *Track
		expected bool
	}{
		{
			"Exact match - same artist and title",
			&Track{Artist: "The Beatles", Title: "Yesterday"},
			&Track{Artist: "The Beatles", Title: "Yesterday"},
			true,
		},
		{
			"Different artist",
			&Track{Artist: "The Beatles", Title: "Yesterday"},
			&Track{Artist: "The Rolling Stones", Title: "Yesterday"},
			false,
		},
		{
			"Different title",
			&Track{Artist: "The Beatles", Title: "Yesterday"},
			&Track{Artist: "The Beatles", Title: "Hey Jude"},
			false,
		},
		{
			"Case sensitive - different case",
			&Track{Artist: "The Beatles", Title: "Yesterday"},
			&Track{Artist: "the beatles", Title: "yesterday"},
			false,
		},
		{"Empty strings", &Track{Artist: "", Title: ""}, &Track{Artist: "", Title: ""}, true},
		{"One empty, one filled", &Track{Artist: "", Title: ""}, &Track{Artist: "Artist", Title: "Title"}, false},
	}
}

func TestDispatcher_isExactMatch(t *testing.T) {
	d := &Dispatcher{}
	tests := getExactMatchTestData()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := d.isExactMatch(tt.track1, tt.track2)
			if result != tt.expected {
				t.Errorf("isExactMatch() = %v, expected %v", result, tt.expected)
			}
		})
	}
}

// getCaseInsensitiveMatchTestData returns test cases for the case insensitive match function.
func getCaseInsensitiveMatchTestData() []struct {
	name     string
	track1   *Track
	track2   *Track
	expected bool
} {
	return []struct {
		name     string
		track1   *Track
		track2   *Track
		expected bool
	}{
		{
			"Case insensitive match - different cases",
			&Track{Artist: "The Beatles", Title: "Yesterday"},
			&Track{Artist: "the beatles", Title: "yesterday"},
			true,
		},
		{
			"Case insensitive match - mixed cases",
			&Track{Artist: "ThE BeAtLeS", Title: "YeStErDaY"},
			&Track{Artist: "tHe BeAtLeS", Title: "yEsTeRdAy"},
			true,
		},
		{
			"Different artists (case insensitive)",
			&Track{Artist: "The Beatles", Title: "Yesterday"},
			&Track{Artist: "the rolling stones", Title: "yesterday"},
			false,
		},
		{
			"Different titles (case insensitive)",
			&Track{Artist: "The Beatles", Title: "Yesterday"},
			&Track{Artist: "the beatles", Title: "hey jude"},
			false,
		},
		{"Unicode characters", &Track{Artist: "Björk", Title: "Jóga"}, &Track{Artist: "björk", Title: "jóga"}, true},
		{"Empty strings case insensitive", &Track{Artist: "", Title: ""}, &Track{Artist: "", Title: ""}, true},
	}
}

func TestDispatcher_isCaseInsensitiveMatch(t *testing.T) {
	d := &Dispatcher{}
	tests := getCaseInsensitiveMatchTestData()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := d.isCaseInsensitiveMatch(tt.track1, tt.track2)
			if result != tt.expected {
				t.Errorf("isCaseInsensitiveMatch() = %v, expected %v", result, tt.expected)
			}
		})
	}
}

// getPartialMatchTestData returns test cases for the partial match function.
func getPartialMatchTestData() []struct {
	name     string
	track1   *Track
	track2   *Track
	expected bool
} {
	return []struct {
		name     string
		track1   *Track
		track2   *Track
		expected bool
	}{
		{
			"Partial match - artist contains",
			&Track{Artist: "The Beatles", Title: "Yesterday"},
			&Track{Artist: "Beatles", Title: "Yesterday"},
			true,
		},
		{
			"Partial match - title contains",
			&Track{Artist: "The Beatles", Title: "Yesterday (Remastered)"},
			&Track{Artist: "The Beatles", Title: "Yesterday"},
			true,
		},
		{
			"Partial match - both directions",
			&Track{Artist: "Beatles", Title: "Yesterday"},
			&Track{Artist: "The Beatles", Title: "Yesterday (Remastered)"},
			true,
		},
		{
			"No partial match - completely different",
			&Track{Artist: "The Beatles", Title: "Yesterday"},
			&Track{Artist: "The Rolling Stones", Title: "Paint It Black"},
			false,
		},
		{
			"Partial match with case differences",
			&Track{Artist: "The Beatles", Title: "Yesterday"},
			&Track{Artist: "beatles", Title: "yesterday"},
			true,
		},
		{"Edge case - empty vs non-empty", &Track{Artist: "", Title: ""}, &Track{Artist: "Artist", Title: "Title"}, true},
		{"Edge case - both empty", &Track{Artist: "", Title: ""}, &Track{Artist: "", Title: ""}, true},
		{"Single character matches", &Track{Artist: "A", Title: "B"}, &Track{Artist: "ABC", Title: "BCD"}, true},
	}
}

func TestDispatcher_isPartialMatch(t *testing.T) {
	d := &Dispatcher{}
	tests := getPartialMatchTestData()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := d.isPartialMatch(tt.track1, tt.track2)
			if result != tt.expected {
				t.Errorf("isPartialMatch() = %v, expected %v", result, tt.expected)
			}
		})
	}
}
