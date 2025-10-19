// Package spotify provides Spotify Web API integration for playlist management and track search.
package spotify

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
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
	// URLResolveTimeout is the timeout for resolving shortened URLs
	URLResolveTimeout = 10 * time.Second
	// MaxRedirects is the maximum number of redirects to follow
	MaxRedirects = 10
	// ReadBufferSize is the buffer size for reading page content
	ReadBufferSize = 8192
	// SpotifyAppLinkDomain is the domain for Spotify app links
	SpotifyAppLinkDomain = "spotify.app.link"
	// PlaylistCacheDuration is how long to cache playlist contents
	PlaylistCacheDuration = 5 * time.Minute
	// PlaybackStartDelay is the delay to wait for playback to start
	PlaybackStartDelay = 500 * time.Millisecond
)

var (
	spotifyTrackRegex = regexp.MustCompile(`(?:https?://)?(?:open\.)?spotify\.com/track/([a-zA-Z0-9]+)`)
	spotifyURIRegex   = regexp.MustCompile(`spotify:track:([a-zA-Z0-9]+)`)
)

type Client struct {
	config             *core.SpotifyConfig
	logger             *zap.Logger
	client             *spotify.Client
	normalizer         *fuzzy.Normalizer
	auth               *spotifyauth.Authenticator
	targetPlaylist     string          // Playlist ID we're managing
	lastKnownVolume    int             // Store last volume for fade recovery
	playlistTrackCache map[string]bool // Cache of track IDs in our playlist
	cacheLastUpdated   time.Time       // When cache was last updated
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
			spotifyauth.ScopeUserModifyPlaybackState,
			spotifyauth.ScopeUserReadCurrentlyPlaying,
			spotifyauth.ScopeUserReadPlaybackState,
		),
		spotifyauth.WithClientID(config.ClientID),
		spotifyauth.WithClientSecret(config.ClientSecret),
	)

	return &Client{
		config:             config,
		logger:             logger,
		normalizer:         fuzzy.NewNormalizer(),
		auth:               auth,
		playlistTrackCache: make(map[string]bool),
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
	return c.AddToPlaylistAtPosition(ctx, playlistID, trackID, -1) // -1 means append to end
}

func (c *Client) AddToPlaylistAtPosition(ctx context.Context, playlistID, trackID string, position int) error {
	if c.client == nil {
		return fmt.Errorf("client not authenticated")
	}

	spotifyTrackID := spotify.ID(trackID)
	spotifyPlaylistID := spotify.ID(playlistID)

	if position < 0 {
		// Add to end of playlist (existing behavior)
		_, err := c.client.AddTracksToPlaylist(ctx, spotifyPlaylistID, spotifyTrackID)
		if err != nil {
			return fmt.Errorf("failed to add track to playlist: %w", err)
		}

		// Invalidate cache since we added a track
		c.invalidatePlaylistCache()
		c.logger.Info("Track added to playlist",
			zap.String("trackID", trackID),
			zap.String("playlistID", playlistID),
			zap.String("position", "end"))
		return nil
	}

	// For specific positions, we need to add then reorder
	// Step 1: Add track to end of playlist
	_, err := c.client.AddTracksToPlaylist(ctx, spotifyPlaylistID, spotifyTrackID)
	if err != nil {
		return fmt.Errorf("failed to add track to playlist: %w", err)
	}

	// Step 2: Get current playlist length to know where the track was added
	items, err := c.client.GetPlaylistItems(ctx, spotifyPlaylistID, spotify.Limit(1))
	if err != nil {
		// Track was added but we can't reorder - this is still a success
		c.logger.Warn("Track added but failed to get playlist info for reordering",
			zap.String("trackID", trackID),
			zap.Error(err))
		return nil
	}

	// Step 3: Reorder the last track (newly added) to the specified position
	trackPosition := items.Total - 1 // Last position (0-indexed)
	reorderOpts := spotify.PlaylistReorderOptions{
		RangeStart:   trackPosition,
		RangeLength:  1,
		InsertBefore: position,
	}

	_, err = c.client.ReorderPlaylistTracks(ctx, spotifyPlaylistID, reorderOpts)
	if err != nil {
		// Track was added but reorder failed - this is still a success
		c.logger.Warn("Track added but failed to reorder to priority position",
			zap.String("trackID", trackID),
			zap.Int("targetPosition", position),
			zap.Error(err))
		return nil
	}

	// Invalidate cache since we added a track
	c.invalidatePlaylistCache()
	c.logger.Info("Track added to playlist with priority positioning",
		zap.String("trackID", trackID),
		zap.String("playlistID", playlistID),
		zap.Int("position", position))

	return nil
}

