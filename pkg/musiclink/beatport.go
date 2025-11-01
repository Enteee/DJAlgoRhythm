package musiclink

import (
	"context"
	"errors"
	"fmt"
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
	BeatportMaxReadSize = 102400 // 100 KB should be enough for metadata.
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

	return r.resolveTrackURL(ctx, rawURL, "/track/")
}

// resolveTrackURL handles the common pattern of validating track URL, fetching HTML, and extracting info.
func (r *BeatportResolver) resolveTrackURL(ctx context.Context, rawURL, trackPath string) (*TrackInfo, error) {
	// Check if this is a track URL.
	if !strings.Contains(rawURL, trackPath) {
		return nil, fmt.Errorf("not a Beatport track URL (only %s URLs are supported)", trackPath)
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
	return fetchHTMLFromURL(ctx, r.client, pageURL, "Beatport", BeatportMaxReadSize)
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
	// Beatport title format: "Track Title (Original Mix) by Artist1, Artist2 on Beatport".
	return extractTitleAndArtistFromTitleTag(html, " on Beatport", " by ")
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
			parts := strings.SplitN(desc, " by ", expectedSplitParts)
			if len(parts) == expectedSplitParts {
				artist = strings.TrimSpace(parts[1])
			}
		}
	}

	return title, artist
}
