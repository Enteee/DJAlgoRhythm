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
	// AmazonMusicRequestTimeout is the timeout for Amazon Music page requests.
	AmazonMusicRequestTimeout = 10 * time.Second
	// AmazonMusicMaxReadSize limits the amount of HTML we read.
	AmazonMusicMaxReadSize = 102400 // 100 KB should be enough for metadata.
)

// AmazonMusicResolver resolves Amazon Music links to track information via HTML scraping.
type AmazonMusicResolver struct {
	client *http.Client
}

// NewAmazonMusicResolver creates a new Amazon Music link resolver.
func NewAmazonMusicResolver() *AmazonMusicResolver {
	return &AmazonMusicResolver{
		client: &http.Client{
			Timeout: AmazonMusicRequestTimeout,
		},
	}
}

// CanResolve checks if the URL is an Amazon Music link.
func (r *AmazonMusicResolver) CanResolve(rawURL string) bool {
	u, err := url.Parse(rawURL)
	if err != nil {
		return false
	}

	hostname := strings.ToLower(u.Hostname())
	// Support various Amazon Music domains (music.amazon.com, music.amazon.co.uk, etc.).
	return strings.HasPrefix(hostname, "music.amazon.")
}

// Resolve extracts track information from an Amazon Music URL by scraping the HTML.
func (r *AmazonMusicResolver) Resolve(ctx context.Context, rawURL string) (*TrackInfo, error) {
	if !r.CanResolve(rawURL) {
		return nil, errors.New("not an Amazon Music URL")
	}

	// Fetch the HTML page.
	html, err := r.fetchHTML(ctx, rawURL)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch Amazon Music page: %w", err)
	}

	// Extract track title and artist from HTML.
	title, artist, err := r.extractTrackInfo(html)
	if err != nil {
		return nil, fmt.Errorf("failed to extract track info from HTML: %w", err)
	}

	return &TrackInfo{
		Title:  title,
		Artist: artist,
	}, nil
}

// fetchHTML fetches the HTML content of an Amazon Music page.
func (r *AmazonMusicResolver) fetchHTML(ctx context.Context, pageURL string) (string, error) {
	return fetchHTMLFromURL(ctx, r.client, pageURL, "Amazon Music", AmazonMusicMaxReadSize)
}

// extractTrackInfo extracts track title and artist from Amazon Music HTML.
func (r *AmazonMusicResolver) extractTrackInfo(html string) (title, artist string, err error) {
	// Try to extract from OpenGraph meta tags first.
	title, artist = r.extractFromMetaTags(html)
	if title != "" {
		return title, artist, nil
	}

	// Fallback: try to extract from <title> tag.
	title, artist = r.extractFromTitleTag(html)
	if title != "" {
		return title, artist, nil
	}

	return "", "", errors.New("could not extract track information from Amazon Music page")
}

// extractFromMetaTags extracts track info from OpenGraph or Twitter meta tags.
func (r *AmazonMusicResolver) extractFromMetaTags(html string) (title, artist string) {
	// Look for og:title meta tag.
	titleRegex := regexp.MustCompile(`<meta\s+property="og:title"\s+content="([^"]+)"`)
	if matches := titleRegex.FindStringSubmatch(html); len(matches) > 1 {
		title = matches[1]
	}

	// Look for og:description which might contain artist info.
	descRegex := regexp.MustCompile(`<meta\s+property="og:description"\s+content="([^"]+)"`)
	if matches := descRegex.FindStringSubmatch(html); len(matches) > 1 {
		desc := matches[1]
		artist = r.extractArtistFromDescription(desc)
	}

	return title, artist
}

// extractArtistFromDescription extracts artist name from og:description meta tag content.
func (r *AmazonMusicResolver) extractArtistFromDescription(desc string) string {
	// Description often contains artist info (e.g., "Song by Artist on Amazon Music").
	if !strings.Contains(strings.ToLower(desc), " by ") {
		return ""
	}

	parts := strings.SplitN(desc, " by ", expectedSplitParts)
	if len(parts) != expectedSplitParts {
		return ""
	}

	// Further split on " on Amazon Music" if present.
	artistPart := parts[1]
	if strings.Contains(artistPart, " on Amazon Music") {
		artistPart = strings.SplitN(artistPart, " on Amazon Music", expectedSplitParts)[0]
	}

	return strings.TrimSpace(artistPart)
}

// extractFromTitleTag extracts track info from the HTML <title> tag.
func (r *AmazonMusicResolver) extractFromTitleTag(html string) (title, artist string) {
	// Amazon Music title format: "Song Title by Artist Name on Amazon Music".
	return extractTitleAndArtistFromTitleTag(html, " on Amazon Music", " by ")
}