func (c *Client) AddToQueue(ctx context.Context, trackID string) error {
	if c.client == nil {
		return fmt.Errorf("client not authenticated")
	}

	spotifyTrackID := spotify.ID(trackID)

	err := c.client.QueueSong(ctx, spotifyTrackID)
	if err != nil {
		return fmt.Errorf("failed to add track to queue: %w", err)
	}

	c.logger.Info("Track added to queue",
		zap.String("trackID", trackID))

	return nil
}

// GetQueuePosition finds the position of a track in the user's queue
// Returns the position (0-based) or -1 if not found
func (c *Client) GetQueuePosition(ctx context.Context, trackID string) (int, error) {
	if c.client == nil {
		return -1, fmt.Errorf("client not authenticated")
	}

	queue, err := c.client.GetQueue(ctx)
	if err != nil {
		return -1, fmt.Errorf("failed to get user queue: %w", err)
	}

	// Search through the queue for our track
	for i := range queue.Items {
		if queue.Items[i].ID == spotify.ID(trackID) {
			return i, nil
		}
	}

	// Track not found in queue
	return -1, nil
}

// IsInAutoPlay detects if Spotify is currently in auto-play mode
// Returns true if the currently playing track is not in our managed playlist,
// even if the context still shows our playlist URI
func (c *Client) IsInAutoPlay(ctx context.Context) (bool, error) {
	if c.client == nil {
		return false, fmt.Errorf("client not authenticated")
	}

	currently, err := c.client.PlayerCurrentlyPlaying(ctx)
	if err != nil {
		return false, fmt.Errorf("failed to get currently playing: %w", err)
	}

	// No playback active
	if currently == nil || !currently.Playing {
		return false, nil
	}

	// Get the currently playing track ID
	if currently.Item == nil {
		return true, nil // No track info likely means auto-play
	}

	currentTrackID := string(currently.Item.ID)
	if currentTrackID == "" {
		return true, nil // No track ID likely means auto-play
	}

	// If no target playlist set, use context-based detection as fallback
	if c.targetPlaylist == "" {
		if currently.PlaybackContext.URI == "" {
			return true, nil // No context likely means auto-play
		}
		return false, nil // Can't determine without target playlist
	}

	// Check if the currently playing track exists in our playlist
	trackInPlaylist, err := c.isTrackInPlaylist(ctx, currentTrackID)
	if err != nil {
		c.logger.Warn("Failed to check if track is in playlist, falling back to context check",
			zap.Error(err))
		// Fallback to context-based detection
		contextURI := string(currently.PlaybackContext.URI)
		expectedURI := fmt.Sprintf("spotify:playlist:%s", c.targetPlaylist)
		return contextURI != expectedURI, nil
	}

	contextURI := string(currently.PlaybackContext.URI)
	expectedURI := fmt.Sprintf("spotify:playlist:%s", c.targetPlaylist)
	isContextCorrect := contextURI == expectedURI

	// Auto-play detected if:
	// 1. Track is not in our playlist, OR
	// 2. Context doesn't match our playlist
	isAutoPlay := !trackInPlaylist || !isContextCorrect

	c.logger.Debug("Auto-play detection",
		zap.String("currentTrackID", currentTrackID),
		zap.Bool("trackInPlaylist", trackInPlaylist),
		zap.String("currentContext", contextURI),
		zap.String("expectedContext", expectedURI),
		zap.Bool("isContextCorrect", isContextCorrect),
		zap.Bool("isAutoPlay", isAutoPlay))

	return isAutoPlay, nil
}

