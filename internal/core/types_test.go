package core

import (
	"testing"
)

func TestPlaybackCompliance_IsOptimalForAutoDJ(t *testing.T) {
	tests := []struct {
		name            string
		compliance      PlaybackCompliance
		expectedOptimal bool
	}{
		{
			name: "Optimal settings - both shuffle and repeat correct",
			compliance: PlaybackCompliance{
				IsCorrectShuffle: true,
				IsCorrectRepeat:  true,
				Issues:           []string{},
			},
			expectedOptimal: true,
		},
		{
			name: "Non-optimal - shuffle incorrect",
			compliance: PlaybackCompliance{
				IsCorrectShuffle: false,
				IsCorrectRepeat:  true,
				Issues:           []string{"Shuffle is disabled"},
			},
			expectedOptimal: false,
		},
		{
			name: "Non-optimal - repeat incorrect",
			compliance: PlaybackCompliance{
				IsCorrectShuffle: true,
				IsCorrectRepeat:  false,
				Issues:           []string{"Repeat mode is wrong"},
			},
			expectedOptimal: false,
		},
		{
			name: "Non-optimal - both incorrect",
			compliance: PlaybackCompliance{
				IsCorrectShuffle: false,
				IsCorrectRepeat:  false,
				Issues:           []string{"Shuffle is disabled", "Repeat mode is wrong"},
			},
			expectedOptimal: false,
		},
		{
			name: "Optimal with issues list (issues don't affect result)",
			compliance: PlaybackCompliance{
				IsCorrectShuffle: true,
				IsCorrectRepeat:  true,
				Issues:           []string{"Warning: Device volume low"},
			},
			expectedOptimal: true,
		},
		{
			name:            "Default zero values",
			compliance:      PlaybackCompliance{},
			expectedOptimal: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.compliance.IsOptimalForAutoDJ()
			if result != tt.expectedOptimal {
				t.Errorf("IsOptimalForAutoDJ() = %v, expected %v", result, tt.expectedOptimal)
			}
		})
	}
}
