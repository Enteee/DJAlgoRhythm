// Package text provides text parsing and URL classification for WhatsApp messages.
package text

import (
	"errors"
	"net/url"
	"regexp"
	"strings"

	"whatdj/internal/core"

	"golang.org/x/text/unicode/norm"
)

const (
	// MinPartsForTrackInfo represents the minimum number of parts needed for track info
	MinPartsForTrackInfo = 3
)

var (
	urlRegex      = regexp.MustCompile(`https?://\S+`)
	spotifyURIRegex = regexp.MustCompile(`spotify:\w+:\w+`)

	spotifyDomains = map[string]bool{
		"open.spotify.com": true,
		"spotify.com":      true,
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
		return strings.Contains(u.Path, "/track/")
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