// SetTargetPlaylist sets the playlist ID that we're managing for auto-play detection
func (c *Client) SetTargetPlaylist(playlistID string) {
	c.targetPlaylist = playlistID
	// Clear cache when target playlist changes
	c.playlistTrackCache = make(map[string]bool)
	c.cacheLastUpdated = time.Time{}
	c.logger.Info("Target playlist set for auto-play detection", zap.String("playlistID", playlistID))
}

// refreshPlaylistCache updates the cached playlist tracks if cache is stale
func (c *Client) refreshPlaylistCache(ctx context.Context) error {
	if c.targetPlaylist == "" {
		return fmt.Errorf("no target playlist set")
	}

	// Check if cache is still valid
	if time.Since(c.cacheLastUpdated) < PlaylistCacheDuration {
		return nil // Cache is still fresh
	}

	// Fetch fresh playlist tracks
	playlistTracks, err := c.GetPlaylistTracks(ctx, c.targetPlaylist)
	if err != nil {
		return fmt.Errorf("failed to refresh playlist cache: %w", err)
	}

	// Update cache
	c.playlistTrackCache = make(map[string]bool)
	for _, trackID := range playlistTracks {
		c.playlistTrackCache[trackID] = true
	}
	c.cacheLastUpdated = time.Now()

	c.logger.Debug("Playlist cache refreshed",
		zap.String("playlistID", c.targetPlaylist),
		zap.Int("trackCount", len(playlistTracks)))

	return nil
}

// isTrackInPlaylist checks if a track is in our cached playlist
func (c *Client) isTrackInPlaylist(ctx context.Context, trackID string) (bool, error) {
	if err := c.refreshPlaylistCache(ctx); err != nil {
		return false, err
	}
	return c.playlistTrackCache[trackID], nil
}

// invalidatePlaylistCache clears the playlist cache to force refresh
func (c *Client) invalidatePlaylistCache() {
	c.playlistTrackCache = make(map[string]bool)
	c.cacheLastUpdated = time.Time{}
}

// getCurrentVolume gets the current volume from the active device
func (c *Client) getCurrentVolume(ctx context.Context) error {
	devices, err := c.client.PlayerDevices(ctx)
	if err != nil {
		return fmt.Errorf("failed to get devices: %w", err)
	}

	// Find the active device
	for _, device := range devices {
		if device.Active {
			c.lastKnownVolume = device.Volume
			c.logger.Debug("Got current volume from active device",
				zap.String("deviceName", device.Name),
				zap.Int("volume", device.Volume))
			return nil
		}
	}

	// No active device found
	return fmt.Errorf("no active device found")
}

// getTrackPositionInPlaylist finds the position of a track in the current playlist
func (c *Client) getTrackPositionInPlaylist(ctx context.Context, trackID string) (int, error) {
	// Invalidate cache to get fresh playlist data since we just added a track
	c.invalidatePlaylistCache()

	// Get current playlist tracks to find the position of our track
	playlistTracks, err := c.GetPlaylistTracks(ctx, c.targetPlaylist)
	if err != nil {
		return -1, fmt.Errorf("failed to get playlist tracks: %w", err)
	}

	c.logger.Debug("Searching for track in playlist",
		zap.String("trackID", trackID),
		zap.Int("playlistLength", len(playlistTracks)))

	// Find the track position in the playlist (should be at the end since we just added it)
	for i, id := range playlistTracks {
		if id == trackID {
			c.logger.Debug("Found track in playlist",
				zap.String("trackID", trackID),
				zap.Int("position", i))
			return i, nil
		}
	}

	c.logger.Warn("Track not found in playlist",
		zap.String("trackID", trackID),
		zap.Int("playlistLength", len(playlistTracks)))
	return -1, fmt.Errorf("track %s not found in playlist", trackID)
}

