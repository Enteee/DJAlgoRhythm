package fuzzy

import (
	"regexp"
	"strings"
	"time"
	"unicode"

	"golang.org/x/text/unicode/norm"
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

	if len(s1) == 0 || len(s2) == 0 {
		return 0.0
	}

	return float64(n.longestCommonSubsequence(s1, s2)) / float64(max(len(s1), len(s2)))
}

func (norm *Normalizer) longestCommonSubsequence(s1, s2 string) int {
	m, n := len(s1), len(s2)
	dp := make([][]int, m+1)
	for i := range dp {
		dp[i] = make([]int, n+1)
	}

	for i := 1; i <= m; i++ {
		for j := 1; j <= n; j++ {
			if s1[i-1] == s2[j-1] {
				dp[i][j] = dp[i-1][j-1] + 1
			} else {
				dp[i][j] = max(dp[i-1][j], dp[i][j-1])
			}
		}
	}

	return dp[m][n]
}

func (n *Normalizer) DurationTolerance(d1, d2 time.Duration) float64 {
	diff := time.Duration(abs(int64(d1 - d2)))
	tolerance := 30 * time.Second

	if diff <= tolerance {
		return 1.0
	}

	maxDiff := 2 * time.Minute
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

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}