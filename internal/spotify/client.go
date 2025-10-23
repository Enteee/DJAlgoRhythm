// Package spotify provides Spotify Web API integration for playlist management and track search.
package spotify

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
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
	"whatdj/pkg/text"
)

// Package-level random number generator for consistent random operations
var rng = rand.New(rand.NewSource(time.Now().UnixNano())) //nolint:gosec // Music selection doesn't require crypto-secure randomness

const (
	// MinValidYear represents the minimum reasonable year for music tracks
	MinValidYear = 1950
	// FilePermission is the permission for token files
	FilePermission = 0600
	// RecommendationSeedTracks is the number of recent tracks to use as seeds for recommendations
	RecommendationSeedTracks = 5
	// SpotifyIDLength is the expected length of a Spotify track/artist/album ID
	SpotifyIDLength = 22
	// MaxTrackSearchResults limits track search results for user queries and disambiguation
	MaxTrackSearchResults = 10
	// MaxPlaylistSearchResults limits playlist search results for automated recommendations
	MaxPlaylistSearchResults = 10
	// DefaultPlaylistSearchQuery is the fallback search query when LLM is unavailable
	DefaultPlaylistSearchQuery = "popular music playlist"
	// MaxCandidateTracksPerPlaylist is the number of tracks to sample from each playlist
	MaxCandidateTracksPerPlaylist = 2
	// MaxTotalCandidates is the maximum total number of candidate tracks to collect
	MaxTotalCandidates = 12
	// MaxPlaylistsForCandidates limits playlists used for candidate track collection
	MaxPlaylistsForCandidates = 3
	// ReleaseDateYearLength is the expected length of a release date year string
	ReleaseDateYearLength = 4
	// UnknownArtist is the default value when artist name is not available
	UnknownArtist = "Unknown"

	// RepeatStateTrack represents the "track" repeat state
	RepeatStateTrack = "track"
	// RepeatStateOff represents the "off" repeat state
	RepeatStateOff = "off"
	// RepeatStateContext represents the "context" repeat state
	RepeatStateContext = "context"
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
		if len(tracks) >= MaxTrackSearchResults {
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
		if len(playlists) >= MaxPlaylistSearchResults {
			break
		}

		playlist := &results.Playlists.Playlists[i]
		// Safe conversion from Spotify API uint to int
		trackCount := int(playlist.Tracks.Total) //nolint:gosec // Spotify playlist counts are reasonable for int conversion

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

// GetQueueTrackIDs returns all track IDs currently in the Spotify queue
func (c *Client) GetQueueTrackIDs(ctx context.Context) ([]string, error) {
	if c.client == nil {
		return nil, fmt.Errorf("client not authenticated")
	}

	queue, err := c.client.GetQueue(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get user queue: %w", err)
	}

	// Extract track IDs from queue
	trackIDs := make([]string, 0, len(queue.Items))
	for i := range queue.Items {
		if queue.Items[i].ID != "" {
			trackIDs = append(trackIDs, string(queue.Items[i].ID))
		}
	}

	return trackIDs, nil
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

// SetTargetPlaylist sets the playlist ID that we're managing
func (c *Client) SetTargetPlaylist(playlistID string) {
	c.targetPlaylist = playlistID
	c.logger.Info("Target playlist set", zap.String("playlistID", playlistID))
}

// CheckPlaybackCompliance checks if current playback settings are optimal for auto-DJing
func (c *Client) CheckPlaybackCompliance(ctx context.Context) (*core.PlaybackCompliance, error) {
	if c.client == nil {
		return nil, fmt.Errorf("client not authenticated")
	}

	// Get current playback state
	state, err := c.client.PlayerState(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get player state: %w", err)
	}

	compliance := &core.PlaybackCompliance{
		IsCorrectShuffle: true,
		IsCorrectRepeat:  true,
		Issues:           []string{},
	}

	if state == nil || state.Item == nil {
		return compliance, nil // No playback active, consider this OK
	}

	// Check only settings compliance (shuffle/repeat)
	c.checkSettingsCompliance(state, compliance)

	return compliance, nil
}

// checkSettingsCompliance verifies shuffle and repeat settings
func (c *Client) checkSettingsCompliance(state *spotify.PlayerState, compliance *core.PlaybackCompliance) {
	// Check shuffle setting (should be off for auto-DJing)
	if state.ShuffleState {
		compliance.IsCorrectShuffle = false
		compliance.Issues = append(compliance.Issues, "Shuffle is enabled (should be off for auto-DJing)")
		c.logger.Debug("Shuffle is enabled, which may interfere with auto-DJing")
	}

	// Check repeat setting (should be off for auto-DJing)
	if state.RepeatState != RepeatStateOff {
		compliance.IsCorrectRepeat = false
		compliance.Issues = append(compliance.Issues, "Repeat is not set to off (should be off for auto-DJing)")
		c.logger.Debug("Repeat is not set to off, which may interfere with auto-DJing",
			zap.String("currentRepeatState", state.RepeatState))
	}
}

// GetRecommendedTrack gets a track ID using LLM-enhanced search based on recent playlist tracks
func (c *Client) GetRecommendedTrack(ctx context.Context) (trackID, searchQuery, newTrackMood string, err error) {
	if c.client == nil {
		return "", "", "", fmt.Errorf("client not authenticated")
	}

	// Get playlist tracks and build exclusion set in single pass
	playlistTracks, exclusionSet, err := c.getPlaylistTracksWithExclusions(ctx)
	if err != nil {
		return "", "", "", fmt.Errorf("failed to get playlist tracks: %w", err)
	}

	if len(playlistTracks) == 0 {
		return "", "", "", fmt.Errorf("playlist is empty, cannot generate recommendations")
	}

	// Get recent tracks for search context (simple approach)
	recentTracks := c.getRecentTracksForSearch(ctx, playlistTracks, RecommendationSeedTracks)

	// Generate search query with LLM or fallback
	searchQuery = c.generateSearchQuery(ctx, recentTracks)

	// Find and return track along with search query
	trackID, err = c.findTrackFromSearch(ctx, searchQuery, exclusionSet)
	if err != nil {
		return "", "", "", err
	}

	// Get the new track details and generate its mood
	newTrack, err := c.GetTrack(ctx, trackID)
	if err != nil {
		c.logger.Warn("Failed to get new track details for mood generation",
			zap.String("trackID", trackID), zap.Error(err))
		newTrackMood = "unknown"
	} else {
		// Generate mood for the new track using just this track
		newTrackMood = c.generateSearchQuery(ctx, []core.Track{*newTrack})
	}

	return trackID, searchQuery, newTrackMood, nil
}

// getPlaylistTracksWithExclusions gets playlist tracks and builds exclusion set in single operation
func (c *Client) getPlaylistTracksWithExclusions(ctx context.Context) (tracks []string, exclusions map[string]bool, err error) {
	playlistTracks, err := c.GetPlaylistTracks(ctx, c.targetPlaylist)
	if err != nil {
		return nil, nil, err
	}

	// Build exclusion set during the same iteration
	exclusionSet := make(map[string]bool)
	for _, trackID := range playlistTracks {
		if trackID != "" {
			exclusionSet[trackID] = true
		}
	}

	return playlistTracks, exclusionSet, nil
}

// getRecentTracksForSearch extracts recent tracks for LLM context (simplified)
func (c *Client) getRecentTracksForSearch(ctx context.Context, playlistTracks []string, count int) []core.Track {
	if len(playlistTracks) == 0 {
		return nil
	}

	// Take the last 'count' tracks from playlist (most recent)
	start := len(playlistTracks) - count
	if start < 0 {
		start = 0
	}

	var recentTracks []core.Track
	for i := start; i < len(playlistTracks); i++ {
		trackID := playlistTracks[i]

		// Simple validation - just check length
		if len(trackID) == SpotifyIDLength {
			track, err := c.GetTrack(ctx, trackID)
			if err != nil {
				c.logger.Warn("Failed to get recent track for search context",
					zap.String("trackID", trackID),
					zap.Error(err))
				continue
			}
			recentTracks = append(recentTracks, *track)
		}
	}

	return recentTracks
}

// generateSearchQuery creates search query using LLM or fallback
func (c *Client) generateSearchQuery(ctx context.Context, recentTracks []core.Track) string {
	if c.llm == nil || len(recentTracks) == 0 {
		c.logger.Debug("Using fallback search query (no LLM or no recent tracks)")
		return DefaultPlaylistSearchQuery
	}

	searchQuery, err := c.llm.GenerateSearchQuery(ctx, recentTracks)
	if err != nil {
		c.logger.Warn("Failed to generate LLM search query, using fallback",
			zap.Error(err))
		return DefaultPlaylistSearchQuery
	}

	c.logger.Info("Generated search query for queue management",
		zap.String("searchQuery", searchQuery),
		zap.Int("seedTrackCount", len(recentTracks)))

	return searchQuery
}

// collectCandidateTracksFromPlaylists gathers random tracks from multiple playlists using batch optimization
func (c *Client) collectCandidateTracksFromPlaylists(
	ctx context.Context,
	playlists []core.Playlist,
	exclusionSet map[string]bool,
	maxCandidates int,
) ([]core.Track, error) {
	// Batch fetch all tracks from all playlists
	playlistTracks := c.getAllTracksFromPlaylists(ctx, playlists)
	if len(playlistTracks) == 0 {
		return nil, fmt.Errorf("no tracks available from any of the %d playlists", len(playlists))
	}

	// Collect all non-excluded track IDs
	var availableTrackIDs []string
	for _, trackIDs := range playlistTracks {
		for _, trackID := range trackIDs {
			if !exclusionSet[trackID] {
				availableTrackIDs = append(availableTrackIDs, trackID)
			}
		}
	}

	if len(availableTrackIDs) == 0 {
		return nil, fmt.Errorf("no non-excluded tracks available from any playlist")
	}

	// Randomly select track IDs
	numToSelect := maxCandidates
	if numToSelect > len(availableTrackIDs) {
		numToSelect = len(availableTrackIDs)
	}

	// Randomly select track IDs using Go's built-in rand
	selectedIndices := rng.Perm(len(availableTrackIDs))[:numToSelect]
	var selectedTrackIDs []string
	for _, idx := range selectedIndices {
		selectedTrackIDs = append(selectedTrackIDs, availableTrackIDs[idx])
	}

	// Fetch track details for selected tracks
	var candidates []core.Track
	for _, trackID := range selectedTrackIDs {
		track, err := c.GetTrack(ctx, trackID)
		if err != nil {
			c.logger.Debug("Failed to get track details, skipping", zap.String("trackID", trackID), zap.Error(err))
			continue
		}
		candidates = append(candidates, *track)
	}

	if len(candidates) == 0 {
		return nil, fmt.Errorf("no candidate tracks collected from %d playlists", len(playlists))
	}

	c.logger.Info("Candidate track collection completed",
		zap.Int("totalCandidates", len(candidates)),
		zap.Int("fromPlaylists", len(playlistTracks)))

	return candidates, nil
}

// selectRandomPlaylists randomly selects up to maxCount playlists from the given slice
func (c *Client) selectRandomPlaylists(playlists []core.Playlist, maxCount int) []core.Playlist {
	if len(playlists) <= maxCount {
		return playlists // Return all if we have fewer than maxCount
	}

	// Create a copy and shuffle to get random selection
	playlistsCopy := make([]core.Playlist, len(playlists))
	copy(playlistsCopy, playlists)

	// Shuffle the copy and take first maxCount elements
	rng.Shuffle(len(playlistsCopy), func(i, j int) {
		playlistsCopy[i], playlistsCopy[j] = playlistsCopy[j], playlistsCopy[i]
	})

	return playlistsCopy[:maxCount]
}

// findTrackFromSearch searches for playlists and uses AI to select the best matching track
func (c *Client) findTrackFromSearch(ctx context.Context, searchQuery string, exclusionSet map[string]bool) (string, error) {
	// Search for playlists
	playlists, err := c.SearchPlaylist(ctx, searchQuery)
	if err != nil {
		return "", fmt.Errorf("playlist search failed: %w", err)
	}

	if len(playlists) == 0 {
		return "", fmt.Errorf("no playlists found for query: %s", searchQuery)
	}

	// Randomly select up to MaxPlaylistsForCandidates playlists for variety and performance
	selectedPlaylists := c.selectRandomPlaylists(playlists, MaxPlaylistsForCandidates)

	// Collect candidate tracks from selected playlists
	candidates, err := c.collectCandidateTracksFromPlaylists(ctx, selectedPlaylists, exclusionSet, MaxTotalCandidates)
	if err != nil {
		return "", fmt.Errorf("failed to collect candidate tracks: %w", err)
	}

	if len(candidates) == 0 {
		return "", fmt.Errorf("no candidate tracks found in any of the %d playlists", len(playlists))
	}

	// Use AI to rank candidates based on search query relevance
	rankedTracks := c.llm.RankTracks(ctx, searchQuery, candidates)

	if len(rankedTracks) == 0 {
		return "", fmt.Errorf("no ranked tracks available")
	}

	// Select the top-ranked track
	selectedTrack := rankedTracks[0]

	c.logger.Info("Selected AI-ranked track for queue management",
		zap.String("searchQuery", searchQuery),
		zap.String("selectedTrackID", selectedTrack.ID),
		zap.String("title", selectedTrack.Title),
		zap.String("artist", selectedTrack.Artist),
		zap.Int("totalCandidates", len(candidates)),
		zap.Int("rankedCandidates", len(rankedTracks)))

	return selectedTrack.ID, nil
}

// getAllTracksFromPlaylists fetches all tracks from multiple playlists in batch
func (c *Client) getAllTracksFromPlaylists(ctx context.Context, playlists []core.Playlist) map[string][]string {
	playlistTracks := make(map[string][]string)

	c.logger.Debug("Batch fetching tracks from all playlists",
		zap.Int("playlistCount", len(playlists)))

	for _, playlist := range playlists {
		if playlist.TrackCount < 1 {
			c.logger.Debug("Skipping empty playlist",
				zap.String("playlistID", playlist.ID),
				zap.String("playlistName", playlist.Name))
			continue
		}

		trackIDs, err := c.GetPlaylistTracks(ctx, playlist.ID)
		if err != nil {
			c.logger.Warn("Failed to fetch tracks from playlist",
				zap.String("playlistID", playlist.ID),
				zap.String("playlistName", playlist.Name),
				zap.Error(err))
			continue // Skip this playlist but continue with others
		}

		playlistTracks[playlist.ID] = trackIDs
		c.logger.Debug("Fetched tracks from playlist",
			zap.String("playlistID", playlist.ID),
			zap.String("playlistName", playlist.Name),
			zap.Int("trackCount", len(trackIDs)))
	}

	c.logger.Info("Batch playlist fetch completed",
		zap.Int("playlistsRequested", len(playlists)),
		zap.Int("playlistsSuccess", len(playlistTracks)))

	return playlistTracks
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
	ctx, cancel := context.WithTimeout(context.Background(), text.URLResolveTimeout)
	defer cancel()

	client := &http.Client{
		Timeout: text.URLResolveTimeout,
		CheckRedirect: func(_ *http.Request, via []*http.Request) error {
			if len(via) >= text.MaxRedirects {
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
	if hostname == text.SpotifyAppLinkDomain {
		return c.resolveWithPageContent(shortURL)
	}

	return "", fmt.Errorf("URL did not resolve to a Spotify track")
}

// resolveWithPageContent fetches page content to extract Spotify URL
func (c *Client) resolveWithPageContent(shortURL string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), text.URLResolveTimeout)
	defer cancel()

	client := &http.Client{Timeout: text.URLResolveTimeout}

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
	buf := make([]byte, text.ReadBufferSize)
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
	if hostname == "spotify.link" || hostname == text.SpotifyAppLinkDomain {
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

// SetShuffle sets the shuffle state for the user's playback
func (c *Client) SetShuffle(ctx context.Context, shuffle bool) error {
	if c.client == nil {
		return fmt.Errorf("spotify client not initialized")
	}

	err := c.client.Shuffle(ctx, shuffle)
	if err != nil {
		return fmt.Errorf("failed to set shuffle to %t: %w", shuffle, err)
	}

	c.logger.Debug("Set Spotify shuffle",
		zap.Bool("shuffle", shuffle))

	return nil
}

// SetRepeat sets the repeat state for the user's playback
// state should be "track", "context", or "off"
func (c *Client) SetRepeat(ctx context.Context, state string) error {
	if c.client == nil {
		return fmt.Errorf("spotify client not initialized")
	}

	// Validate repeat state
	switch state {
	case RepeatStateTrack, RepeatStateContext, RepeatStateOff:
		// Valid states
	default:
		return fmt.Errorf("invalid repeat state: %s (must be 'track', 'context', or 'off')", state)
	}

	err := c.client.Repeat(ctx, state)
	if err != nil {
		return fmt.Errorf("failed to set repeat to %s: %w", state, err)
	}

	c.logger.Debug("Set Spotify repeat",
		zap.String("state", state))

	return nil
}

// GetNextPlaylistTracks gets the next N tracks from the playlist after the current position
func (c *Client) GetNextPlaylistTracks(ctx context.Context, count int) ([]core.Track, error) {
	if c.client == nil {
		return nil, fmt.Errorf("client not authenticated")
	}

	if c.targetPlaylist == "" {
		return nil, fmt.Errorf("no target playlist set")
	}

	// Get all playlist tracks
	playlistTrackIDs, err := c.GetPlaylistTracks(ctx, c.targetPlaylist)
	if err != nil {
		return nil, fmt.Errorf("failed to get playlist tracks: %w", err)
	}

	// Determine starting position based on current track
	startPos := c.determineStartPosition(ctx, playlistTrackIDs)

	// Get track IDs to fetch
	trackIDsToFetch := c.selectTrackIDsFromPosition(playlistTrackIDs, startPos, count)

	// Convert track IDs to Track objects
	return c.convertTrackIDsToTracks(ctx, trackIDsToFetch)
}

// GetNextPlaylistTracksFromPosition gets the next N tracks starting from a specific position
func (c *Client) GetNextPlaylistTracksFromPosition(ctx context.Context, startPosition, count int) ([]core.Track, error) {
	if c.client == nil {
		return nil, fmt.Errorf("client not authenticated")
	}

	if c.targetPlaylist == "" {
		return nil, fmt.Errorf("no target playlist set")
	}

	// Get all playlist tracks
	playlistTrackIDs, err := c.GetPlaylistTracks(ctx, c.targetPlaylist)
	if err != nil {
		return nil, fmt.Errorf("failed to get playlist tracks: %w", err)
	}

	// Use provided start position + 1 for next tracks
	startPos := startPosition + 1

	c.logger.Debug("Getting playlist tracks from specific position",
		zap.Int("requestedStartPosition", startPosition),
		zap.Int("actualStartPosition", startPos),
		zap.Int("count", count),
		zap.Int("totalPlaylistTracks", len(playlistTrackIDs)))

	// Get track IDs to fetch
	trackIDsToFetch := c.selectTrackIDsFromPosition(playlistTrackIDs, startPos, count)

	// Convert track IDs to Track objects
	return c.convertTrackIDsToTracks(ctx, trackIDsToFetch)
}

// determineStartPosition finds the position to start fetching tracks from
func (c *Client) determineStartPosition(ctx context.Context, playlistTrackIDs []string) int {
	currentTrackID, err := c.GetCurrentTrackID(ctx)
	if err != nil {
		c.logger.Debug("No current track playing, starting from beginning of playlist")
		return 0
	}

	// Find current track position
	for i, trackID := range playlistTrackIDs {
		if trackID == currentTrackID {
			return i + 1 // Start from next track
		}
	}

	c.logger.Debug("Current track not found in playlist, starting from beginning")
	return 0
}

// selectTrackIDsFromPosition selects track IDs starting from the given position
func (c *Client) selectTrackIDsFromPosition(playlistTrackIDs []string, startPos, count int) []string {
	if startPos >= len(playlistTrackIDs) {
		return []string{}
	}

	endPos := startPos + count
	if endPos > len(playlistTrackIDs) {
		endPos = len(playlistTrackIDs)
	}

	return playlistTrackIDs[startPos:endPos]
}

// convertTrackIDsToTracks converts track IDs to Track objects
func (c *Client) convertTrackIDsToTracks(ctx context.Context, trackIDs []string) ([]core.Track, error) {
	var tracks []core.Track

	for _, trackID := range trackIDs {
		track, err := c.GetTrack(ctx, trackID)
		if err != nil {
			c.logger.Warn("Failed to get track details",
				zap.String("trackID", trackID),
				zap.Error(err))
			continue
		}
		tracks = append(tracks, *track)
	}

	c.logger.Debug("Converted track IDs to Track objects",
		zap.Int("requestedCount", len(trackIDs)),
		zap.Int("convertedCount", len(tracks)))

	return tracks, nil
}

// GetCurrentTrackRemainingTime gets the remaining duration of the currently playing track
func (c *Client) GetCurrentTrackRemainingTime(ctx context.Context) (time.Duration, error) {
	if c.client == nil {
		return 0, fmt.Errorf("spotify client not initialized")
	}

	state, err := c.client.PlayerState(ctx)
	if err != nil {
		return 0, fmt.Errorf("failed to get player state: %w", err)
	}

	if state == nil || state.Item == nil || !state.Playing {
		return 0, nil // No track playing or paused
	}

	// Calculate remaining time in current track
	remainingMs := state.Item.Duration - state.Progress
	if remainingMs < 0 {
		remainingMs = 0 // Prevent negative duration
	}

	remaining := time.Duration(remainingMs) * time.Millisecond

	c.logger.Debug("Current track remaining time",
		zap.Duration("remaining", remaining),
		zap.Int("progressMs", state.Progress),
		zap.Int("durationMs", state.Item.Duration))

	return remaining, nil
}

// HasActiveDevice checks if there are any active Spotify devices available for playback
func (c *Client) HasActiveDevice(ctx context.Context) (bool, error) {
	if c.client == nil {
		return false, fmt.Errorf("spotify client not initialized")
	}

	devices, err := c.client.PlayerDevices(ctx)
	if err != nil {
		return false, fmt.Errorf("failed to get player devices: %w", err)
	}

	// Check if any device is active
	for _, device := range devices {
		if device.Active {
			c.logger.Debug("Found active device",
				zap.String("deviceName", device.Name),
				zap.String("deviceType", device.Type),
				zap.String("deviceID", device.ID.String()))
			return true, nil
		}
	}

	c.logger.Debug("No active devices found",
		zap.Int("totalDevices", len(devices)))
	return false, nil
}