// RecoverFromAutoPlay returns playback to our target playlist
// Uses fade-out strategy: add track to playlist, fade out, start new track, restore volume
func (c *Client) RecoverFromAutoPlay(ctx context.Context, newTrackID string) error {
	if c.client == nil {
		return fmt.Errorf("client not authenticated")
	}

	if c.targetPlaylist == "" {
		return fmt.Errorf("no target playlist set")
	}

	c.logger.Info("Starting auto-play recovery",
		zap.String("trackID", newTrackID),
		zap.String("targetPlaylist", c.targetPlaylist))

	// Step 0: Add track to playlist first
	if err := c.AddToPlaylist(ctx, c.targetPlaylist, newTrackID); err != nil {
		return fmt.Errorf("failed to add track to playlist during recovery: %w", err)
	}

	// Give Spotify API time to update the playlist
	time.Sleep(PlaybackStartDelay)

	// Step 1: Get current volume from device
	if err := c.getCurrentVolume(ctx); err != nil {
		c.logger.Warn("Failed to get current volume, using default", zap.Error(err))
		c.lastKnownVolume = 50 // Default fallback
	}

	// Step 2: Fade out current track
	c.fadeOut(ctx)

	// Step 3: Start new track directly in playlist context
	// First get the track position in the playlist
	trackPosition, err := c.getTrackPositionInPlaylist(ctx, newTrackID)
	if err != nil {
		c.logger.Warn("Failed to find track position, trying direct track play", zap.Error(err))
		// Fallback: play track directly
		trackURI := spotify.URI(fmt.Sprintf("spotify:track:%s", newTrackID))
		trackPlayOpts := &spotify.PlayOptions{
			URIs: []spotify.URI{trackURI},
		}
		if trackErr := c.client.PlayOpt(ctx, trackPlayOpts); trackErr != nil {
			return fmt.Errorf("failed to start track playback: %w", trackErr)
		}
	} else {
		// Start playlist directly at the new track position
		playlistURI := spotify.URI(fmt.Sprintf("spotify:playlist:%s", c.targetPlaylist))
		playOpts := &spotify.PlayOptions{
			PlaybackContext: &playlistURI,
			PlaybackOffset: &spotify.PlaybackOffset{
				Position: &trackPosition,
			},
		}

		playErr := c.client.PlayOpt(ctx, playOpts)
		if playErr != nil {
			c.logger.Warn("Failed to start playlist at track position, trying direct track play", zap.Error(playErr))
			// Fallback: play track directly
			trackURI := spotify.URI(fmt.Sprintf("spotify:track:%s", newTrackID))
			trackPlayOpts := &spotify.PlayOptions{
				URIs: []spotify.URI{trackURI},
			}
			if trackErr := c.client.PlayOpt(ctx, trackPlayOpts); trackErr != nil {
				return fmt.Errorf("failed to start track playback: %w", trackErr)
			}
		} else {
			c.logger.Info("Successfully started playlist at new track position",
				zap.String("trackID", newTrackID),
				zap.Int("position", trackPosition))
		}
	}

	// Step 4: Restore volume
	c.fadeIn(ctx)

	c.logger.Info("Auto-play recovery completed successfully")
	return nil
}

// fadeOut gradually reduces volume to 0
func (c *Client) fadeOut(ctx context.Context) {
	const fadeSteps = 10
	const stepDuration = 200 * time.Millisecond

	// Ensure we have a valid starting volume
	if c.lastKnownVolume <= 0 {
		c.lastKnownVolume = 50 // Reasonable default
	}

	c.logger.Debug("Starting fade out", zap.Int("fromVolume", c.lastKnownVolume))

	for i := fadeSteps; i >= 0; i-- {
		volumePercent := (c.lastKnownVolume * i) / fadeSteps
		if err := c.client.Volume(ctx, volumePercent); err != nil {
			c.logger.Warn("Failed to set volume during fade out",
				zap.Int("targetVolume", volumePercent),
				zap.Error(err))
			// Continue with fade even if volume control fails
		}
		time.Sleep(stepDuration)
	}
}

