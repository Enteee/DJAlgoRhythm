package musiclink

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

const (
	// YouTubeOEmbedURL is the YouTube oEmbed API endpoint.
	YouTubeOEmbedURL = "https://www.youtube.com/oembed"
	// YouTubeRequestTimeout is the timeout for YouTube API requests.
	YouTubeRequestTimeout = 10 * time.Second
	// youtubeExpectedSplitParts is the expected number of parts when splitting title/artist strings.
	youtubeExpectedSplitParts = 2
)

// YouTubeOEmbedResponse represents the response from YouTube's oEmbed API.
type YouTubeOEmbedResponse struct {
	Title      string `json:"title"`
	AuthorName string `json:"author_name"`
}

// YouTubeResolver resolves YouTube and YouTube Music links to track information.
type YouTubeResolver struct {
	client *http.Client
}

// NewYouTubeResolver creates a new YouTube link resolver.
func NewYouTubeResolver() *YouTubeResolver {
	return &YouTubeResolver{
		client: &http.Client{
			Timeout: YouTubeRequestTimeout,
		},
	}
}

// CanResolve checks if the URL is a YouTube or YouTube Music link.
func (r *YouTubeResolver) CanResolve(rawURL string) bool {
	u, err := url.Parse(rawURL)
	if err != nil {
		return false
	}

	hostname := strings.ToLower(u.Hostname())
	// Normalize various YouTube domains.
	switch hostname {
	case "youtube.com", "www.youtube.com", "m.youtube.com", "music.youtube.com", "youtu.be":
		return true
	}
	return false
}

// Resolve extracts track information from a YouTube URL using the oEmbed API.
func (r *YouTubeResolver) Resolve(ctx context.Context, rawURL string) (*TrackInfo, error) {
	if !r.CanResolve(rawURL) {
		return nil, errors.New("not a YouTube URL.")
	}

	// Extract video ID.
	videoID, err := r.extractVideoID(rawURL)
	if err != nil {
		return nil, fmt.Errorf("failed to extract video ID: %w", err)
	}

	// Build canonical YouTube URL for oEmbed.
	videoURL := fmt.Sprintf("https://www.youtube.com/watch?v=%s", videoID)

	// Fetch metadata from oEmbed API.
	oembedResp, err := r.fetchOEmbed(ctx, videoURL)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch oEmbed data: %w", err)
	}

	// Extract track title and artist from the response.
	title, artist := r.parseTrackInfo(oembedResp)

	return &TrackInfo{
		Title:  title,
		Artist: artist,
	}, nil
}

// extractVideoID extracts the YouTube video ID from various URL formats.
func (r *YouTubeResolver) extractVideoID(rawURL string) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", err
	}

	hostname := strings.ToLower(u.Hostname())

	// Handle youtu.be short links.
	if hostname == "youtu.be" {
		// Video ID is in the path.
		path := strings.Trim(u.Path, "/")
		if path == "" {
			return "", errors.New("no video ID in youtu.be URL.")
		}
		return path, nil
	}

	// Handle standard YouTube URLs (youtube.com, www.youtube.com, m.youtube.com, music.youtube.com).
	videoID := u.Query().Get("v")
	if videoID == "" {
		return "", errors.New("no video ID in YouTube URL.")
	}
	return videoID, nil
}

// fetchOEmbed fetches metadata from YouTube's oEmbed API.
func (r *YouTubeResolver) fetchOEmbed(ctx context.Context, videoURL string) (*YouTubeOEmbedResponse, error) {
	// Build oEmbed request URL.
	reqURL := fmt.Sprintf("%s?url=%s&format=json", YouTubeOEmbedURL, url.QueryEscape(videoURL))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, http.NoBody)
	if err != nil {
		return nil, err
	}

	resp, err := r.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("oEmbed API returned status %d", resp.StatusCode)
	}

	var oembedResp YouTubeOEmbedResponse
	if err := json.NewDecoder(resp.Body).Decode(&oembedResp); err != nil {
		return nil, fmt.Errorf("failed to decode oEmbed response: %w", err)
	}

	return &oembedResp, nil
}

// parseTrackInfo extracts track title and artist from oEmbed response.
func (r *YouTubeResolver) parseTrackInfo(resp *YouTubeOEmbedResponse) (title, artist string) {
	// Clean the title by removing common video-specific terms.
	title = r.cleanTitle(resp.Title)

	// Try to extract artist from the author name or title.
	artist = r.extractArtist(resp.Title, resp.AuthorName)

	return title, artist
}

// cleanTitle removes common YouTube video metadata from titles.
func (r *YouTubeResolver) cleanTitle(title string) string {
	// Remove common patterns in parentheses or brackets.
	patterns := []string{
		`\(Official Video\)`,
		`\(Official Music Video\)`,
		`\(Official Audio\)`,
		`\(Lyric Video\)`,
		`\(Lyrics\)`,
		`\[Official Video\]`,
		`\[Official Music Video\]`,
		`\[Official Audio\]`,
		`\[Lyric Video\]`,
		`\[Lyrics\]`,
		`\(HD\)`,
		`\[HD\]`,
		`\(4K\)`,
		`\[4K\]`,
	}

	cleaned := title
	for _, pattern := range patterns {
		re := regexp.MustCompile(`(?i)` + pattern)
		cleaned = re.ReplaceAllString(cleaned, "")
	}

	// Trim whitespace.
	cleaned = strings.TrimSpace(cleaned)

	return cleaned
}

// extractArtist attempts to extract the artist name from title and author.
func (r *YouTubeResolver) extractArtist(title, authorName string) string {
	// Check if the author name looks like an official artist channel.
	// VEVO channels, Topic channels, and verified artist channels are good indicators.
	if strings.HasSuffix(authorName, "VEVO") {
		// Extract artist name from VEVO channel (e.g., "RickAstleyVEVO" -> "Rick Astley").
		artist := strings.TrimSuffix(authorName, "VEVO")
		return r.splitCamelCase(artist)
	}

	if strings.HasSuffix(authorName, " - Topic") {
		// YouTube auto-generated artist channels.
		return strings.TrimSuffix(authorName, " - Topic")
	}

	// Try to extract artist from title if it contains a separator.
	// Common patterns: "Artist - Song Title" or "Song Title - Artist".
	if strings.Contains(title, " - ") {
		parts := strings.SplitN(title, " - ", youtubeExpectedSplitParts)
		if len(parts) == youtubeExpectedSplitParts {
			// Assume first part is the artist (most common format).
			return strings.TrimSpace(parts[0])
		}
	}

	// Fall back to using the author name as-is.
	return authorName
}

// splitCamelCase splits a camelCase string into words.
func (r *YouTubeResolver) splitCamelCase(s string) string {
	// Insert spaces before capital letters.
	re := regexp.MustCompile(`([a-z])([A-Z])`)
	result := re.ReplaceAllString(s, "$1 $2")
	return result
}
