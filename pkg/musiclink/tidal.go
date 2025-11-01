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
	// TidalRequestTimeout is the timeout for Tidal page requests.
	TidalRequestTimeout = 10 * time.Second
	// TidalMaxReadSize limits the amount of HTML we read.
	TidalMaxReadSize = 102400 // 100 KB should be enough for metadata.
)

// TidalResolver resolves Tidal links to track information via HTML scraping.
type TidalResolver struct {
	client *http.Client
}

// NewTidalResolver creates a new Tidal link resolver.
func NewTidalResolver() *TidalResolver {
	return &TidalResolver{
		client: &http.Client{
			Timeout: TidalRequestTimeout,
		},
	}
}

// CanResolve checks if the URL is a Tidal link.
func (r *TidalResolver) CanResolve(rawURL string) bool {
	u, err := url.Parse(rawURL)
	if err != nil {
		return false
	}

	hostname := strings.ToLower(u.Hostname())
	return hostname == "tidal.com" || hostname == "www.tidal.com"
}

// Resolve extracts track information from a Tidal URL by scraping the HTML.
func (r *TidalResolver) Resolve(ctx context.Context, rawURL string) (*TrackInfo, error) {
	if !r.CanResolve(rawURL) {
		return nil, errors.New("not a Tidal URL")
	}

	return r.resolveTrackURL(ctx, rawURL, "/track/")
}

// resolveTrackURL handles the common pattern of validating track URL, fetching HTML, and extracting info.
func (r *TidalResolver) resolveTrackURL(ctx context.Context, rawURL, trackPath string) (*TrackInfo, error) {
	// Check if this is a track URL.
	if !strings.Contains(rawURL, trackPath) {
		return nil, fmt.Errorf("not a Tidal track URL (only %s URLs are supported)", trackPath)
	}

	// Fetch the HTML page.
	html, err := r.fetchHTML(ctx, rawURL)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch Tidal page: %w", err)
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

// fetchHTML fetches the HTML content of a Tidal page.
func (r *TidalResolver) fetchHTML(ctx context.Context, pageURL string) (string, error) {
	return fetchHTMLFromURL(ctx, r.client, pageURL, "Tidal", TidalMaxReadSize)
}

// extractTrackInfo extracts track title and artist from Tidal HTML.
func (r *TidalResolver) extractTrackInfo(html string) (title, artist string, err error) {
	// Try to extract from OpenGraph meta tags first (most reliable).
	title, artist = r.extractFromMetaTags(html)
	if title != "" {
		return title, artist, nil
	}

	// Fallback: try to extract from <title> tag.
	title, artist = r.extractFromTitleTag(html)
	if title != "" {
		return title, artist, nil
	}

	return "", "", errors.New("could not extract track information from Tidal page")
}

// extractFromMetaTags extracts track info from OpenGraph or Twitter meta tags.
func (r *TidalResolver) extractFromMetaTags(html string) (title, artist string) {
	// Look for og:title meta tag.
	titleRegex := regexp.MustCompile(`<meta\s+property="og:title"\s+content="([^"]+)"`)
	if matches := titleRegex.FindStringSubmatch(html); len(matches) > 1 {
		title = matches[1]
	}

	// Look for og:description which might contain artist info.
	descRegex := regexp.MustCompile(`<meta\s+property="og:description"\s+content="([^"]+)"`)
	if matches := descRegex.FindStringSubmatch(html); len(matches) > 1 {
		desc := matches[1]
		// Description often contains "By Artist Name" or similar.
		if strings.Contains(strings.ToLower(desc), "by ") {
			parts := strings.SplitN(desc, "by ", expectedSplitParts)
			if len(parts) == expectedSplitParts {
				artist = strings.TrimSpace(parts[1])
			}
		}
	}

	return title, artist
}

// extractFromTitleTag extracts track info from the HTML <title> tag.
func (r *TidalResolver) extractFromTitleTag(html string) (title, artist string) {
	// Tidal title format is often: "Track Title – Artist Name | TIDAL" or similar.
	titleTagRegex := regexp.MustCompile(`<title>([^<]+)</title>`)
	matches := titleTagRegex.FindStringSubmatch(html)
	if len(matches) < minTitleTagMatches {
		return "", ""
	}

	titleText := matches[1]

	// Remove " | TIDAL" suffix if present.
	titleText = strings.TrimSuffix(titleText, " | TIDAL")
	titleText = strings.TrimSpace(titleText)

	// Split by " – " (en dash) or " - " (hyphen).
	var parts []string
	if strings.Contains(titleText, " – ") {
		parts = strings.SplitN(titleText, " – ", expectedSplitParts)
	} else if strings.Contains(titleText, " - ") {
		parts = strings.SplitN(titleText, " - ", expectedSplitParts)
	}

	if len(parts) == expectedSplitParts {
		title = strings.TrimSpace(parts[0])
		artist = strings.TrimSpace(parts[1])
	} else {
		// If no separator, treat the whole thing as the title.
		title = titleText
	}

	return title, artist
}
