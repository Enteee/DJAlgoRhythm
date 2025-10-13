// Package spotify provides Spotify Web API integration for playlist management and track search.
package spotify

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/zmb3/spotify/v2"
	spotifyauth "github.com/zmb3/spotify/v2/auth"
	"go.uber.org/zap"
	"golang.org/x/oauth2"

	"whatdj/internal/core"
	"whatdj/pkg/fuzzy"
)

const (
	// MinValidYear represents the minimum reasonable year for music tracks
	MinValidYear = 1950
	// FilePermission is the permission for token files
	FilePermission = 0600
	// MaxSearchResults is the maximum number of search results to return
	MaxSearchResults = 10
	// ReleaseDateYearLength is the expected length of a release date year string
	ReleaseDateYearLength = 4
)

var (
	spotifyTrackRegex = regexp.MustCompile(`(?:https?://)?(?:open\.)?spotify\.com/track/([a-zA-Z0-9]+)`)
	spotifyURIRegex   = regexp.MustCompile(`spotify:track:([a-zA-Z0-9]+)`)
)

type Client struct {
	config     *core.SpotifyConfig
	logger     *zap.Logger
	client     *spotify.Client
	normalizer *fuzzy.Normalizer
	auth       *spotifyauth.Authenticator
}

type TokenData struct {
	Token *oauth2.Token `json:"token"`
}

func NewClient(config *core.SpotifyConfig, logger *zap.Logger) *Client {
	auth := spotifyauth.New(
		spotifyauth.WithRedirectURL(config.RedirectURL),
		spotifyauth.WithScopes(
			spotifyauth.ScopePlaylistModifyPublic,
			spotifyauth.ScopePlaylistModifyPrivate,
			spotifyauth.ScopePlaylistReadPrivate,
		),
		spotifyauth.WithClientID(config.ClientID),
		spotifyauth.WithClientSecret(config.ClientSecret),
	)

	return &Client{
		config:     config,
		logger:     logger,
		normalizer: fuzzy.NewNormalizer(),
		auth:       auth,
	}
}

func (c *Client) Authenticate(ctx context.Context) error {
	token, err := c.loadToken()
	if err != nil {
		c.logger.Info("No saved token found, starting OAuth flow")
		return c.startOAuthFlow(ctx)
	}

	client := spotify.New(c.auth.Client(ctx, token))
	c.client = client

	user, err := client.CurrentUser(ctx)
	if err != nil {
		c.logger.Warn("Saved token invalid, starting OAuth flow", zap.Error(err))
		return c.startOAuthFlow(ctx)
	}

	c.logger.Info("Authenticated successfully", zap.String("user", user.DisplayName))
	return nil
}

func (c *Client) SearchTrack(ctx context.Context, query string) ([]core.Track, error) {
	if c.client == nil {
		return nil, fmt.Errorf("client not authenticated")
	}

	normalizedQuery := c.normalizer.NormalizeTitle(query)

	results, err := c.client.Search(ctx, normalizedQuery, spotify.SearchTypeTrack)
	if err != nil {
		return nil, fmt.Errorf("search failed: %w", err)
	}

	if results.Tracks == nil || len(results.Tracks.Tracks) == 0 {
		return nil, fmt.Errorf("no tracks found")
	}

	var tracks []core.Track
	for i := range results.Tracks.Tracks {
		if len(tracks) >= MaxSearchResults {
			break
		}

		coreTrack := c.convertSpotifyTrack(&results.Tracks.Tracks[i])
		tracks = append(tracks, coreTrack)
	}

	return c.rankTracks(tracks, query), nil
}

func (c *Client) GetTrack(ctx context.Context, trackID string) (*core.Track, error) {
	if c.client == nil {
		return nil, fmt.Errorf("client not authenticated")
	}

	track, err := c.client.GetTrack(ctx, spotify.ID(trackID))
	if err != nil {
		return nil, fmt.Errorf("failed to get track: %w", err)
	}

	coreTrack := c.convertSpotifyTrack(track)
	return &coreTrack, nil
}

func (c *Client) AddToPlaylist(ctx context.Context, playlistID, trackID string) error {
	if c.client == nil {
		return fmt.Errorf("client not authenticated")
	}

	spotifyTrackID := spotify.ID(trackID)
	spotifyPlaylistID := spotify.ID(playlistID)

	_, err := c.client.AddTracksToPlaylist(ctx, spotifyPlaylistID, spotifyTrackID)
	if err != nil {
		return fmt.Errorf("failed to add track to playlist: %w", err)
	}

	c.logger.Info("Track added to playlist",
		zap.String("trackID", trackID),
		zap.String("playlistID", playlistID))

	return nil
}

func (c *Client) GetPlaylistTracks(ctx context.Context, playlistID string) ([]string, error) {
	if c.client == nil {
		return nil, fmt.Errorf("client not authenticated")
	}

	spotifyPlaylistID := spotify.ID(playlistID)
	var allTrackIDs []string
	limit := 100
	offset := 0

	for {
		// Note: GetPlaylistTracks is deprecated but still functional
		// TODO: Migrate to GetPlaylistItems when ready to support episodes
		tracks, err := c.client.GetPlaylistTracks(ctx, spotifyPlaylistID,
			spotify.Limit(limit), spotify.Offset(offset))
		if err != nil {
			return nil, fmt.Errorf("failed to get playlist tracks: %w", err)
		}

		for i := range tracks.Tracks {
			allTrackIDs = append(allTrackIDs, string(tracks.Tracks[i].Track.ID))
		}

		if len(tracks.Tracks) < limit {
			break
		}

		offset += limit
	}

	c.logger.Info("Retrieved playlist tracks",
		zap.String("playlistID", playlistID),
		zap.Int("count", len(allTrackIDs)))

	return allTrackIDs, nil
}

