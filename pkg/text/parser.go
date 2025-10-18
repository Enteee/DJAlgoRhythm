// Package text provides text parsing and URL classification for WhatsApp messages.
package text

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"whatdj/internal/core"

	"golang.org/x/text/unicode/norm"
)

const (
	// MinPartsForTrackInfo represents the minimum number of parts needed for track info
	MinPartsForTrackInfo = 3
	// SpotifyOpenDomain is the main Spotify domain
	SpotifyOpenDomain = "open.spotify.com"
	// SpotifyLinkDomain is the shortened Spotify link domain
	SpotifyLinkDomain = "spotify.link"
	// SpotifyAppLinkDomain is the app link domain
	SpotifyAppLinkDomain = "spotify.app.link"
	// URLResolveTimeout is the timeout for resolving URLs
	URLResolveTimeout = 10 * time.Second
	// MaxRedirects is the maximum number of redirects to follow
	MaxRedirects = 10
	// ReadBufferSize is the buffer size for reading page content
	ReadBufferSize = 8192
)

var (
	urlRegex        = regexp.MustCompile(`https?://\S+`)
	spotifyURIRegex = regexp.MustCompile(`spotify:\w+:\w+`)

	spotifyDomains = map[string]bool{
		SpotifyOpenDomain:    true,
		"spotify.com":        true,
		SpotifyLinkDomain:    true,
		SpotifyAppLinkDomain: true,
	}

	nonSpotifyMusicDomains = map[string]bool{
		"youtube.com":     true,
		"youtu.be":        true,
		"music.apple.com": true,
		"soundcloud.com":  true,
		"bandcamp.com":    true,
		"tiktok.com":      true,
		"instagram.com":   true,
	}
)

type Parser struct{}

func NewParser() *Parser {
	return &Parser{}
}

func (p *Parser) ParseMessage(text string) core.InputMessage {
	text = p.normalizeText(text)
	urls := p.extractURLs(text)

	msgType := p.classifyMessage(text, urls)

	return core.InputMessage{
		Type: msgType,
		Text: text,
		URLs: urls,
	}
}

func (p *Parser) normalizeText(text string) string {
	text = strings.TrimSpace(text)
	text = norm.NFKC.String(text)

	// Replace multiple spaces with single space
	text = regexp.MustCompile(`\s+`).ReplaceAllString(text, " ")

	lines := strings.Split(text, "\n")
	var normalizedLines []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" {
			normalizedLines = append(normalizedLines, line)
		}
	}

	return strings.Join(normalizedLines, " ")
}

func (p *Parser) extractURLs(text string) []string {
	matches := urlRegex.FindAllString(text, -1)
	var cleanURLs []string

	for _, match := range matches {
		cleanURL := p.cleanURL(match)
		if cleanURL != "" {
			cleanURLs = append(cleanURLs, cleanURL)
		}
	}

	return cleanURLs
}

func (p *Parser) cleanURL(rawURL string) string {
	rawURL = strings.TrimRight(rawURL, ".,!?;")

	// Check if this looks like a valid URL
	if !strings.HasPrefix(rawURL, "http://") && !strings.HasPrefix(rawURL, "https://") {
		return ""
	}

	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}

	// Additional validation - ensure it has a valid host
	if u.Host == "" {
		return ""
	}

	q := u.Query()

	utmParams := []string{"utm_source", "utm_medium", "utm_campaign", "utm_term", "utm_content"}
	for _, param := range utmParams {
		q.Del(param)
	}

	q.Del("si")

	u.RawQuery = q.Encode()

	return u.String()
}

func (p *Parser) classifyMessage(text string, urls []string) core.MessageType {
	// Check for Spotify URIs in the text
	if spotifyURIRegex.MatchString(text) {
		return core.MessageTypeSpotifyLink
	}

	for _, url := range urls {
		if p.isSpotifyURL(url) {
			return core.MessageTypeSpotifyLink
		}
	}

	for _, url := range urls {
		if p.isMusicURL(url) {
			return core.MessageTypeNonSpotifyLink
		}
	}

	return core.MessageTypeFreeText
}

func (p *Parser) isSpotifyURL(rawURL string) bool {
	if strings.Contains(rawURL, "spotify:track:") {
		return true
	}

	u, err := url.Parse(rawURL)
	if err != nil {
		return false
	}

	hostname := strings.ToLower(u.Hostname())

	if spotifyDomains[hostname] {
		// For regular Spotify domains, check if it contains /track/
		if hostname == SpotifyOpenDomain || hostname == "spotify.com" {
			return strings.Contains(u.Path, "/track/")
		}
		// For shortened URLs, assume they're Spotify track links
		if hostname == SpotifyLinkDomain || hostname == SpotifyAppLinkDomain {
			return true
		}
	}

	return false
}

