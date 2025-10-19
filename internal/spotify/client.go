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
	// MinTracksForEndDetection is the minimum tracks needed for end-of-playlist detection
	MinTracksForEndDetection = 3
	// EndOfPlaylistThreshold is the number of tracks from the end to consider "near end"
	EndOfPlaylistThreshold = 3
	// RecommendationSeedTracks is the number of recent tracks to use as seeds for recommendations
	RecommendationSeedTracks = 5
	// SpotifyIDLength is the expected length of a Spotify track/artist/album ID
	SpotifyIDLength = 22
	// MaxSearchResults is the maximum number of search results to return
	MaxSearchResults = 10
	// SearchResultsMultiplier is used to get more search results for better filtering
	SearchResultsMultiplier = 2
	// MaxPlaylistTracks is a reasonable upper bound for playlist track count conversion
	MaxPlaylistTracks = 1000
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
	// UnknownArtist is the default value when artist name is not available
	UnknownArtist = "Unknown"
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

	// MaxVolume is the maximum volume level (0-100)
	MaxVolume = 100
)

var (
	spotifyTrackRegex = regexp.MustCompile(`(?:https?://)?(?:open\.)?spotify\.com/track/([a-zA-Z0-9]+)`)
	spotifyURIRegex   = regexp.MustCompile(`spotify:track:([a-zA-Z0-9]+)`)
)

type Client struct {
	config         *core.SpotifyConfig
	logger         *zap.Logger
	client         *spotify.Client
	normalizer     *fuzzy.Normalizer
	auth           *spotifyauth.Authenticator
	llm            core.LLMProvider // LLM provider for search query generation
	targetPlaylist string           // Playlist ID we're managing
}

type TokenData struct {
	Token *oauth2.Token `json:"token"`
}

