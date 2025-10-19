// Package spotify provides Spotify Web API integration for playlist management and track search.
package spotify

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand/v2"
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
	// SkipDelay is the delay between track skips to avoid rate limiting
	SkipDelay = 100 * time.Millisecond

	// RepeatStateTrack represents the "track" repeat state
	RepeatStateTrack = "track"
	// RepeatStateOff represents the "off" repeat state
	RepeatStateOff = "off"
	// RepeatStateContext represents the "context" repeat state
	RepeatStateContext = "context"
	// DefaultStatusNone represents the default "none" status
	DefaultStatusNone = "none"

	// QueueClearDelay is the delay to wait for queue clearing
	QueueClearDelay = 500 * time.Millisecond
	// TrackAddDelay is the delay between track additions to avoid rate limits
	TrackAddDelay = 100 * time.Millisecond
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

// verifyPlaybackState checks and logs the current playback state after recovery
func (c *Client) verifyPlaybackState(ctx context.Context) error {
	state, err := c.client.PlayerState(ctx)
	if err != nil {
		return fmt.Errorf("failed to get player state: %w", err)
	}

	if state == nil {
		c.logger.Warn("No current playback after recovery")
		return nil
	}

	contextURI := ""
	contextType := ""
	if state.PlaybackContext.URI != "" {
		contextURI = string(state.PlaybackContext.URI)
		contextType = state.PlaybackContext.Type
	}

	trackID := ""
	trackName := ""
	if state.Item != nil {
		trackID = string(state.Item.ID)
		trackName = state.Item.Name
	}

	c.logger.Info("Playback state after recovery",
		zap.String("contextURI", contextURI),
		zap.String("contextType", contextType),
		zap.String("trackID", trackID),
		zap.String("trackName", trackName),
		zap.Bool("isPlaying", state.Playing),
		zap.Bool("shuffleState", state.ShuffleState),
		zap.String("repeatState", state.RepeatState))

	// Check if shuffle is enabled and log a warning
	if state.ShuffleState {
		c.logger.Warn("Shuffle is enabled - this may prevent new tracks from being added to queue in order")
	}

	// Check repeat state
	if state.RepeatState != "off" {
		c.logger.Info("Repeat mode is active", zap.String("repeatState", state.RepeatState))
	}

	return nil
}

// ensureOptimalPlaybackSettings ensures shuffle is off and repeat is appropriate for queueing
func (c *Client) ensureOptimalPlaybackSettings(ctx context.Context) error {
	state, err := c.client.PlayerState(ctx)
	if err != nil {
		return fmt.Errorf("failed to get player state: %w", err)
	}

	if state == nil {
		return nil // No playback to adjust
	}

	// Disable shuffle if it's enabled (shuffle prevents proper queueing)
	if state.ShuffleState {
		c.logger.Info("Disabling shuffle to ensure proper track queueing")
		if err := c.client.Shuffle(ctx, false); err != nil {
			c.logger.Warn("Failed to disable shuffle", zap.Error(err))
		}
	}

	// Set repeat to context (playlist) if it's not already optimal
	if state.RepeatState == RepeatStateTrack {
		c.logger.Info("Changing repeat from track to context for better queueing")
		if err := c.client.Repeat(ctx, RepeatStateContext); err != nil {
			c.logger.Warn("Failed to set repeat to context", zap.Error(err))
		}
	}

	return nil
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

	// Step 1: Add track to playlist with snapshot tracking
	if err := c.addTrackWithSnapshotTracking(ctx, newTrackID); err != nil {
		return fmt.Errorf("failed to add track during recovery: %w", err)
	}

	// Step 2: Prepare for playback switch
	c.preparePlaybackSwitch(ctx)

	// Step 3: Switch to playlist playback
	if err := c.switchToPlaylistPlayback(ctx, newTrackID); err != nil {
		return fmt.Errorf("failed to switch to playlist playback: %w", err)
	}

	// Step 4: Finalize recovery
	c.finalizeRecovery(ctx)

	c.logger.Info("Auto-play recovery completed successfully")
	return nil
}

