package musiclink

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
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
)

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
