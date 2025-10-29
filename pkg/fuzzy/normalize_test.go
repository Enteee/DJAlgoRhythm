package fuzzy

import (
	"testing"
	"time"
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

func TestNormalizer_NormalizeArtist(t *testing.T) {
	normalizer := NewNormalizer()

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "Simple artist name",
			input:    "The Beatles",
			expected: "the beatles",
		},
		{
			name:     "Artist with feat",
			input:    "Artist feat. Someone",
			expected: "artist feat. someone",
		},
		{
			name:     "Artist with and",
			input:    "Artist and Someone",
			expected: "artist & someone",
		},
		{
			name:     "Artist with vs",
			input:    "Artist vs Someone",
			expected: "artist vs. someone",
		},
		{
			name:     "Artist with punctuation",
			input:    "P!nk",
			expected: "p nk",
		},
		{
			name:     "Artist with accents",
			input:    "Björk",
			expected: "bjork",
		},
	}

	runStringTransformationTest(t, "NormalizeArtist", normalizer.NormalizeArtist, tests)
}

func TestNormalizer_NormalizeTitle(t *testing.T) {
	normalizer := NewNormalizer()

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "Simple title",
			input:    "Hey Jude",
			expected: "hey jude",
		},
		{
			name:     "Title with featuring",
			input:    "Song Title (feat. Artist)",
			expected: "song title",
		},
		{
			name:     "Title with remix",
			input:    "Song Title (Remix)",
			expected: "song title",
		},
		{
			name:     "Title with remaster",
			input:    "Song Title (Remastered)",
			expected: "song title",
		},
		{
			name:     "Title with version info",
			input:    "Song Title - Radio Edit",
			expected: "song title",
		},
		{
			name:     "Title with punctuation",
			input:    "Don't Stop Me Now!",
			expected: "don t stop me now",
		},
		{
			name:     "Title with multiple spaces",
			input:    "Song    Title",
			expected: "song title",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := normalizer.NormalizeTitle(tt.input)
			if result != tt.expected {
				t.Errorf("NormalizeTitle() = %q, want %q", result, tt.expected)
			}
		})
	}
}

func TestNormalizer_CalculateSimilarity(t *testing.T) {
	normalizer := NewNormalizer()
	tests := createSimilarityTestCases()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := normalizer.CalculateSimilarity(tt.s1, tt.s2)
			if abs64(result-tt.expected) > tt.delta {
				t.Errorf("CalculateSimilarity() = %f, want %f (±%f)", result, tt.expected, tt.delta)
			}
		})
	}
}

// similarityTestCase represents a test case for similarity calculation.
type similarityTestCase struct {
	name     string
	s1       string
	s2       string
	expected float64
	delta    float64
}

// createSimilarityTestCases creates all test cases for similarity calculation.
func createSimilarityTestCases() []similarityTestCase {
	return []similarityTestCase{
		{"Identical strings", "hello", "hello", 1.0, 0.0},
		{"Completely different strings", "hello", "world", 0.2, 0.1},
		{"Similar strings", "hello", "hallo", 0.8, 0.1},
		{"Empty strings", "", "", 1.0, 0.0},
		{"One empty string", "hello", "", 0.0, 0.0},
		{"Substring", "hello world", "hello", 0.45, 0.1},
	}
}

func TestNormalizer_DurationTolerance(t *testing.T) {
	normalizer := NewNormalizer()

	tests := []struct {
		name     string
		d1       time.Duration
		d2       time.Duration
		expected float64
		delta    float64
	}{
		{
			name:     "Identical durations",
			d1:       3 * time.Minute,
			d2:       3 * time.Minute,
			expected: 1.0,
			delta:    0.0,
		},
		{
			name:     "Within tolerance",
			d1:       3 * time.Minute,
			d2:       3*time.Minute + 20*time.Second,
			expected: 1.0,
			delta:    0.0,
		},
		{
			name:     "Just outside tolerance",
			d1:       3 * time.Minute,
			d2:       3*time.Minute + 40*time.Second,
			expected: 0.9,
			delta:    0.1,
		},
		{
			name:     "Very different durations",
			d1:       1 * time.Minute,
			d2:       5 * time.Minute,
			expected: 0.0,
			delta:    0.1,
		},
		{
			name:     "Negative difference",
			d1:       4 * time.Minute,
			d2:       3 * time.Minute,
			expected: 0.667,
			delta:    0.01,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := normalizer.DurationTolerance(tt.d1, tt.d2)
			if abs64(result-tt.expected) > tt.delta {
				t.Errorf("DurationTolerance() = %f, want %f (±%f)", result, tt.expected, tt.delta)
			}
		})
	}
}

func TestNormalizer_basicNormalize(t *testing.T) {
	normalizer := NewNormalizer()

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "Simple text",
			input:    "Hello World",
			expected: "hello world",
		},
		{
			name:     "Text with punctuation",
			input:    "Hello, World!",
			expected: "hello world",
		},
		{
			name:     "Text with accents",
			input:    "Café",
			expected: "cafe",
		},
		{
			name:     "Text with multiple spaces",
			input:    "Hello    World",
			expected: "hello world",
		},
		{
			name:     "Text with leading/trailing spaces",
			input:    "  Hello World  ",
			expected: "hello world",
		},
		{
			name:     "Mixed punctuation and spaces",
			input:    "Hello,  World!!!",
			expected: "hello world",
		},
	}

	runStringTransformationTest(t, "basicNormalize", normalizer.basicNormalize, tests)
}

func BenchmarkNormalizer_NormalizeArtist(b *testing.B) {
	normalizer := NewNormalizer()
	artist := "The Beatles feat. John Lennon & Paul McCartney"

	b.ResetTimer()
	for range b.N {
		normalizer.NormalizeArtist(artist)
	}
}

func BenchmarkNormalizer_NormalizeTitle(b *testing.B) {
	normalizer := NewNormalizer()
	title := "Hey Jude (Remastered 2009) [feat. Orchestra] - Radio Edit"

	b.ResetTimer()
	for range b.N {
		normalizer.NormalizeTitle(title)
	}
}

func BenchmarkNormalizer_CalculateSimilarity(b *testing.B) {
	normalizer := NewNormalizer()
	s1 := "hey jude remastered"
	s2 := "hey jude original"

	b.ResetTimer()
	for range b.N {
		normalizer.CalculateSimilarity(s1, s2)
	}
}

// Helper function for floating point comparison.
func abs64(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}