// addTrackWithSnapshotTracking adds track to playlist and tracks snapshot changes
func (c *Client) addTrackWithSnapshotTracking(ctx context.Context, trackID string) error {
	c.logger.Info("Getting current playlist state for snapshot tracking")

	// Get current playlist to see the snapshot before adding
	currentPlaylist, err := c.client.GetPlaylist(ctx, spotify.ID(c.targetPlaylist))
	if err != nil {
		c.logger.Warn("Could not get current playlist state", zap.Error(err))
	} else {
		c.logger.Info("Current playlist state",
			zap.String("snapshotID", currentPlaylist.SnapshotID),
			zap.Int("trackCount", currentPlaylist.Tracks.Total))
	}

	// Add track and capture new snapshot ID
	c.logger.Info("Adding track to playlist with snapshot tracking")
	newSnapshotID, err := c.client.AddTracksToPlaylist(ctx, spotify.ID(c.targetPlaylist), spotify.ID(trackID))
	if err != nil {
		return fmt.Errorf("failed to add track to playlist: %w", err)
	}

	c.logger.Info("Track added to playlist",
		zap.String("newSnapshotID", newSnapshotID),
		zap.String("trackID", trackID))

	// Invalidate our cache since we just added a track
	c.invalidatePlaylistCache()

	// Give Spotify API time to update the playlist
	time.Sleep(PlaybackStartDelay)

	// Verify playlist state after adding
	c.verifyPlaylistUpdate(ctx, currentPlaylist)
	return nil
}

// preparePlaybackSwitch prepares for switching playback (volume, fade out)
func (c *Client) preparePlaybackSwitch(ctx context.Context) {
	// Get current volume from device
	if volumeErr := c.getCurrentVolume(ctx); volumeErr != nil {
		c.logger.Warn("Failed to get current volume, using default", zap.Error(volumeErr))
		c.lastKnownVolume = 50 // Default fallback
	}

	// Fade out current track
	c.fadeOut(ctx)
}

// switchToPlaylistPlayback switches playback to the target track in the playlist
func (c *Client) switchToPlaylistPlayback(ctx context.Context, trackID string) error {
	// Find the track position in the playlist first
	trackPosition, err := c.getTrackPositionInPlaylist(ctx, trackID)
	if err != nil {
		c.logger.Warn("Could not find track position, falling back to direct track play", zap.Error(err))
		return c.playTrackDirectly(ctx, trackID)
	}

	// Play playlist starting at the specific track position
	playlistURI := spotify.URI(fmt.Sprintf("spotify:playlist:%s", c.targetPlaylist))
	playOpts := &spotify.PlayOptions{
		PlaybackContext: &playlistURI,
		PlaybackOffset: &spotify.PlaybackOffset{
			Position: &trackPosition,
		},
	}

	c.logger.Info("Playing playlist at track position",
		zap.String("trackID", trackID),
		zap.String("playlistURI", string(playlistURI)),
		zap.Int("position", trackPosition))

	if err := c.client.PlayOpt(ctx, playOpts); err != nil {
		c.logger.Warn("Failed to play playlist at position, falling back to direct track", zap.Error(err))
		return c.playTrackDirectly(ctx, trackID)
	}

	c.logger.Info("Successfully started playlist at track position")
	return nil
}

// playTrackDirectly plays a track directly as fallback
func (c *Client) playTrackDirectly(ctx context.Context, trackID string) error {
	trackURI := spotify.URI(fmt.Sprintf("spotify:track:%s", trackID))
	fallbackOpts := &spotify.PlayOptions{
		URIs: []spotify.URI{trackURI},
	}
	if err := c.client.PlayOpt(ctx, fallbackOpts); err != nil {
		return fmt.Errorf("failed to start track playback: %w", err)
	}
	c.logger.Info("Started direct track playback as fallback")
	return nil
}