func (c *Client) ExtractTrackID(rawURL string) (string, error) {
	rawURL = strings.TrimSpace(rawURL)

	if matches := spotifyURIRegex.FindStringSubmatch(rawURL); len(matches) > 1 {
		return matches[1], nil
	}

	if matches := spotifyTrackRegex.FindStringSubmatch(rawURL); len(matches) > 1 {
		return matches[1], nil
	}

	u, err := url.Parse(rawURL)
	if err != nil {
		return "", fmt.Errorf("invalid URL: %w", err)
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

	return "", fmt.Errorf("no track ID found in URL")
}

func (c *Client) convertSpotifyTrack(track *spotify.FullTrack) core.Track {
	var artists []string
	for _, artist := range track.Artists {
		artists = append(artists, artist.Name)
	}

	var year int
	if track.Album.ReleaseDate != "" {
		if len(track.Album.ReleaseDate) >= ReleaseDateYearLength {
			if _, err := fmt.Sscanf(track.Album.ReleaseDate[:4], "%d", &year); err != nil {
				year = 0
			}
		}
	}

	return core.Track{
		ID:       string(track.ID),
		Title:    track.Name,
		Artist:   strings.Join(artists, ", "),
		Album:    track.Album.Name,
		Year:     year,
		Duration: time.Duration(track.Duration) * time.Millisecond,
		URL:      track.ExternalURLs["spotify"],
	}
}

func (c *Client) rankTracks(tracks []core.Track, originalQuery string) []core.Track {
	normalizedQuery := c.normalizer.NormalizeTitle(originalQuery)

	type scoredTrack struct {
		track core.Track
		score float64
	}

	var scored []scoredTrack

	for _, track := range tracks {
		score := c.calculateRelevanceScore(&track, normalizedQuery)
		scored = append(scored, scoredTrack{track: track, score: score})
	}

	for i := 0; i < len(scored)-1; i++ {
		for j := i + 1; j < len(scored); j++ {
			if scored[i].score < scored[j].score {
				scored[i], scored[j] = scored[j], scored[i]
			}
		}
	}

	var rankedTracks []core.Track
	for _, item := range scored {
		rankedTracks = append(rankedTracks, item.track)
	}

	return rankedTracks
}

func (c *Client) calculateRelevanceScore(track *core.Track, normalizedQuery string) float64 {
	normalizedTitle := c.normalizer.NormalizeTitle(track.Title)
	normalizedArtist := c.normalizer.NormalizeArtist(track.Artist)

	titleSimilarity := c.normalizer.CalculateSimilarity(normalizedTitle, normalizedQuery)
	combinedText := normalizedArtist + " " + normalizedTitle
	combinedSimilarity := c.normalizer.CalculateSimilarity(combinedText, normalizedQuery)

	titleWeight := 0.7
	combinedWeight := 0.3

	score := titleWeight*titleSimilarity + combinedWeight*combinedSimilarity

	if track.Year > MinValidYear {
		score += 0.1
	}

	if track.Duration > 30*time.Second && track.Duration < 10*time.Minute {
		score += 0.05
	}

	return score
}

func (c *Client) startOAuthFlow(ctx context.Context) error {
	state := "whatdj-auth-state"
	authURL := c.auth.AuthURL(state)

	fmt.Printf("Please visit the following URL to authorize the application:\n%s\n", authURL)
	fmt.Print("Enter the authorization code: ")

	var code string
	if _, err := fmt.Scanln(&code); err != nil {
		return fmt.Errorf("failed to read authorization code: %w", err)
	}

	token, err := c.auth.Exchange(ctx, code)
	if err != nil {
		return fmt.Errorf("failed to exchange code for token: %w", err)
	}

	if saveErr := c.saveToken(token); saveErr != nil {
		c.logger.Warn("Failed to save token", zap.Error(saveErr))
	}

	client := spotify.New(c.auth.Client(ctx, token))
	c.client = client

	user, err := client.CurrentUser(ctx)
	if err != nil {
		return fmt.Errorf("failed to get current user: %w", err)
	}

	c.logger.Info("OAuth flow completed successfully", zap.String("user", user.DisplayName))
	return nil
}

func (c *Client) loadToken() (*oauth2.Token, error) {
	file, err := os.Open(c.config.TokenPath)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	data, err := io.ReadAll(file)
	if err != nil {
		return nil, err
	}

	var tokenData TokenData
	if err := json.Unmarshal(data, &tokenData); err != nil {
		return nil, err
	}

	return tokenData.Token, nil
}

func (c *Client) saveToken(token *oauth2.Token) error {
	tokenData := TokenData{Token: token}

	data, err := json.MarshalIndent(tokenData, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(c.config.TokenPath, data, FilePermission)
}
