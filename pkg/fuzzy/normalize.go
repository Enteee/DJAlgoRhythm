// Package fuzzy provides text normalization and similarity matching for music metadata.
package fuzzy

import (
	"regexp"
	"strings"
	"time"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

const (
	// DurationToleranceSeconds is the base tolerance for duration matching in seconds
	DurationToleranceSeconds = 30
	// MaxDurationDifferenceMinutes is the maximum difference before returning 0 similarity
	MaxDurationDifferenceMinutes = 2
)

var (
	featRegex     = regexp.MustCompile(`(?i)\s*[\(\[]?\s*(?:feat\.?|ft\.?|featuring)\s+[^\)\]]*[\)\]]?\s*`)
	remixRegex    = regexp.MustCompile(`(?i)\s*[\(\[]?\s*.*remix.*[\)\]]?\s*`)
	versionRegex  = regexp.MustCompile(`(?i)\s*[\(\[]?\s*(remaster|remastered|deluxe|extended|radio edit|clean|explicit).*[\)\]]?\s*`)
	punctRegex    = regexp.MustCompile(`[^\p{L}\p{N}\s]+`)
	whitespaceRegex = regexp.MustCompile(`\s+`)
)

type Normalizer struct{}

func NewNormalizer() *Normalizer {
	return &Normalizer{}
}

func (n *Normalizer) NormalizeArtist(artist string) string {
	artist = n.basicNormalize(artist)

	artist = strings.ReplaceAll(artist, " and ", " & ")
	artist = strings.ReplaceAll(artist, " vs ", " vs. ")
	artist = strings.ReplaceAll(artist, " feat ", " feat. ")
	artist = strings.ReplaceAll(artist, " ft ", " ft. ")

	return artist
}

func (n *Normalizer) NormalizeTitle(title string) string {
	title = n.basicNormalize(title)

	title = featRegex.ReplaceAllString(title, "")
	title = remixRegex.ReplaceAllString(title, "")
	title = versionRegex.ReplaceAllString(title, "")

	title = strings.TrimSpace(title)
	return title
}

func (n *Normalizer) basicNormalize(text string) string {
	text = norm.NFKD.String(text)

	var result strings.Builder
	for _, r := range text {
		if !unicode.IsMark(r) {
			result.WriteRune(r)
		}
	}
	text = result.String()

	text = punctRegex.ReplaceAllString(text, " ")
	text = whitespaceRegex.ReplaceAllString(text, " ")

	text = strings.ToLower(text)
	text = strings.TrimSpace(text)

	return text
}

func (n *Normalizer) CalculateSimilarity(s1, s2 string) float64 {
	if s1 == s2 {
		return 1.0
	}

	if s1 == "" || s2 == "" {
		return 0.0
	}

	maxLen := len(s1)
	if len(s2) > maxLen {
		maxLen = len(s2)
	}
	return float64(n.longestCommonSubsequence(s1, s2)) / float64(maxLen)
}

func (n *Normalizer) longestCommonSubsequence(s1, s2 string) int {
	m, length := len(s1), len(s2)
	dp := make([][]int, m+1)
	for i := range dp {
		dp[i] = make([]int, length+1)
	}

	for i := 1; i <= m; i++ {
		for j := 1; j <= length; j++ {
			if s1[i-1] == s2[j-1] {
				dp[i][j] = dp[i-1][j-1] + 1
			} else {
				if dp[i-1][j] > dp[i][j-1] {
					dp[i][j] = dp[i-1][j]
				} else {
					dp[i][j] = dp[i][j-1]
				}
			}
		}
	}

	return dp[m][length]
}

func (n *Normalizer) DurationTolerance(d1, d2 time.Duration) float64 {
	diff := time.Duration(abs(int64(d1 - d2)))
	tolerance := DurationToleranceSeconds * time.Second

	if diff <= tolerance {
		return 1.0
	}

	maxDiff := MaxDurationDifferenceMinutes * time.Minute
	if diff >= maxDiff {
		return 0.0
	}

	return 1.0 - float64(diff-tolerance)/float64(maxDiff-tolerance)
}

func abs(x int64) int64 {
	if x < 0 {
		return -x
	}
	return x
}