// fadeIn gradually restores volume
func (c *Client) fadeIn(ctx context.Context) {
	const fadeSteps = 10
	const stepDuration = 200 * time.Millisecond

	c.logger.Debug("Starting fade in", zap.Int("toVolume", c.lastKnownVolume))

	for i := 0; i <= fadeSteps; i++ {
		volumePercent := (c.lastKnownVolume * i) / fadeSteps
		if err := c.client.Volume(ctx, volumePercent); err != nil {
			c.logger.Warn("Failed to set volume during fade in",
				zap.Int("targetVolume", volumePercent),
				zap.Error(err))
			// Continue with fade even if volume control fails
		}
		time.Sleep(stepDuration)
	}
}

// Note: Immediate playbook methods removed in favor of simpler queue-based priority approach

func (c *Client) GetPlaylistTracks(ctx context.Context, playlistID string) ([]string, error) {
	if c.client == nil {
		return nil, fmt.Errorf("client not authenticated")
	}

	spotifyPlaylistID := spotify.ID(playlistID)
	var allTrackIDs []string
	limit := 100
	offset := 0

	for {
		items, err := c.client.GetPlaylistItems(ctx, spotifyPlaylistID,
			spotify.Limit(limit), spotify.Offset(offset))
		if err != nil {
			return nil, fmt.Errorf("failed to get playlist items: %w", err)
		}

		for i := range items.Items {
			// Only process tracks (not episodes or null items)
			if items.Items[i].Track.Track != nil {
				allTrackIDs = append(allTrackIDs, string(items.Items[i].Track.Track.ID))
			}
		}

		if len(items.Items) < limit {
			break
		}

		offset += limit
	}

	c.logger.Info("Retrieved playlist tracks",
		zap.String("playlistID", playlistID),
		zap.Int("count", len(allTrackIDs)))

	return allTrackIDs, nil
}

// resolveShortURL resolves shortened Spotify URLs to their final destination
func (c *Client) resolveShortURL(shortURL string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), URLResolveTimeout)
	defer cancel()

	client := &http.Client{
		Timeout: URLResolveTimeout,
		CheckRedirect: func(_ *http.Request, via []*http.Request) error {
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

	req.Header.Set("User-Agent",
		"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	finalURL := resp.Request.URL.String()

	// Check if we got a Spotify track URL
	u, err := url.Parse(finalURL)
	if err != nil {
		return "", err
	}

	hostname := strings.ToLower(u.Hostname())
	if hostname == "open.spotify.com" && strings.Contains(u.Path, "/track/") {
		return finalURL, nil
	}

	// If still a shortened URL, try fetching page content
	if hostname == SpotifyAppLinkDomain {
		return c.resolveWithPageContent(shortURL)
	}

	return "", fmt.Errorf("URL did not resolve to a Spotify track")
}

// resolveWithPageContent fetches page content to extract Spotify URL
func (c *Client) resolveWithPageContent(shortURL string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), URLResolveTimeout)
	defer cancel()

	client := &http.Client{Timeout: URLResolveTimeout}

	req, err := http.NewRequestWithContext(ctx, "GET", shortURL, http.NoBody)
	if err != nil {
		return "", err
	}

	req.Header.Set("User-Agent",
		"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,image/webp,*/*;q=0.8")

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	// Read page content to extract Spotify URL
	buf := make([]byte, ReadBufferSize)
	n, _ := resp.Body.Read(buf)
	content := string(buf[:n])

	// Extract Spotify track URL using regex
	spotifyURLRegex := regexp.MustCompile(`https://open\.spotify\.com/track/[a-zA-Z0-9]+`)
	matches := spotifyURLRegex.FindStringSubmatch(content)

	if len(matches) > 0 {
		return matches[0], nil
	}

	return "", fmt.Errorf("could not find Spotify track URL in page content")
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

	// Handle shortened URLs by resolving them first
	hostname := strings.ToLower(u.Hostname())
	if hostname == "spotify.link" || hostname == SpotifyAppLinkDomain {
		resolvedURL, err := c.resolveShortURL(rawURL)
		if err != nil {
			return "", fmt.Errorf("failed to resolve shortened URL: %w", err)
		}
		// Recursively extract from the resolved URL
		return c.ExtractTrackID(resolvedURL)
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
