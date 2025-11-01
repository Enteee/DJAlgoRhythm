package musiclink

import (
	"context"
	"encoding/json"
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
	// commonUserAgent is the user agent string used for all HTTP requests.
	commonUserAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 " +
		"(KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"
	// commonAcceptHeader is the accept header used for all HTTP requests.
	commonAcceptHeader = "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8"
	// minTitleTagMatches is the minimum number of regex matches expected for title tag extraction.
	minTitleTagMatches = 2
	// expectedSplitParts is the expected number of parts when splitting title/artist strings.
	expectedSplitParts = 2
	// defaultHTTPTimeout is the default timeout for HTTP requests.
	defaultHTTPTimeout = 10 * time.Second
	// maxHTTPRedirects is the maximum number of HTTP redirects to follow.
	maxHTTPRedirects = 3
)

var (
	// ErrTooManyRedirects is returned when too many redirects are encountered.
	ErrTooManyRedirects = errors.New("too many redirects")
)

// newHTTPClient creates a new HTTP client with standard settings and redirect validation.
func newHTTPClient() *http.Client {
	return &http.Client{
		Timeout: defaultHTTPTimeout,
		CheckRedirect: func(_ *http.Request, via []*http.Request) error {
			if len(via) >= maxHTTPRedirects {
				return ErrTooManyRedirects
			}
			return nil
		},
	}
}

// fetchHTMLFromURL fetches HTML content from a URL with a size limit.
func fetchHTMLFromURL(
	ctx context.Context,
	client *http.Client,
	pageURL string,
	serviceName string,
	maxReadSize int64,
) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, pageURL, http.NoBody)
	if err != nil {
		return "", err
	}

	// Set realistic browser headers.
	req.Header.Set("User-Agent", commonUserAgent)
	req.Header.Set("Accept", commonAcceptHeader)

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("%s returned status %d", serviceName, resp.StatusCode)
	}

	// Read response body (limited to avoid excessive memory use).
	limitedReader := io.LimitReader(resp.Body, maxReadSize)
	bodyBytes, err := io.ReadAll(limitedReader)
	if err != nil {
		return "", fmt.Errorf("failed to read response body: %w", err)
	}

	return string(bodyBytes), nil
}

// fetchOEmbedJSON fetches and decodes JSON from an oEmbed API endpoint.
// This is a generic helper to avoid code duplication across resolvers.
func fetchOEmbedJSON(
	ctx context.Context,
	client *http.Client,
	oembedURL string,
	targetURL string,
	dest interface{},
) error {
	// Build oEmbed request URL.
	reqURL := fmt.Sprintf("%s?url=%s&format=json", oembedURL, url.QueryEscape(targetURL))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, http.NoBody)
	if err != nil {
		return err
	}

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("oEmbed API returned status %d", resp.StatusCode)
	}

	if err := json.NewDecoder(resp.Body).Decode(dest); err != nil {
		return fmt.Errorf("failed to decode oEmbed response: %w", err)
	}

	return nil
}

// extractTitleAndArtistFromTitleTag extracts track info from HTML <title> tag.
// This handles the common pattern of "Track Title by Artist on Service" format.
func extractTitleAndArtistFromTitleTag(html, serviceSuffix, separator string) (title, artist string) {
	titleTagRegex := regexp.MustCompile(`<title>([^<]+)</title>`)
	matches := titleTagRegex.FindStringSubmatch(html)
	if len(matches) < minTitleTagMatches {
		return "", ""
	}

	titleText := matches[1]

	// Remove service suffix if present.
	if serviceSuffix != "" {
		titleText = strings.TrimSuffix(titleText, serviceSuffix)
		titleText = strings.TrimSpace(titleText)
	}

	// Split by separator to separate track title from artist(s).
	if separator != "" && strings.Contains(titleText, separator) {
		parts := strings.SplitN(titleText, separator, expectedSplitParts)
		if len(parts) == expectedSplitParts {
			title = strings.TrimSpace(parts[0])
			artist = strings.TrimSpace(parts[1])
			return title, artist
		}
	}

	// If no separator, treat the whole thing as the title.
	return titleText, ""
}
