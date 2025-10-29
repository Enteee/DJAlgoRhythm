package core

import (
	"testing"
)

func TestPlaybackCompliance_IsOptimalForAutoDJ(t *testing.T) {
	tests := createPlaybackComplianceTestCases()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.compliance.IsOptimalForAutoDJ()
			if result != tt.expectedOptimal {
				t.Errorf("IsOptimalForAutoDJ() = %v, expected %v", result, tt.expectedOptimal)
			}
		})
	}
}

// playbackComplianceTestCase represents a test case for playback compliance.
type playbackComplianceTestCase struct {
	name            string
	compliance      PlaybackCompliance
	expectedOptimal bool
}

// createPlaybackComplianceTestCases creates all test cases for playback compliance.
func createPlaybackComplianceTestCases() []playbackComplianceTestCase {
	return []playbackComplianceTestCase{
		createOptimalSettingsTestCase(),
		createShuffleIncorrectTestCase(),
		createRepeatIncorrectTestCase(),
		createBothIncorrectTestCase(),
		createOptimalWithIssuesTestCase(),
		createDefaultZeroValuesTestCase(),
	}
}

// createOptimalSettingsTestCase creates test case for optimal settings.
func createOptimalSettingsTestCase() playbackComplianceTestCase {
	return playbackComplianceTestCase{
		name: "Optimal settings - both shuffle and repeat correct",
		compliance: PlaybackCompliance{
			IsCorrectShuffle: true,
			IsCorrectRepeat:  true,
			Issues:           []string{},
		},
		expectedOptimal: true,
	}
}

// createShuffleIncorrectTestCase creates test case for incorrect shuffle.
func createShuffleIncorrectTestCase() playbackComplianceTestCase {
	return playbackComplianceTestCase{
		name: "Non-optimal - shuffle incorrect",
		compliance: PlaybackCompliance{
			IsCorrectShuffle: false,
			IsCorrectRepeat:  true,
			Issues:           []string{"Shuffle is disabled"},
		},
		expectedOptimal: false,
	}
}

// createRepeatIncorrectTestCase creates test case for incorrect repeat.
func createRepeatIncorrectTestCase() playbackComplianceTestCase {
	return playbackComplianceTestCase{
		name: "Non-optimal - repeat incorrect",
		compliance: PlaybackCompliance{
			IsCorrectShuffle: true,
			IsCorrectRepeat:  false,
			Issues:           []string{"Repeat mode is wrong"},
		},
		expectedOptimal: false,
	}
}

// createBothIncorrectTestCase creates test case for both settings incorrect.
func createBothIncorrectTestCase() playbackComplianceTestCase {
	return playbackComplianceTestCase{
		name: "Non-optimal - both incorrect",
		compliance: PlaybackCompliance{
			IsCorrectShuffle: false,
			IsCorrectRepeat:  false,
			Issues:           []string{"Shuffle is disabled", "Repeat mode is wrong"},
		},
		expectedOptimal: false,
	}
}

// createOptimalWithIssuesTestCase creates test case for optimal settings with issues.
func createOptimalWithIssuesTestCase() playbackComplianceTestCase {
	return playbackComplianceTestCase{
		name: "Optimal with issues list (issues don't affect result)",
		compliance: PlaybackCompliance{
			IsCorrectShuffle: true,
			IsCorrectRepeat:  true,
			Issues:           []string{"Warning: Device volume low"},
		},
		expectedOptimal: true,
	}
}

// createDefaultZeroValuesTestCase creates test case for default zero values.
func createDefaultZeroValuesTestCase() playbackComplianceTestCase {
	return playbackComplianceTestCase{
		name:            "Default zero values",
		compliance:      PlaybackCompliance{},
		expectedOptimal: false,
	}
}
