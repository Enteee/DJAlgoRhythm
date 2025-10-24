package llm

import (
	"reflect"
	"testing"

	"go.uber.org/zap"

	"djalgorhythm/internal/core"
)

func TestParseTrackRanking(t *testing.T) {
	// Create a no-op logger for tests
	logger := zap.NewNop()

	// Sample tracks for testing
	sampleTracks := []core.Track{
		{ID: "track1", Artist: "Artist 1", Title: "Song 1"},
		{ID: "track2", Artist: "Artist 2", Title: "Song 2"},
		{ID: "track3", Artist: "Artist 3", Title: "Song 3"},
		{ID: "track4", Artist: "Artist 4", Title: "Song 4"},
		{ID: "track5", Artist: "Artist 5", Title: "Song 5"},
	}

	tests := []struct {
		name           string
		rankingText    string
		originalTracks []core.Track
		expected       []core.Track
	}{
		{
			name:           "Valid ranking - normal order",
			rankingText:    "3,1,5,2,4",
			originalTracks: sampleTracks,
			expected: []core.Track{
				sampleTracks[2], // track3 (index 2, rank 3)
				sampleTracks[0], // track1 (index 0, rank 1)
				sampleTracks[4], // track5 (index 4, rank 5)
				sampleTracks[1], // track2 (index 1, rank 2)
				sampleTracks[3], // track4 (index 3, rank 4)
			},
		},
		{
			name:           "Valid ranking with spaces",
			rankingText:    "2, 1, 3",
			originalTracks: sampleTracks[:3],
			expected: []core.Track{
				sampleTracks[1], // track2
				sampleTracks[0], // track1
				sampleTracks[2], // track3
			},
		},
		{
			name:           "Partial ranking - some tracks missing",
			rankingText:    "3,1",
			originalTracks: sampleTracks[:4],
			expected: []core.Track{
				sampleTracks[2], // track3 (ranked)
				sampleTracks[0], // track1 (ranked)
				sampleTracks[1], // track2 (fallback)
				sampleTracks[3], // track4 (fallback)
			},
		},
		{
			name:           "Invalid numbers - fallback to original",
			rankingText:    "10,20,30",
			originalTracks: sampleTracks[:3],
			expected:       sampleTracks[:3], // fallback to original order
		},
		{
			name:           "Mixed valid and invalid numbers",
			rankingText:    "2,10,1,30",
			originalTracks: sampleTracks[:3],
			expected: []core.Track{
				sampleTracks[1], // track2 (valid rank 2)
				sampleTracks[0], // track1 (valid rank 1)
				sampleTracks[2], // track3 (fallback)
			},
		},
		{
			name:           "Duplicate numbers - first occurrence wins",
			rankingText:    "1,2,1,3",
			originalTracks: sampleTracks[:4],
			expected: []core.Track{
				sampleTracks[0], // track1 (first occurrence of rank 1)
				sampleTracks[1], // track2
				sampleTracks[2], // track3
				sampleTracks[3], // track4 (fallback)
			},
		},
		{
			name:           "Zero and negative numbers - ignored",
			rankingText:    "0,-1,2,1",
			originalTracks: sampleTracks[:3],
			expected: []core.Track{
				sampleTracks[1], // track2 (rank 2)
				sampleTracks[0], // track1 (rank 1)
				sampleTracks[2], // track3 (fallback)
			},
		},
		{
			name:           "Non-numeric input - fallback to original",
			rankingText:    "abc,def,ghi",
			originalTracks: sampleTracks[:3],
			expected:       sampleTracks[:3],
		},
		{
			name:           "Empty ranking text - fallback to original",
			rankingText:    "",
			originalTracks: sampleTracks[:3],
			expected:       sampleTracks[:3],
		},
		{
			name:           "Just commas - fallback to original",
			rankingText:    ",,,",
			originalTracks: sampleTracks[:3],
			expected:       sampleTracks[:3],
		},
		{
			name:           "Single valid number",
			rankingText:    "2",
			originalTracks: sampleTracks[:3],
			expected: []core.Track{
				sampleTracks[1], // track2 (ranked)
				sampleTracks[0], // track1 (fallback)
				sampleTracks[2], // track3 (fallback)
			},
		},
		{
			name:           "Perfect reverse order",
			rankingText:    "3,2,1",
			originalTracks: sampleTracks[:3],
			expected: []core.Track{
				sampleTracks[2], // track3
				sampleTracks[1], // track2
				sampleTracks[0], // track1
			},
		},
		{
			name:           "Empty track list",
			rankingText:    "1,2,3",
			originalTracks: []core.Track{},
			expected:       []core.Track{},
		},
		{
			name:           "Single track",
			rankingText:    "1",
			originalTracks: sampleTracks[:1],
			expected:       sampleTracks[:1],
		},
		{
			name:           "Numbers with extra characters - should be ignored",
			rankingText:    "2a,1b,3c",
			originalTracks: sampleTracks[:3],
			expected:       sampleTracks[:3], // fallback due to invalid format
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parseTrackRanking(tt.rankingText, tt.originalTracks, logger)

			if !reflect.DeepEqual(result, tt.expected) {
				t.Errorf("parseTrackRanking() = %+v, expected %+v", result, tt.expected)
			}
		})
	}
}

func TestParseTrackRanking_LengthConsistency(t *testing.T) {
	logger := zap.NewNop()

	sampleTracks := []core.Track{
		{ID: "track1", Artist: "Artist 1", Title: "Song 1"},
		{ID: "track2", Artist: "Artist 2", Title: "Song 2"},
		{ID: "track3", Artist: "Artist 3", Title: "Song 3"},
	}

	tests := []struct {
		name        string
		rankingText string
	}{
		{"Valid ranking", "3,1,2"},
		{"Partial ranking", "2,1"},
		{"Invalid ranking", "abc,def"},
		{"Empty ranking", ""},
		{"Out of bounds", "10,20,30"},
		{"Mixed valid/invalid", "1,abc,2"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parseTrackRanking(tt.rankingText, sampleTracks, logger)

			if len(result) != len(sampleTracks) {
				t.Errorf("parseTrackRanking() returned %d tracks, expected %d", len(result), len(sampleTracks))
			}
		})
	}
}
