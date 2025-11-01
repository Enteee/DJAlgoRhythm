package musiclink

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

const (
	// BeatportRequestTimeout is the timeout for Beatport page requests.
	BeatportRequestTimeout = 10 * time.Second
	// BeatportMaxReadSize limits the amount of HTML we read.
	BeatportMaxReadSize = 100 * 1024 // 100 KB should be enough for metadata.
)

// BeatportResolver resolves Beatport links to track information via HTML scraping.
type BeatportResolver struct {
	client *http.Client
}

// NewBeatportResolver creates a new Beatport link resolver.
func NewBeatportResolver() *BeatportResolver {
	return &BeatportResolver{
		client: &http.Client{
			Timeout: BeatportRequestTimeout,
		},
	}
}

// CanResolve checks if the URL is a Beatport link.
func (r *BeatportResolver) CanResolve(rawURL string) bool {
	u, err := url.Parse(rawURL)
	if err != nil {
		return false
	}

	hostname := strings.ToLower(u.Hostname())
	return hostname == "beatport.com" || hostname == "www.beatport.com"
}

// Resolve extracts track information from a Beatport URL by scraping the HTML.
func (r *BeatportResolver) Resolve(ctx context.Context, rawURL string) (*TrackInfo, error) {
	if !r.CanResolve(rawURL) {
		return nil, errors.New("not a Beatport URL")
	}

	// Check if this is a track URL.
	if !strings.Contains(rawURL, "/track/") {
		return nil, errors.New("not a Beatport track URL (only /track/ URLs are supported)")
	}

	// Fetch the HTML page.
	html, err := r.fetchHTML(ctx, rawURL)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch Beatport page: %w", err)
	}

	// Extract track title and artists from HTML.
	title, artist, err := r.extractTrackInfo(html)
	if err != nil {
		return nil, fmt.Errorf("failed to extract track info from HTML: %w", err)
	}

	return &TrackInfo{
		Title:  title,
		Artist: artist,
	}, nil
}

// fetchHTML fetches the HTML content of a Beatport page.
func (r *BeatportResolver) fetchHTML(ctx context.Context, pageURL string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, pageURL, http.NoBody)
	if err != nil {
		return "", err
	}

	// Set realistic browser headers.
	req.Header.Set("User-Agent",
		"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 "+
			"(KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")

	resp, err := r.client.Do(req)
	if err != nil {
		return "", err
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("Beatport returned status %d", resp.StatusCode)
	}

	// Read response body (limited to avoid excessive memory use).
	limitedReader := io.LimitReader(resp.Body, BeatportMaxReadSize)
	bodyBytes, err := io.ReadAll(limitedReader)
	if err != nil {
		return "", fmt.Errorf("failed to read response body: %w", err)
	}

	return string(bodyBytes), nil
}

// extractTrackInfo extracts track title and artist from Beatport HTML.
func (r *BeatportResolver) extractTrackInfo(html string) (title, artist string, err error) {
	// Try to extract from <title> tag (most reliable for Beatport).
	title, artist = r.extractFromTitleTag(html)
	if title != "" {
		return title, artist, nil
	}

	// Fallback: try to extract from OpenGraph meta tags.
	title, artist = r.extractFromMetaTags(html)
	if title != "" {
		return title, artist, nil
	}

	return "", "", errors.New("could not extract track information from Beatport page")
}

// extractFromTitleTag extracts track info from the HTML <title> tag.
func (r *BeatportResolver) extractFromTitleTag(html string) (title, artist string) {
	// Beatport title format is typically: "Track Title (Original Mix) by Artist1, Artist2 on Beatport".
	titleTagRegex := regexp.MustCompile(`<title>([^<]+)</title>`)
	matches := titleTagRegex.FindStringSubmatch(html)
	if len(matches) < 2 {
		return "", ""
	}

	titleText := matches[1]

	// Remove " on Beatport" suffix if present.
	titleText = strings.TrimSuffix(titleText, " on Beatport")
	titleText = strings.TrimSpace(titleText)

	// Split by " by " to separate track title from artist(s).
	if strings.Contains(titleText, " by ") {
		parts := strings.SplitN(titleText, " by ", 2)
		if len(parts) == 2 {
			title = strings.TrimSpace(parts[0])
			artist = strings.TrimSpace(parts[1])
		}
	} else {
		// If no " by " separator, treat the whole thing as the title.
		title = titleText
	}

	return title, artist
}

// extractFromMetaTags extracts track info from OpenGraph or Twitter meta tags.
func (r *BeatportResolver) extractFromMetaTags(html string) (title, artist string) {
	// Look for og:title meta tag.
	titleRegex := regexp.MustCompile(`<meta\s+property="og:title"\s+content="([^"]+)"`)
	if matches := titleRegex.FindStringSubmatch(html); len(matches) > 1 {
		title = matches[1]
	}

	// Look for og:description which might contain artist info.
	descRegex := regexp.MustCompile(`<meta\s+property="og:description"\s+content="([^"]+)"`)
	if matches := descRegex.FindStringSubmatch(html); len(matches) > 1 {
		desc := matches[1]
		// Description might contain artist info.
		if strings.Contains(desc, " by ") {
			parts := strings.SplitN(desc, " by ", 2)
			if len(parts) == 2 {
				artist = strings.TrimSpace(parts[1])
			}
		}
	}

	return title, artist
}