func NewClient(config *core.SpotifyConfig, logger *zap.Logger, llm core.LLMProvider) *Client {
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
		config:     config,
		logger:     logger,
		normalizer: fuzzy.NewNormalizer(),
		auth:       auth,
		llm:        llm,
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

// SearchPlaylist searches for playlists based on a query string
func (c *Client) SearchPlaylist(ctx context.Context, query string) ([]core.Playlist, error) {
	if c.client == nil {
		return nil, fmt.Errorf("client not authenticated")
	}

	normalizedQuery := c.normalizer.NormalizeTitle(query)

	results, err := c.client.Search(ctx, normalizedQuery, spotify.SearchTypePlaylist)
	if err != nil {
		return nil, fmt.Errorf("playlist search failed: %w", err)
	}

	if results.Playlists == nil || len(results.Playlists.Playlists) == 0 {
		return nil, fmt.Errorf("no playlists found")
	}

	var playlists []core.Playlist
	for i := range results.Playlists.Playlists {
		if len(playlists) >= MaxSearchResults {
			break
		}

		playlist := &results.Playlists.Playlists[i]
		// Safe conversion with bounds checking
		var trackCount int
		if playlist.Tracks.Total > MaxPlaylistTracks {
			trackCount = MaxPlaylistTracks
		} else {
			trackCount = int(playlist.Tracks.Total) //nolint:gosec // Bounded by check above
		}

		corePlaylists := core.Playlist{
			ID:          string(playlist.ID),
			Name:        playlist.Name,
			Description: playlist.Description,
			TrackCount:  trackCount,
			Owner:       playlist.Owner.DisplayName,
		}
		playlists = append(playlists, corePlaylists)
	}

	return playlists, nil
}

// GetRandomTrackFromPlaylist gets a random track from the specified playlist, excluding duplicates
func (c *Client) GetRandomTrackFromPlaylist(ctx context.Context, playlistID string, excludeSet map[string]bool) (*core.Track, error) {
	if c.client == nil {
		return nil, fmt.Errorf("client not authenticated")
	}

	// Get all track IDs from the playlist
	trackIDs, err := c.GetPlaylistTracks(ctx, playlistID)
	if err != nil {
		return nil, fmt.Errorf("failed to get playlist tracks: %w", err)
	}

	if len(trackIDs) == 0 {
		return nil, fmt.Errorf("playlist is empty")
	}

	// Filter out excluded tracks
	var availableTrackIDs []string
	for _, trackID := range trackIDs {
		if !excludeSet[trackID] {
			availableTrackIDs = append(availableTrackIDs, trackID)
		}
	}

	if len(availableTrackIDs) == 0 {
		return nil, fmt.Errorf("no non-duplicate tracks available in playlist")
	}

	// Pick a random track from available tracks
	// Use a simple approach with time-based seeding
	randomIndex := int(time.Now().UnixNano()) % len(availableTrackIDs)
	selectedTrackID := availableTrackIDs[randomIndex]

	// Get track details
	track, err := c.GetTrack(ctx, selectedTrackID)
	if err != nil {
		return nil, fmt.Errorf("failed to get track details: %w", err)
	}

	return track, nil
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

	c.logger.Info("Track added to playlist with priority positioning",
		zap.String("trackID", trackID),
		zap.String("playlistID", playlistID),
		zap.Int("position", position))

	return nil
}

func (c *Client) RemoveFromPlaylist(ctx context.Context, playlistID, trackID string) error {
	if c.client == nil {
		return fmt.Errorf("client not authenticated")
	}

	spotifyTrackID := spotify.ID(trackID)
	spotifyPlaylistID := spotify.ID(playlistID)

	// Remove all instances of the track from the playlist
	_, err := c.client.RemoveTracksFromPlaylist(ctx, spotifyPlaylistID, spotifyTrackID)
	if err != nil {
		return fmt.Errorf("failed to remove track from playlist: %w", err)
	}

	c.logger.Info("Track removed from playlist",
		zap.String("trackID", trackID),
		zap.String("playlistID", playlistID))

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

// GetPlaylistPosition calculates the position of a track relative to the currently playing track in the playlist
// This is more reliable than GetQueuePosition for newly added tracks since playlist updates are immediate
func (c *Client) GetPlaylistPosition(ctx context.Context, trackID string) (int, error) {
	if c.client == nil {
		return -1, fmt.Errorf("client not authenticated")
	}

	if c.targetPlaylist == "" {
		return -1, fmt.Errorf("no target playlist set")
	}

	currentTrackID, err := c.GetCurrentTrackID(ctx)
	if err != nil {
		// No track playing, fallback to queue-based position
		return c.GetQueuePosition(ctx, trackID)
	}

	return c.calculatePlaylistPosition(ctx, currentTrackID, trackID)
}

// GetPlaylistPositionRelativeTo calculates the position of a track relative to a specific reference track in the playlist
func (c *Client) GetPlaylistPositionRelativeTo(ctx context.Context, trackID, referenceTrackID string) (int, error) {
	if c.client == nil {
		return -1, fmt.Errorf("client not authenticated")
	}

	if c.targetPlaylist == "" {
		return -1, fmt.Errorf("no target playlist set")
	}

	if referenceTrackID == "" {
		// No reference track, fallback to queue-based position
		return c.GetQueuePosition(ctx, trackID)
	}

	return c.calculatePlaylistPosition(ctx, referenceTrackID, trackID)
}

// GetCurrentTrackID gets the currently playing track ID, returns error if no track is playing
func (c *Client) GetCurrentTrackID(ctx context.Context) (string, error) {
	currently, err := c.client.PlayerCurrentlyPlaying(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to get currently playing: %w", err)
	}

	if currently == nil || currently.Item == nil || !currently.Playing {
		return "", fmt.Errorf("no track currently playing")
	}

	return string(currently.Item.ID), nil
}

// calculatePlaylistPosition calculates the relative position between two tracks in the playlist
func (c *Client) calculatePlaylistPosition(ctx context.Context, currentTrackID, newTrackID string) (int, error) {
	playlistTracks, err := c.GetPlaylistTracks(ctx, c.targetPlaylist)
	if err != nil {
		return -1, fmt.Errorf("failed to get playlist tracks: %w", err)
	}

	currentTrackPos, newTrackPos := c.findTrackPositions(playlistTracks, currentTrackID, newTrackID)

	if currentTrackPos == -1 {
		c.logger.Debug("Current track not found in playlist, falling back to queue position")
		return c.GetQueuePosition(ctx, newTrackID)
	}

	if newTrackPos == -1 {
		return -1, fmt.Errorf("track %s not found in playlist", newTrackID)
	}

	return c.computeRelativePosition(currentTrackPos, newTrackPos, newTrackID)
}

// findTrackPositions finds the positions of two tracks in the playlist
func (c *Client) findTrackPositions(playlistTracks []string, currentTrackID, newTrackID string) (currentPos, newPos int) {
	currentPos = -1
	newPos = -1

	for i, id := range playlistTracks {
		if id == currentTrackID {
			currentPos = i
		}
		if id == newTrackID {
			newPos = i
		}
		// Stop early if we found both tracks
		if currentPos >= 0 && newPos >= 0 {
			break
		}
	}

	return
}

// computeRelativePosition calculates the relative position between current and new track
func (c *Client) computeRelativePosition(currentTrackPos, newTrackPos int, newTrackID string) (int, error) {
	// If the new track is after the current track, subtract current position
	if newTrackPos > currentTrackPos {
		position := newTrackPos - currentTrackPos - 1 // -1 because current track is playing
		c.logger.Debug("Calculated playlist position",
			zap.String("trackID", newTrackID),
			zap.Int("currentTrackPos", currentTrackPos),
			zap.Int("newTrackPos", newTrackPos),
			zap.Int("calculatedPosition", position))
		return position, nil
	}

	// If the new track is before the current track, it will play after the playlist loops
	// This is more complex to calculate accurately, so fall back to queue position
	c.logger.Debug("New track is before current track in playlist, falling back to queue position")
	return c.GetQueuePosition(context.Background(), newTrackID)
}

// SetTargetPlaylist sets the playlist ID that we're managing
func (c *Client) SetTargetPlaylist(playlistID string) {
	c.targetPlaylist = playlistID
	c.logger.Info("Target playlist set", zap.String("playlistID", playlistID))
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

// IsNearPlaylistEnd checks if we're playing near the end of the playlist
// Returns true if currently playing within EndOfPlaylistThreshold tracks from the end
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

	// If playlist has fewer tracks than threshold, consider it always "near end"
	if len(playlistTracks) < MinTracksForEndDetection {
		return true, nil
	}

	// Find current track position in playlist
	for i, trackID := range playlistTracks {
		if trackID == currentTrackID {
			// Consider "near end" if we're within EndOfPlaylistThreshold tracks from the end
			return i >= len(playlistTracks)-EndOfPlaylistThreshold, nil
		}
	}

	return false, nil // Current track not found in playlist
}

// CheckPlaybackCompliance checks if current playback settings are optimal for auto-DJing
func (c *Client) CheckPlaybackCompliance(ctx context.Context) (*core.PlaybackCompliance, error) {
	if c.client == nil {
		return nil, fmt.Errorf("client not authenticated")
	}

	if c.targetPlaylist == "" {
		return nil, fmt.Errorf("no target playlist set")
	}

	// Get current playback state
	state, err := c.client.PlayerState(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get player state: %w", err)
	}

	compliance := &core.PlaybackCompliance{
		IsCorrectPlaylist: true,
		IsCorrectShuffle:  true,
		IsCorrectRepeat:   true,
		Issues:            []string{},
	}

	if state == nil || state.Item == nil {
		return compliance, nil // No playback active, consider this OK
	}

	// Check playlist compliance
	playlistOK := c.checkPlaylistCompliance(ctx, state, compliance)

	// Check settings compliance
	c.checkSettingsCompliance(state, compliance)

	// If playlist is wrong, don't bother checking track membership
	if !playlistOK {
		return compliance, nil
	}

	// Check if current track is actually in our playlist
	c.checkTrackMembership(ctx, state, compliance)

	return compliance, nil
}

// IsPlayingFromCorrectPlaylist checks if current playback is from the target playlist
// Kept for backward compatibility
func (c *Client) IsPlayingFromCorrectPlaylist(ctx context.Context) (bool, error) {
	compliance, err := c.CheckPlaybackCompliance(ctx)
	if err != nil {
		return false, err
	}
	return compliance.IsCorrectPlaylist, nil
}

// checkPlaylistCompliance verifies the current playback context
func (c *Client) checkPlaylistCompliance(_ context.Context, state *spotify.PlayerState, compliance *core.PlaybackCompliance) bool {
	playlistURI := fmt.Sprintf("spotify:playlist:%s", c.targetPlaylist)
	if state.PlaybackContext.URI != spotify.URI(playlistURI) {
		compliance.IsCorrectPlaylist = false
		compliance.Issues = append(compliance.Issues, "Not playing from target playlist")
		c.logger.Debug("Not playing from target playlist",
			zap.String("currentContext", string(state.PlaybackContext.URI)),
			zap.String("targetPlaylist", playlistURI))
		return false
	}
	return true
}

// checkSettingsCompliance verifies shuffle and repeat settings
func (c *Client) checkSettingsCompliance(state *spotify.PlayerState, compliance *core.PlaybackCompliance) {
	// Check shuffle setting (should be off for auto-DJing)
	if state.ShuffleState {
		compliance.IsCorrectShuffle = false
		compliance.Issues = append(compliance.Issues, "Shuffle is enabled (should be off for auto-DJing)")
		c.logger.Debug("Shuffle is enabled, which may interfere with auto-DJing")
	}

	// Check repeat setting (should be off or context, not track)
	if state.RepeatState == RepeatStateTrack {
		compliance.IsCorrectRepeat = false
		compliance.Issues = append(compliance.Issues, "Repeat is set to track (should be off or context for auto-DJing)")
		c.logger.Debug("Repeat is set to track, which will prevent auto-DJing")
	}
}

// checkTrackMembership verifies the current track is in the target playlist
func (c *Client) checkTrackMembership(ctx context.Context, state *spotify.PlayerState, compliance *core.PlaybackCompliance) {
	currentTrackID := string(state.Item.ID)
	playlistTracks, err := c.GetPlaylistTracks(ctx, c.targetPlaylist)
	if err != nil {
		c.logger.Warn("Failed to verify track is in playlist", zap.Error(err))
		return // Assume OK if we can't verify
	}

	// Check if current track exists in playlist
	for _, trackID := range playlistTracks {
		if trackID == currentTrackID {
			return // Found the track in playlist
		}
	}

	compliance.IsCorrectPlaylist = false
	compliance.Issues = append(compliance.Issues, "Current track not found in target playlist")
	c.logger.Debug("Current track not found in target playlist",
		zap.String("currentTrackID", currentTrackID),
		zap.String("targetPlaylist", c.targetPlaylist))
}

// GetAutoPlayPreventionTrack gets a track ID using LLM-enhanced search based on recent playlist tracks
func (c *Client) GetAutoPlayPreventionTrack(ctx context.Context) (string, error) {
	if c.client == nil {
		return "", fmt.Errorf("client not authenticated")
	}

	// Get current playlist tracks
	playlistTracks, err := c.GetPlaylistTracks(ctx, c.targetPlaylist)
	if err != nil {
		return "", fmt.Errorf("failed to get playlist tracks: %w", err)
	}

	if len(playlistTracks) == 0 {
		return "", fmt.Errorf("playlist is empty, cannot generate recommendations")
	}

	// Build seed tracks from recent playlist tracks
	seedTrackIDs := c.buildSeedTracks(playlistTracks)
	if len(seedTrackIDs) == 0 {
		c.logger.Warn("No valid seed tracks found, using generic search")
		return c.getLLMSearchFallbackTrack(ctx, nil, "no valid seed tracks")
	}

	c.logger.Info("Getting track recommendations using LLM-enhanced search",
		zap.Int("seedTrackCount", len(seedTrackIDs)),
		zap.Strings("seedTracks", func() []string {
			var tracks []string
			for _, id := range seedTrackIDs {
				tracks = append(tracks, string(id))
			}
			return tracks
		}()))

	// Validate track IDs
	validSeedTracks := c.validateSeedTracks(seedTrackIDs)
	if len(validSeedTracks) == 0 {
		c.logger.Warn("No valid seed tracks after validation, using generic search")
		return c.getLLMSearchFallbackTrack(ctx, nil, "no valid seed tracks after validation")
	}

	// Convert track IDs to Track objects for LLM processing
	seedTracks := c.getSeedTracksAsObjects(ctx, func() []string {
		tracks := make([]string, len(validSeedTracks))
		for i, id := range validSeedTracks {
			tracks[i] = string(id)
		}
		return tracks
	}())

	// Use LLM to generate search query and find track
	return c.getLLMSearchFallbackTrack(ctx, seedTracks, "primary LLM-enhanced search")
}

// buildSeedTracks extracts and validates seed tracks from playlist
func (c *Client) buildSeedTracks(playlistTracks []string) []spotify.ID {
	var seedTracks []spotify.ID
	numSeeds := RecommendationSeedTracks
	if len(playlistTracks) < numSeeds {
		numSeeds = len(playlistTracks)
	}

	// Take the last numSeeds tracks from the playlist and validate them
	for i := len(playlistTracks) - numSeeds; i < len(playlistTracks); i++ {
		trackID := playlistTracks[i]
		// Basic validation - Spotify track IDs should be 22 characters long
		if len(trackID) == SpotifyIDLength {
			seedTracks = append(seedTracks, spotify.ID(trackID))
		} else {
			c.logger.Warn("Skipping invalid track ID for recommendations seed",
				zap.String("trackID", trackID),
				zap.Int("length", len(trackID)))
		}
	}

	return seedTracks
}

// Deprecated: selectFirstNonDuplicateTrack was used with the deprecated recommendations API

// validateSeedTracks filters and validates Spotify track IDs for use as recommendation seeds
func (c *Client) validateSeedTracks(seedTracks []spotify.ID) []spotify.ID {
	validSeedTracks := make([]spotify.ID, 0, len(seedTracks))
	for i, trackID := range seedTracks {
		trackIDStr := string(trackID)

		// Check length
		if len(trackIDStr) != SpotifyIDLength {
			c.logger.Warn("Invalid seed track ID length, skipping",
				zap.Int("index", i),
				zap.String("trackID", trackIDStr),
				zap.Int("length", len(trackIDStr)),
				zap.Int("expected", SpotifyIDLength))
			continue
		}

		// Check for valid characters (alphanumeric)
		isValid := true
		for _, char := range trackIDStr {
			if !((char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9')) {
				isValid = false
				break
			}
		}

		if !isValid {
			c.logger.Warn("Invalid seed track ID characters, skipping",
				zap.Int("index", i),
				zap.String("trackID", trackIDStr))
			continue
		}

		validSeedTracks = append(validSeedTracks, trackID)
	}

	if len(validSeedTracks) < len(seedTracks) {
		c.logger.Warn("Some seed tracks were invalid and filtered out",
			zap.Int("originalCount", len(seedTracks)),
			zap.Int("validCount", len(validSeedTracks)))
	}

	return validSeedTracks
}

// getLLMSearchFallbackTrack gets a track using LLM-generated playlist search query when recommendations fail
func (c *Client) getLLMSearchFallbackTrack(ctx context.Context, seedTracks []core.Track, reason string) (string, error) {
	// Get current playlist tracks to check for duplicates
	playlistTracks, err := c.GetPlaylistTracks(ctx, c.targetPlaylist)
	if err != nil {
		c.logger.Warn("Failed to get playlist tracks for duplicate check, proceeding without duplicate filtering", zap.Error(err))
		playlistTracks = []string{} // Continue without duplicate checking
	}

	// Create set for fast duplicate lookup (includes both playlist tracks and seed tracks)
	excludeSet := make(map[string]bool)
	for _, trackID := range playlistTracks {
		excludeSet[trackID] = true
	}
	for _, track := range seedTracks {
		if track.ID != "" {
			excludeSet[track.ID] = true
		}
	}

	if c.llm == nil {
		// Fallback to generic playlist search if no LLM available
		playlists, searchErr := c.SearchPlaylist(ctx, "popular music playlist")
		if searchErr != nil {
			return "", fmt.Errorf("playlist search fallback failed (no LLM): %w", searchErr)
		}

		return c.selectRandomTrackFromPlaylistResults(ctx, playlists, excludeSet, reason, "generic playlist search")
	}

	// Generate playlist search query using LLM based on seed tracks
	searchQuery, err := c.llm.GenerateSearchQuery(ctx, seedTracks)
	if err != nil {
		c.logger.Warn("Failed to generate LLM search query, using generic fallback",
			zap.Error(err))
		searchQuery = "popular music playlist"
	} else if !strings.Contains(strings.ToLower(searchQuery), "playlist") {
		// Ensure the query is for playlists
		searchQuery += " playlist"
	}

	c.logger.Info("Generated LLM playlist search query for auto-play prevention",
		zap.String("reason", reason),
		zap.String("searchQuery", searchQuery),
		zap.Int("seedTrackCount", len(seedTracks)))

	playlists, err := c.SearchPlaylist(ctx, searchQuery)
	if err != nil {
		return "", fmt.Errorf("LLM playlist search failed: %w", err)
	}

	return c.selectRandomTrackFromPlaylistResults(ctx, playlists, excludeSet, reason, searchQuery)
}

// selectRandomTrackFromPlaylistResults selects the best matching playlist and picks a random track from it
func (c *Client) selectRandomTrackFromPlaylistResults(
	ctx context.Context,
	playlists []core.Playlist,
	excludeSet map[string]bool,
	reason, searchQuery string,
) (string, error) {
	if len(playlists) == 0 {
		return "", fmt.Errorf("no playlists found in search results")
	}

	// Try each playlist in order (first is best match) until we find a non-duplicate track
	for i, playlist := range playlists {
		c.logger.Debug("Attempting to get random track from playlist",
			zap.String("playlistID", playlist.ID),
			zap.String("playlistName", playlist.Name),
			zap.String("owner", playlist.Owner),
			zap.Int("trackCount", playlist.TrackCount),
			zap.Int("position", i+1))

		// Skip playlists that are too small
		if playlist.TrackCount < 1 {
			c.logger.Debug("Skipping empty playlist",
				zap.String("playlistID", playlist.ID),
				zap.String("playlistName", playlist.Name))
			continue
		}

		track, err := c.GetRandomTrackFromPlaylist(ctx, playlist.ID, excludeSet)
		if err != nil {
			c.logger.Debug("Failed to get random track from playlist, trying next",
				zap.String("playlistID", playlist.ID),
				zap.String("playlistName", playlist.Name),
				zap.Error(err))
			continue
		}

		c.logger.Info("Selected random track from playlist for auto-play prevention",
			zap.String("reason", reason),
			zap.String("searchQuery", searchQuery),
			zap.String("selectedPlaylistID", playlist.ID),
			zap.String("selectedPlaylistName", playlist.Name),
			zap.String("playlistOwner", playlist.Owner),
			zap.Int("playlistTrackCount", playlist.TrackCount),
			zap.String("selectedTrackID", track.ID),
			zap.String("title", track.Title),
			zap.String("artist", track.Artist),
			zap.Int("playlistPosition", i+1),
			zap.Int("totalPlaylists", len(playlists)))

		return track.ID, nil
	}

	return "", fmt.Errorf("no suitable tracks found in any of the %d playlists", len(playlists))
}

// Deprecated: selectFirstNonDuplicateFromSearch was used with direct track search approach

// getSeedTracksAsObjects converts track IDs to Track objects for LLM processing
func (c *Client) getSeedTracksAsObjects(ctx context.Context, trackIDs []string) []core.Track {
	var tracks []core.Track
	for _, trackID := range trackIDs {
		track, err := c.GetTrack(ctx, trackID)
		if err != nil {
			c.logger.Warn("Failed to get track details for seed",
				zap.String("trackID", trackID),
				zap.Error(err))
			continue
		}
		tracks = append(tracks, *track)
	}
	return tracks
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

// GetCurrentVolume gets the current playback volume (0-100)
func (c *Client) GetCurrentVolume(ctx context.Context) (int, error) {
	if c.client == nil {
		return 0, fmt.Errorf("spotify client not initialized")
	}

	playerState, err := c.client.PlayerState(ctx)
	if err != nil {
		return 0, fmt.Errorf("failed to get player state: %w", err)
	}

	return playerState.Device.Volume, nil
}

// SetVolume sets the playback volume (0-100)
func (c *Client) SetVolume(ctx context.Context, volume int) error {
	if c.client == nil {
		return fmt.Errorf("spotify client not initialized")
	}

	if volume < 0 {
		volume = 0
	}
	if volume > MaxVolume {
		volume = MaxVolume
	}

	err := c.client.Volume(ctx, volume)
	if err != nil {
		return fmt.Errorf("failed to set volume to %d: %w", volume, err)
	}

	c.logger.Debug("Set Spotify volume",
		zap.Int("volume", volume))

	return nil
}

// PlayTrack starts playing a specific track
func (c *Client) PlayTrack(ctx context.Context, trackID string) error {
	if c.client == nil {
		return fmt.Errorf("spotify client not initialized")
	}

	trackURI := spotify.URI("spotify:track:" + trackID)
	playOptions := &spotify.PlayOptions{
		URIs: []spotify.URI{trackURI},
	}

	err := c.client.PlayOpt(ctx, playOptions)
	if err != nil {
		return fmt.Errorf("failed to play track %s: %w", trackID, err)
	}

	c.logger.Info("Started playing track",
		zap.String("trackID", trackID))

	return nil
}

// SetPlaylistContext sets the playback context to a specific playlist and starts playing from a specific track
func (c *Client) SetPlaylistContext(ctx context.Context, playlistID, trackID string) error {
	if c.client == nil {
		return fmt.Errorf("spotify client not initialized")
	}

	playlistURI := spotify.URI("spotify:playlist:" + playlistID)
	trackURI := spotify.URI("spotify:track:" + trackID)

	playOptions := &spotify.PlayOptions{
		PlaybackContext: &playlistURI,
		PlaybackOffset: &spotify.PlaybackOffset{
			URI: trackURI,
		},
	}

	err := c.client.PlayOpt(ctx, playOptions)
	if err != nil {
		return fmt.Errorf("failed to set playlist context %s with track %s: %w", playlistID, trackID, err)
	}

	c.logger.Info("Set playlist context and started playback",
		zap.String("playlistID", playlistID),
		zap.String("trackID", trackID))

	return nil
}
