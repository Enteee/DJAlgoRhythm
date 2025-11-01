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
	// TidalRequestTimeout is the timeout for Tidal page requests.
	TidalRequestTimeout = 10 * time.Second
	// TidalMaxReadSize limits the amount of HTML we read.
	TidalMaxReadSize = 102400 // 100 KB should be enough for metadata.
	// expectedSplitParts is the expected number of parts when splitting title/artist strings.
	expectedSplitParts = 2
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
		return nil, errors.New("not a Tidal URL.")
	}

	// Check if this is a track URL.
	if !strings.Contains(rawURL, "/track/") {
		return nil, errors.New("not a Tidal track URL (only /track/ URLs are supported).")
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
		return "", fmt.Errorf("Tidal returned status %d", resp.StatusCode)
	}

	// Read response body (limited to avoid excessive memory use).
	limitedReader := io.LimitReader(resp.Body, TidalMaxReadSize)
	bodyBytes, err := io.ReadAll(limitedReader)
	if err != nil {
		return "", fmt.Errorf("failed to read response body: %w", err)
	}

	return string(bodyBytes), nil
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

	return "", "", errors.New("could not extract track information from Tidal page.")
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
	if len(matches) < 2 {
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
