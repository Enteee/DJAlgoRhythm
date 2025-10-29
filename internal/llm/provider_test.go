package llm

import (
	"reflect"
	"testing"

	"go.uber.org/zap"

	"djalgorhythm/internal/core"
)

func TestParseTrackRanking(t *testing.T) {
	logger := zap.NewNop()
	sampleTracks := createSampleTracks()
	tests := createTrackRankingTestCases(sampleTracks)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runTrackRankingTest(t, logger, &tt)
		})
	}
}

// createSampleTracks creates sample tracks for testing.
func createSampleTracks() []core.Track {
	return []core.Track{
		{ID: "track1", Artist: "Artist 1", Title: "Song 1"},
		{ID: "track2", Artist: "Artist 2", Title: "Song 2"},
		{ID: "track3", Artist: "Artist 3", Title: "Song 3"},
		{ID: "track4", Artist: "Artist 4", Title: "Song 4"},
		{ID: "track5", Artist: "Artist 5", Title: "Song 5"},
	}
}

// trackRankingTestCase represents a test case for track ranking.
type trackRankingTestCase struct {
	name           string
	rankingText    string
	originalTracks []core.Track
	expected       []core.Track
}

// createTrackRankingTestCases creates test cases (simplified version).
func createTrackRankingTestCases(sampleTracks []core.Track) []trackRankingTestCase {
	return []trackRankingTestCase{
		{"Valid ranking", "3,1,2", sampleTracks[:3],
			[]core.Track{sampleTracks[2], sampleTracks[0], sampleTracks[1]}},
		{"Empty ranking", "", sampleTracks[:3], sampleTracks[:3]},
		{"Invalid numbers", "10,20,30", sampleTracks[:3], sampleTracks[:3]},
	}
}

// runTrackRankingTest executes a single track ranking test case.
func runTrackRankingTest(t *testing.T, logger *zap.Logger, tt *trackRankingTestCase) {
	t.Helper()
	result := parseTrackRanking(tt.rankingText, tt.originalTracks, logger)
	if !reflect.DeepEqual(result, tt.expected) {
		t.Errorf("parseTrackRanking() = %+v, expected %+v", result, tt.expected)
	}
}

func TestParseTrackRanking_LengthConsistency(t *testing.T) {
	logger := zap.NewNop()
	sampleTracks := createSampleTracks()[:3]
	testCases := []string{"3,1,2", "2,1", "abc,def", "", "10,20,30", "1,abc,2"}

	for _, rankingText := range testCases {
		t.Run("Ranking_"+rankingText, func(t *testing.T) {
			result := parseTrackRanking(rankingText, sampleTracks, logger)
			if len(result) != len(sampleTracks) {
				t.Errorf("parseTrackRanking() returned %d tracks, expected %d",
					len(result), len(sampleTracks))
			}
		})
	}
}
