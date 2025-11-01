package musiclink

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

const (
	// SoundCloudOEmbedURL is the SoundCloud oEmbed API endpoint.
	SoundCloudOEmbedURL = "https://soundcloud.com/oembed"
	// soundcloudExpectedSplitParts is the expected number of parts when splitting title by " by ".
	soundcloudExpectedSplitParts = 2
)

// SoundCloudOEmbedResponse represents the response from SoundCloud's oEmbed API.
type SoundCloudOEmbedResponse struct {
	Title      string `json:"title"`
	AuthorName string `json:"author_name"`
	AuthorURL  string `json:"author_url"`
}

// SoundCloudResolver resolves SoundCloud links to track information.
type SoundCloudResolver struct {
	client *http.Client
}

// NewSoundCloudResolver creates a new SoundCloud link resolver.
func NewSoundCloudResolver() *SoundCloudResolver {
	return &SoundCloudResolver{
		client: newHTTPClient(),
	}
}

// CanResolve checks if the URL is a SoundCloud link.
func (r *SoundCloudResolver) CanResolve(rawURL string) bool {
	u, err := url.Parse(rawURL)
	if err != nil {
		return false
	}

	hostname := strings.ToLower(u.Hostname())
	// Support main, mobile, and short link domains.
	switch hostname {
	case "soundcloud.com", "www.soundcloud.com", "m.soundcloud.com", "on.soundcloud.com":
		return true
	}
	return false
}

// Resolve extracts track information from a SoundCloud URL using the oEmbed API.
func (r *SoundCloudResolver) Resolve(ctx context.Context, rawURL string) (*TrackInfo, error) {
	if !r.CanResolve(rawURL) {
		return nil, errors.New("not a SoundCloud URL")
	}

	// Fetch metadata from oEmbed API.
	oembedResp, err := r.fetchOEmbed(ctx, rawURL)
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

// fetchOEmbed fetches metadata from SoundCloud's oEmbed API.
func (r *SoundCloudResolver) fetchOEmbed(ctx context.Context, trackURL string) (*SoundCloudOEmbedResponse, error) {
	var oembedResp SoundCloudOEmbedResponse
	if err := fetchOEmbedJSON(ctx, r.client, SoundCloudOEmbedURL, trackURL, &oembedResp); err != nil {
		return nil, err
	}
	return &oembedResp, nil
}

// parseTrackInfo extracts track title and artist from oEmbed response.
func (r *SoundCloudResolver) parseTrackInfo(resp *SoundCloudOEmbedResponse) (title, artist string) {
	// SoundCloud title format is typically: "Track Title by Artist Name".
	// We need to split on " by " to separate track from artist.

	if strings.Contains(resp.Title, " by ") {
		parts := strings.SplitN(resp.Title, " by ", soundcloudExpectedSplitParts)
		if len(parts) == soundcloudExpectedSplitParts {
			title = strings.TrimSpace(parts[0])
			artist = strings.TrimSpace(parts[1])
			return title, artist
		}
	}

	// Fallback: use full title as track name and author_name as artist.
	title = strings.TrimSpace(resp.Title)
	artist = strings.TrimSpace(resp.AuthorName)

	return title, artist
}
