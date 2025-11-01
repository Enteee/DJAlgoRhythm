package musiclink

import (
	"context"
	"encoding/json">
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	// iTunesLookupURL is the iTunes/Apple Music API lookup endpoint.
	iTunesLookupURL = "https://itunes.apple.com/lookup"
	// AppleMusicRequestTimeout is the timeout for Apple Music API requests.
	AppleMusicRequestTimeout = 10 * time.Second
)

// iTunesLookupResponse represents the response from iTunes lookup API.
type iTunesLookupResponse struct {
	ResultCount int                `json:"resultCount"`
	Results     []iTunesTrackResult `json:"results"`
}

// iTunesTrackResult represents a track result from iTunes API.
type iTunesTrackResult struct {
	TrackID    int64  `json:"trackId"`
	TrackName  string `json:"trackName"`
	ArtistName string `json:"artistName"`
	ISRC       string `json:"isrc"`
}

// AppleMusicResolver resolves Apple Music links to track information.
type AppleMusicResolver struct {
	client *http.Client
}

// NewAppleMusicResolver creates a new Apple Music link resolver.
func NewAppleMusicResolver() *AppleMusicResolver {
	return &AppleMusicResolver{
		client: &http.Client{
			Timeout: AppleMusicRequestTimeout,
		},
	}
}

// CanResolve checks if the URL is an Apple Music link.
func (r *AppleMusicResolver) CanResolve(rawURL string) bool {
	u, err := url.Parse(rawURL)
	if err != nil {
		return false
	}

	hostname := strings.ToLower(u.Hostname())
	// Support both music.apple.com and legacy itunes.apple.com.
	return hostname == "music.apple.com" || hostname == "itunes.apple.com"
}

// Resolve extracts track information from an Apple Music URL using iTunes API.
func (r *AppleMusicResolver) Resolve(ctx context.Context, rawURL string) (*TrackInfo, error) {
	if !r.CanResolve(rawURL) {
		return nil, errors.New("not an Apple Music URL.")
	}

	// Extract track ID from the URL.
	trackID, err := r.extractTrackID(rawURL)
	if err != nil {
		return nil, fmt.Errorf("failed to extract track ID: %w", err)
	}

	// Fetch track metadata from iTunes API.
	trackData, err := r.fetchTrackData(ctx, trackID)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch track data: %w", err)
	}

	return &TrackInfo{
		Title:  trackData.TrackName,
		Artist: trackData.ArtistName,
		ISRC:   trackData.ISRC,
	}, nil
}

// extractTrackID extracts the track ID from an Apple Music URL.
func (r *AppleMusicResolver) extractTrackID(rawURL string) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", err
	}

	// Check for track ID in query parameter ?i=<trackId>.
	trackID := u.Query().Get("i")
	if trackID != "" {
		return trackID, nil
	}

	// Check if this is a direct song link (music.apple.com/us/song/...).
	// Path format: /us/song/<song-name>/<song-id>
	if strings.Contains(u.Path, "/song/") {
		parts := strings.Split(strings.Trim(u.Path, "/"), "/")
		if len(parts) > 0 {
			// The last part should be the song ID.
			songID := parts[len(parts)-1]
			if songID != "" {
				return songID, nil
			}
		}
	}

	return "", errors.New("no track ID found in Apple Music URL (album links without ?i= are not supported).")
}

// fetchTrackData fetches track metadata from iTunes lookup API.
func (r *AppleMusicResolver) fetchTrackData(ctx context.Context, trackID string) (*iTunesTrackResult, error) {
	// Build lookup request URL.
	reqURL := fmt.Sprintf("%s?id=%s&entity=song", iTunesLookupURL, trackID)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, http.NoBody)
	if err != nil {
		return nil, err
	}

	resp, err := r.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("iTunes API returned status %d", resp.StatusCode)
	}

	var lookupResp iTunesLookupResponse
	if err := json.NewDecoder(resp.Body).Decode(&lookupResp); err != nil {
		return nil, fmt.Errorf("failed to decode iTunes API response: %w", err)
	}

	if lookupResp.ResultCount == 0 || len(lookupResp.Results) == 0 {
		return nil, errors.New("no track found in iTunes API response.")
	}

	return &lookupResp.Results[0], nil
}