// verifyPlaylistUpdate verifies that the playlist was updated correctly
func (c *Client) verifyPlaylistUpdate(ctx context.Context, currentPlaylist *spotify.FullPlaylist) {
	updatedPlaylist, err := c.client.GetPlaylist(ctx, spotify.ID(c.targetPlaylist))
	if err != nil {
		c.logger.Warn("Could not get updated playlist state", zap.Error(err))
		return
	}

	snapshotChanged := false
	if currentPlaylist != nil {
		snapshotChanged = updatedPlaylist.SnapshotID != currentPlaylist.SnapshotID
	}
	c.logger.Info("Updated playlist state after adding track",
		zap.String("snapshotID", updatedPlaylist.SnapshotID),
		zap.Int("trackCount", updatedPlaylist.Tracks.Total),
		zap.Bool("snapshotChanged", snapshotChanged))
}

// finalizeRecovery completes the recovery process
func (c *Client) finalizeRecovery(ctx context.Context) {
	// Ensure optimal playback settings for proper queueing
	if err := c.ensureOptimalPlaybackSettings(ctx); err != nil {
		c.logger.Warn("Could not ensure optimal playback settings", zap.Error(err))
	}

	// Restore volume
	c.fadeIn(ctx)

	// Skip queue check during recovery since track is currently playing
	c.logger.Info("Skipping queue check during auto-play recovery since track is now playing")

	// Verify the final playback state
	if err := c.verifyPlaybackState(ctx); err != nil {
		c.logger.Warn("Could not verify playback state after recovery", zap.Error(err))
	}
}

// EnsureTrackInQueue checks if a track is in the queue and adds it if not
// This should be called after adding tracks to playlist to ensure proper queueing
func (c *Client) EnsureTrackInQueue(ctx context.Context, trackID string) error {
	c.logger.Info("Checking if track is in queue", zap.String("trackID", trackID))

	queuePosition, err := c.GetQueuePosition(ctx, trackID)
	if err != nil {
		c.logger.Warn("Could not check queue position", zap.Error(err))
		return err
	}

	if queuePosition == -1 {
		c.logger.Info("Track not found in queue, adding manually", zap.String("trackID", trackID))

		// Add track to queue manually
		if queueErr := c.AddToQueue(ctx, trackID); queueErr != nil {
			c.logger.Warn("Failed to add track to queue manually", zap.Error(queueErr))
			return queueErr
		}

		c.logger.Info("Successfully added track to queue manually")

		// Check queue position again after manual addition
		time.Sleep(1 * time.Second) // Give API time to update
		newQueuePosition, checkErr := c.GetQueuePosition(ctx, trackID)
		if checkErr != nil {
			c.logger.Warn("Could not verify queue position after manual addition", zap.Error(checkErr))
		} else {
			c.logger.Info("Track queue status after manual addition",
				zap.String("trackID", trackID),
				zap.Int("queuePosition", newQueuePosition))
		}
	} else {
		c.logger.Info("Track found in queue",
			zap.String("trackID", trackID),
			zap.Int("queuePosition", queuePosition))
	}

	return nil
}

// IsNearPlaylistEnd checks if we're playing the last track of the playlist
// Returns true if currently playing the very last track
func (c *Client) IsNearPlaylistEnd(ctx context.Context) (bool, error) {
	if c.client == nil {
		return false, fmt.Errorf("client not authenticated")
	}

	if c.targetPlaylist == "" {
		return false, fmt.Errorf("no target playlist set")
	}

	// Get current playback state
	state, err := c.client.PlayerState(ctx)
	if err != nil {
		return false, fmt.Errorf("failed to get player state: %w", err)
	}

	if state == nil || state.Item == nil {
		return false, nil
	}

	// Check if we're playing from our target playlist
	playlistURI := fmt.Sprintf("spotify:playlist:%s", c.targetPlaylist)
	if state.PlaybackContext.URI != spotify.URI(playlistURI) {
		return false, nil // Not playing from our playlist
	}

	// Get current track ID and playlist tracks
	currentTrackID := string(state.Item.ID)
	playlistTracks, err := c.GetPlaylistTracks(ctx, c.targetPlaylist)
	if err != nil {
		return false, fmt.Errorf("failed to get playlist tracks: %w", err)
	}

	// Find current track position in playlist
	for i, trackID := range playlistTracks {
		if trackID == currentTrackID {
			// Consider "near end" only if we're playing the very last track
			return i == len(playlistTracks)-1, nil
		}
	}

	return false, nil // Current track not found in playlist
}