func (p *Parser) isMusicURL(rawURL string) bool {
	u, err := url.Parse(rawURL)
	if err != nil {
		return false
	}

	hostname := strings.ToLower(u.Hostname())

	if hostname == "www.youtube.com" || hostname == "m.youtube.com" {
		hostname = "youtube.com"
	}

	if hostname == "www.tiktok.com" || hostname == "vm.tiktok.com" {
		hostname = "tiktok.com"
	}

	return nonSpotifyMusicDomains[hostname]
}

// resolveShortURL resolves shortened URLs to their final destination
func (p *Parser) resolveShortURL(shortURL string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), URLResolveTimeout)
	defer cancel()

	client := &http.Client{
		Timeout: URLResolveTimeout,
		CheckRedirect: func(_ *http.Request, via []*http.Request) error {
			// Follow redirects but stop after reasonable limit
			if len(via) >= MaxRedirects {
				return http.ErrUseLastResponse
			}
			return nil
		},
	}

	req, err := http.NewRequestWithContext(ctx, "HEAD", shortURL, http.NoBody)
	if err != nil {
		return "", err
	}

	// Set realistic browser headers
	req.Header.Set("User-Agent",
		"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	finalURL := resp.Request.URL.String()

	// Validate that we got a Spotify track URL
	u, err := url.Parse(finalURL)
	if err != nil {
		return "", err
	}

	// Check if we got a valid Spotify track URL
	hostname := strings.ToLower(u.Hostname())
	if hostname == SpotifyOpenDomain && strings.Contains(u.Path, "/track/") {
		return finalURL, nil
	}

	// If it's still a shortened URL, try using GET instead of HEAD
	if hostname == SpotifyAppLinkDomain {
		return p.resolveWithGET(shortURL)
	}

	return "", errors.New("URL did not resolve to a Spotify track - got: " + finalURL)
}

// resolveWithGET tries to resolve the URL by fetching page content (for spotify.app.link)
func (p *Parser) resolveWithGET(shortURL string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), URLResolveTimeout)
	defer cancel()

	client := &http.Client{
		Timeout: URLResolveTimeout,
	}

	req, err := http.NewRequestWithContext(ctx, "GET", shortURL, http.NoBody)
	if err != nil {
		return "", err
	}

	// Set realistic browser headers
	req.Header.Set("User-Agent",
		"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,image/webp,*/*;q=0.8")

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	// Read the page content to extract Spotify URL
	buf := make([]byte, ReadBufferSize) // Read first 8KB which should contain the URL
	n, _ := resp.Body.Read(buf)
	content := string(buf[:n])

	// Use regex to find Spotify track URL in the content
	spotifyURLRegex := regexp.MustCompile(`https://open\.spotify\.com/track/[a-zA-Z0-9]+`)
	matches := spotifyURLRegex.FindStringSubmatch(content)

	if len(matches) > 0 {
		return matches[0], nil
	}

	return "", errors.New("could not find Spotify track URL in page content")
}

func (p *Parser) ExtractSpotifyTrackID(rawURL string) (string, error) {
	if strings.HasPrefix(rawURL, "spotify:track:") {
		parts := strings.Split(rawURL, ":")
		if len(parts) >= MinPartsForTrackInfo {
			return parts[2], nil
		}
	}

	// Check if this looks like a valid URL
	if !strings.HasPrefix(rawURL, "http://") && !strings.HasPrefix(rawURL, "https://") {
		return "", errors.New("invalid URL")
	}

	u, err := url.Parse(rawURL)
	if err != nil {
		return "", err
	}

	// Handle shortened URLs by resolving them first
	hostname := strings.ToLower(u.Hostname())
	if hostname == "spotify.link" || hostname == "spotify.app.link" {
		resolvedURL, err := p.resolveShortURL(rawURL)
		if err != nil {
			return "", err
		}
		// Recursively extract from the resolved URL
		return p.ExtractSpotifyTrackID(resolvedURL)
	}

	pathParts := strings.Split(strings.Trim(u.Path, "/"), "/")
	for i, part := range pathParts {
		if part == "track" && i+1 < len(pathParts) {
			trackID := pathParts[i+1]
			if idx := strings.Index(trackID, "?"); idx != -1 {
				trackID = trackID[:idx]
			}
			return trackID, nil
		}
	}

	return "", nil
}