// GetAutoPlayPreventionTrack gets a track ID from Spotify's prepared auto-play queue for prevention
func (c *Client) GetAutoPlayPreventionTrack(ctx context.Context) (string, error) {
	if c.client == nil {
		return "", fmt.Errorf("client not authenticated")
	}

	// Get the current queue to see what Spotify has prepared for auto-play
	queue, err := c.client.GetQueue(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to get current queue: %w", err)
	}

	c.logger.Debug("Checking queue for auto-play tracks",
		zap.Int("queueLength", len(queue.Items)),
		zap.String("currentlyPlaying", func() string {
			if queue.CurrentlyPlaying.ID != "" {
				return string(queue.CurrentlyPlaying.ID)
			}
			return DefaultStatusNone
		}()))

	// Get current playlist tracks to determine which queue items are auto-play
	playlistTracks, err := c.GetPlaylistTracks(ctx, c.targetPlaylist)
	if err != nil {
		return "", fmt.Errorf("failed to get playlist tracks: %w", err)
	}

	// Create a set for fast lookup of playlist tracks
	playlistTrackSet := make(map[string]bool)
	for _, trackID := range playlistTracks {
		playlistTrackSet[trackID] = true
	}

	// Find all queued tracks that are NOT in our playlist (these would be auto-play)
	var autoPlayTracks []spotify.FullTrack
	for i := range queue.Items {
		trackID := string(queue.Items[i].ID)
		if !playlistTrackSet[trackID] {
			autoPlayTracks = append(autoPlayTracks, queue.Items[i])
		}
	}

	if len(autoPlayTracks) > 0 {
		// Pick a random track from the auto-play candidates
		randomIndex := rand.IntN(len(autoPlayTracks)) //nolint:gosec // Non-cryptographic randomness is sufficient for song selection
		selectedTrack := autoPlayTracks[randomIndex]
		trackID := string(selectedTrack.ID)

		c.logger.Info("Found auto-play tracks in queue, selecting random one",
			zap.Int("totalAutoPlayTracks", len(autoPlayTracks)),
			zap.String("selectedTrackID", trackID),
			zap.String("title", selectedTrack.Name),
			zap.String("artist", func() string {
				if len(selectedTrack.Artists) > 0 {
					return selectedTrack.Artists[0].Name
				}
				return "Unknown"
			}()))

		c.logger.Info("Selected random auto-play prevention track",
			zap.String("trackID", trackID))
		return trackID, nil
	}

	// If no auto-play tracks found in queue, fall back to search
	c.logger.Info("No auto-play tracks found in queue, using search fallback")

	tracks, err := c.SearchTrack(ctx, "popular music")
	if err != nil {
		return "", fmt.Errorf("failed to search for fallback track: %w", err)
	}

	if len(tracks) == 0 {
		return "", fmt.Errorf("no tracks found in search")
	}

	// Use the first search result
	trackID := tracks[0].ID
	c.logger.Info("Selected fallback track for auto-play prevention",
		zap.String("trackID", trackID))
	return trackID, nil
}

// AddAutoPlayPreventionTrack gets and adds a track from Spotify's auto-play queue (legacy method)
func (c *Client) AddAutoPlayPreventionTrack(ctx context.Context) (string, error) {
	trackID, err := c.GetAutoPlayPreventionTrack(ctx)
	if err != nil {
		return "", err
	}

	if err := c.AddToPlaylist(ctx, c.targetPlaylist, trackID); err != nil {
		return "", fmt.Errorf("failed to add auto-play prevention track to playlist: %w", err)
	}

	c.logger.Info("Added auto-play prevention track to playlist",
		zap.String("trackID", trackID))
	return trackID, nil
}

// RebuildQueueWithPriority clears the current queue and rebuilds it with the priority track first
// This is needed because Spotify API doesn't support queue reordering
func (c *Client) RebuildQueueWithPriority(ctx context.Context, priorityTrackID string) error {
	if c.client == nil {
		return fmt.Errorf("client not authenticated")
	}

	c.logger.Info("Rebuilding queue with priority track", zap.String("priorityTrackID", priorityTrackID))

	// Step 1: Get current queue to preserve other tracks
	currentQueue, err := c.client.GetQueue(ctx)
	if err != nil {
		c.logger.Warn("Could not get current queue for rebuilding", zap.Error(err))
		// Continue anyway - just add priority track to current queue
		return c.AddToQueue(ctx, priorityTrackID)
	}

	// Extract track IDs from current queue (excluding currently playing track)
	var queuedTrackIDs []string
	for i := range currentQueue.Items {
		if currentQueue.Items[i].ID != "" {
			queuedTrackIDs = append(queuedTrackIDs, string(currentQueue.Items[i].ID))
		}
	}

	c.logger.Info("Current queue state before rebuild",
		zap.Int("queueLength", len(queuedTrackIDs)),
		zap.String("currentlyPlaying", func() string {
			if currentQueue.CurrentlyPlaying.ID != "" {
				return string(currentQueue.CurrentlyPlaying.ID)
			}
			return DefaultStatusNone
		}()))

	// Step 2: Start playing current track again to clear the queue
	// This is a workaround since there's no direct "clear queue" API
	if currentQueue.CurrentlyPlaying.ID != "" {
		currentTrackURI := spotify.URI(fmt.Sprintf("spotify:track:%s", currentQueue.CurrentlyPlaying.ID))
		playOpts := &spotify.PlayOptions{
			URIs: []spotify.URI{currentTrackURI},
		}

		if err := c.client.PlayOpt(ctx, playOpts); err != nil {
			c.logger.Warn("Failed to restart current track for queue clearing", zap.Error(err))
			// Fallback to just adding priority track to existing queue
			return c.AddToQueue(ctx, priorityTrackID)
		}

		c.logger.Info("Restarted current track to clear queue")
		time.Sleep(QueueClearDelay) // Give API time to clear queue
	}

	// Step 3: Add priority track first
	if err := c.AddToQueue(ctx, priorityTrackID); err != nil {
		return fmt.Errorf("failed to add priority track to cleared queue: %w", err)
	}

	c.logger.Info("Added priority track to queue", zap.String("trackID", priorityTrackID))

	// Step 4: Re-add other tracks that were in the original queue
	for i, trackID := range queuedTrackIDs {
		// Skip the priority track if it was already in the queue
		if trackID == priorityTrackID {
			c.logger.Debug("Skipping priority track (already added)", zap.String("trackID", trackID))
			continue
		}

		if err := c.AddToQueue(ctx, trackID); err != nil {
			c.logger.Warn("Failed to re-add track to rebuilt queue",
				zap.String("trackID", trackID),
				zap.Int("position", i),
				zap.Error(err))
			// Continue with other tracks
		} else {
			c.logger.Debug("Re-added track to queue",
				zap.String("trackID", trackID),
				zap.Int("originalPosition", i))
		}

		// Small delay to avoid API rate limits
		if i < len(queuedTrackIDs)-1 {
			time.Sleep(TrackAddDelay)
		}
	}

	c.logger.Info("Queue rebuild completed",
		zap.String("priorityTrackID", priorityTrackID),
		zap.Int("readdedTracks", len(queuedTrackIDs)))

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
