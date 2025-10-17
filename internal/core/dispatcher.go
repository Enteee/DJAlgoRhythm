package core

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"

	"whatdj/internal/chat"
	"whatdj/internal/i18n"
)

const (
	thumbsUpReaction   = chat.ReactionThumbsUp
	thumbsDownReaction = chat.ReactionThumbsDown
)

// Dispatcher handles messages from any chat frontend using the unified interface
type Dispatcher struct {
	config    *Config
	frontend  chat.Frontend
	spotify   SpotifyClient
	llm       LLMProvider
	dedup     DedupStore
	logger    *zap.Logger
	localizer *i18n.Localizer

	messageContexts map[string]*MessageContext
	contextMutex    sync.RWMutex
}

// NewDispatcher creates a new dispatcher with the provided chat frontend
func NewDispatcher(
	config *Config,
	frontend chat.Frontend,
	spotify SpotifyClient,
	llm LLMProvider,
	dedup DedupStore,
	logger *zap.Logger,
) *Dispatcher {
	return &Dispatcher{
		config:          config,
		frontend:        frontend,
		spotify:         spotify,
		llm:             llm,
		dedup:           dedup,
		logger:          logger,
		localizer:       i18n.NewLocalizer(config.App.Language),
		messageContexts: make(map[string]*MessageContext),
	}
}

// Start initializes the dispatcher and begins processing messages
func (d *Dispatcher) Start(ctx context.Context) error {
	d.logger.Info("Starting message dispatcher")

	// Load existing playlist tracks into dedup store
	if err := d.loadPlaylistSnapshot(ctx); err != nil {
		d.logger.Warn("Failed to load playlist snapshot", zap.Error(err))
	}

	// Start the chat frontend
	if err := d.frontend.Start(ctx); err != nil {
		return fmt.Errorf("failed to start chat frontend: %w", err)
	}

	// Send startup message to the group
	d.sendStartupMessage(ctx)

	// Begin listening for messages
	return d.frontend.Listen(ctx, d.handleMessage)
}

// Stop gracefully shuts down the dispatcher
func (d *Dispatcher) Stop(ctx context.Context) error {
	d.logger.Info("Stopping message dispatcher")

	// Send shutdown message to the group
	d.sendShutdownMessage(ctx)

	return nil
}

// handleMessage processes incoming chat messages
func (d *Dispatcher) handleMessage(msg *chat.Message) {
	ctx := context.Background()

	d.logger.Debug("Received message",
		zap.String("messageID", msg.ID),
		zap.String("sender", msg.SenderName),
		zap.String("text", msg.Text),
	)

	// Convert chat message to internal format
	inputMsg := d.convertToInputMessage(msg)

	msgCtx := &MessageContext{
		Input:     inputMsg,
		State:     StateDispatch,
		StartTime: time.Now(),
		TimeoutAt: time.Now().Add(time.Duration(d.config.App.ConfirmTimeoutSecs) * time.Second),
	}

	d.contextMutex.Lock()
	d.messageContexts[msg.ID] = msgCtx
	d.contextMutex.Unlock()

	go d.processMessage(ctx, msgCtx, msg)
}

// processMessage handles the main message processing logic
func (d *Dispatcher) processMessage(ctx context.Context, msgCtx *MessageContext, originalMsg *chat.Message) {
	defer d.cleanupContext(msgCtx.Input.MessageID)

	d.logger.Debug("Processing message",
		zap.String("messageID", msgCtx.Input.MessageID),
		zap.String("sender", msgCtx.Input.SenderJID),
		zap.String("text", msgCtx.Input.Text),
	)

	// Add "eyes" reaction to show the message is being processed
	d.reactProcessing(ctx, originalMsg)

	switch msgCtx.Input.Type {
	case MessageTypeSpotifyLink:
		d.handleSpotifyLink(ctx, msgCtx, originalMsg)
	case MessageTypeNonSpotifyLink:
		d.askWhichSong(ctx, msgCtx, originalMsg)
	case MessageTypeFreeText:
		// Filter out obvious chatter
		if d.isNotMusicRequest(ctx, msgCtx.Input.Text) {
			d.logger.Debug("Message filtered out as chatter",
				zap.String("text", msgCtx.Input.Text))
			d.reactIgnored(ctx, originalMsg)
			return
		}
		d.llmDisambiguate(ctx, msgCtx, originalMsg)
	}
}

// handleSpotifyLink processes Spotify links
func (d *Dispatcher) handleSpotifyLink(ctx context.Context, msgCtx *MessageContext, originalMsg *chat.Message) {
	msgCtx.State = StateHandleSpotifyLink

	var trackID string
	var err error

	for _, url := range msgCtx.Input.URLs {
		if trackID, err = d.spotify.ExtractTrackID(url); err == nil && trackID != "" {
			break
		}
	}

	if trackID == "" {
		d.replyError(ctx, msgCtx, originalMsg, d.localizer.T("error.spotify.extract_track_id"))
		return
	}

	if d.dedup.Has(trackID) {
		d.reactDuplicate(ctx, msgCtx, originalMsg)
		return
	}

	d.addToPlaylist(ctx, msgCtx, originalMsg, trackID)
}

// askWhichSong asks for clarification on non-Spotify links
func (d *Dispatcher) askWhichSong(ctx context.Context, msgCtx *MessageContext, originalMsg *chat.Message) {
	msgCtx.State = StateAskWhichSong

	_, err := d.frontend.SendText(ctx, originalMsg.ChatID, originalMsg.ID, d.localizer.T("prompt.which_song"))
	if err != nil {
		d.logger.Error("Failed to ask which song", zap.Error(err))
	}
}

// llmDisambiguate uses enhanced three-stage LLM disambiguation with Spotify search
func (d *Dispatcher) llmDisambiguate(ctx context.Context, msgCtx *MessageContext, originalMsg *chat.Message) {
	msgCtx.State = StateLLMDisambiguate

	if d.llm == nil {
		d.replyError(ctx, msgCtx, originalMsg, d.localizer.T("error.llm.no_provider"))
		return
	}

	// Stage 1: Initial Spotify search using user input directly
	d.logger.Debug("Stage 1: Performing initial Spotify search",
		zap.String("text", msgCtx.Input.Text))

	initialSpotifyTracks, err := d.spotify.SearchTrack(ctx, msgCtx.Input.Text)
	if err != nil {
		d.logger.Error("Initial Spotify search failed", zap.Error(err))
		d.replyError(ctx, msgCtx, originalMsg, d.localizer.T("error.spotify.search_failed"))
		return
	}

	d.logger.Info("Stage 1 complete: Found Spotify tracks",
		zap.Int("count", len(initialSpotifyTracks)))

	// Stage 2: LLM ranking of Spotify results with user context (or extraction if no results)
	var rankedCandidates []LLMCandidate

	if len(initialSpotifyTracks) > 0 {
		d.logger.Debug("Stage 2: LLM ranking of Spotify results")
		spotifyContext := d.buildSpotifyContextForLLM(initialSpotifyTracks, msgCtx.Input.Text)
		rankedCandidates, err = d.llm.RankCandidates(ctx, spotifyContext)
	} else {
		d.logger.Debug("Stage 2: No initial Spotify results, using LLM extraction")
		rankedCandidates, err = d.llm.RankCandidates(ctx, msgCtx.Input.Text)
	}
	if err != nil {
		d.logger.Error("LLM ranking failed", zap.Error(err))
		if len(initialSpotifyTracks) > 0 {
			// Fallback to Spotify results without LLM ranking
			d.fallbackToSpotifyResults(ctx, msgCtx, originalMsg, initialSpotifyTracks)
		} else {
			d.replyError(ctx, msgCtx, originalMsg, d.localizer.T("error.llm.understand"))
		}
		return
	}

	if len(rankedCandidates) == 0 {
		d.logger.Warn("LLM returned no ranked candidates")
		if len(initialSpotifyTracks) > 0 {
			d.fallbackToSpotifyResults(ctx, msgCtx, originalMsg, initialSpotifyTracks)
		} else {
			d.replyError(ctx, msgCtx, originalMsg, d.localizer.T("error.llm.no_songs"))
		}
		return
	}

	d.logger.Info("Stage 2 complete: LLM ranked Spotify results",
		zap.Int("count", len(rankedCandidates)),
		zap.String("top_candidate", fmt.Sprintf("%s - %s",
			rankedCandidates[0].Track.Artist, rankedCandidates[0].Track.Title)))

	// Stage 3: Enhanced disambiguation with more targeted Spotify search
	d.enhancedLLMDisambiguate(ctx, msgCtx, originalMsg, rankedCandidates)
}

// enhancedLLMDisambiguate performs Stage 3: targeted Spotify search and final LLM ranking
func (d *Dispatcher) enhancedLLMDisambiguate(ctx context.Context, msgCtx *MessageContext,
	originalMsg *chat.Message, rankedCandidates []LLMCandidate) {
	msgCtx.State = StateEnhancedLLMDisambiguate

	// Stage 3a: Targeted Spotify search with LLM-ranked candidates
	d.logger.Debug("Stage 3a: Targeted Spotify search with ranked candidates")

	const maxRankedCandidates = 3 // Limit API calls
	var allSpotifyTracks []Track
	for i, candidate := range rankedCandidates {
		if i >= maxRankedCandidates {
			break
		}

		searchQuery := fmt.Sprintf("%s %s", candidate.Track.Artist, candidate.Track.Title)
		d.logger.Debug("Searching Spotify",
			zap.String("query", searchQuery),
			zap.Float64("confidence", candidate.Confidence))

		tracks, err := d.spotify.SearchTrack(ctx, searchQuery)
		if err != nil {
			d.logger.Warn("Spotify search failed for candidate",
				zap.String("query", searchQuery),
				zap.Error(err))
			continue
		}

		// Take top results from this search
		maxResults := 3
		if len(tracks) < maxResults {
			maxResults = len(tracks)
		}

		for j := 0; j < maxResults; j++ {
			allSpotifyTracks = append(allSpotifyTracks, tracks[j])
		}
	}

	if len(allSpotifyTracks) == 0 {
		d.logger.Error("No Spotify tracks found for any ranked candidates")
		d.replyError(ctx, msgCtx, originalMsg, d.localizer.T("error.spotify.no_matches"))
		return
	}

	d.logger.Info("Stage 3a complete: Found targeted Spotify tracks",
		zap.Int("count", len(allSpotifyTracks)))

	// Stage 3b: Final LLM ranking of targeted Spotify results
	d.logger.Debug("Stage 3b: Final LLM ranking of targeted results")

	spotifyContext := d.buildSpotifyContextForLLM(allSpotifyTracks, msgCtx.Input.Text)
	finalCandidates, err := d.llm.RankCandidates(ctx, spotifyContext)
	if err != nil {
		d.logger.Error("Final LLM ranking failed", zap.Error(err))
		// Fallback to targeted Spotify search results
		d.fallbackToSpotifyResults(ctx, msgCtx, originalMsg, allSpotifyTracks)
		return
	}

	if len(finalCandidates) == 0 {
		d.logger.Warn("Final LLM returned no candidates, using targeted Spotify results")
		d.fallbackToSpotifyResults(ctx, msgCtx, originalMsg, allSpotifyTracks)
		return
	}

	d.logger.Info("Stage 3b complete: Final ranking finished",
		zap.Int("final_candidates", len(finalCandidates)),
		zap.String("top_result", fmt.Sprintf("%s - %s",
			finalCandidates[0].Track.Artist, finalCandidates[0].Track.Title)))

	// Match LLM candidates back to original Spotify tracks to restore URLs and IDs
	d.matchSpotifyTrackData(finalCandidates, allSpotifyTracks)

	// Store enhanced candidates and proceed with user approval
	msgCtx.Candidates = finalCandidates
	best := finalCandidates[0]

	// Validate that we have a Spotify URL (since we're reranking actual Spotify tracks)
	if best.Track.URL == "" {
		d.logger.Warn("Enhanced LLM candidate missing Spotify URL, asking for clarification",
			zap.String("artist", best.Track.Artist),
			zap.String("title", best.Track.Title))
		d.clarifyAsk(ctx, msgCtx, originalMsg, &best)
		return
	}

	if best.Confidence >= d.config.LLM.Threshold {
		d.promptEnhancedApproval(ctx, msgCtx, originalMsg, &best)
	} else {
		d.clarifyAsk(ctx, msgCtx, originalMsg, &best)
	}
}

// buildSpotifyContextForLLM creates enhanced context for LLM re-ranking
func (d *Dispatcher) buildSpotifyContextForLLM(tracks []Track, originalText string) string {
	context := fmt.Sprintf("User said: %q\n\nAvailable songs from Spotify:\n", originalText)

	for i, track := range tracks {
		context += fmt.Sprintf("%d. %s - %s", i+1, track.Artist, track.Title)
		if track.Album != "" {
			context += fmt.Sprintf(" (Album: %s)", track.Album)
		}
		if track.Year > 0 {
			context += fmt.Sprintf(" (%d)", track.Year)
		}
		context += "\n"
	}

	context += "\nPlease rank these songs based on how well they match what the user is looking for."
	return context
}

// matchSpotifyTrackData matches LLM candidates back to original Spotify tracks to restore URLs and IDs
func (d *Dispatcher) matchSpotifyTrackData(candidates []LLMCandidate, spotifyTracks []Track) {
	for i := range candidates {
		candidate := &candidates[i]

		// Find best matching Spotify track
		bestMatch := d.findBestSpotifyMatch(&candidate.Track, spotifyTracks)
		if bestMatch != nil {
			// Restore complete Spotify data
			candidate.Track.ID = bestMatch.ID
			candidate.Track.URL = bestMatch.URL
			candidate.Track.Duration = bestMatch.Duration
			// Keep LLM's values for other fields as they might be more accurate
		} else {
			d.logger.Warn("Could not match LLM candidate to Spotify track",
				zap.String("artist", candidate.Track.Artist),
				zap.String("title", candidate.Track.Title))
		}
	}
}

// findBestSpotifyMatch finds the best matching Spotify track for an LLM candidate
func (d *Dispatcher) findBestSpotifyMatch(track *Track, spotifyTracks []Track) *Track {
	// First try exact match on artist and title
	for i := range spotifyTracks {
		spotifyTrack := &spotifyTracks[i]
		if d.isExactMatch(track, spotifyTrack) {
			return spotifyTrack
		}
	}

	// Then try case-insensitive match
	for i := range spotifyTracks {
		spotifyTrack := &spotifyTracks[i]
		if d.isCaseInsensitiveMatch(track, spotifyTrack) {
			return spotifyTrack
		}
	}

	// Finally try partial match (contains)
	for i := range spotifyTracks {
		spotifyTrack := &spotifyTracks[i]
		if d.isPartialMatch(track, spotifyTrack) {
			return spotifyTrack
		}
	}

	return nil
}

// isExactMatch checks for exact artist and title match
func (d *Dispatcher) isExactMatch(track1, track2 *Track) bool {
	return track1.Artist == track2.Artist && track1.Title == track2.Title
}

// isCaseInsensitiveMatch checks for case-insensitive artist and title match
func (d *Dispatcher) isCaseInsensitiveMatch(track1, track2 *Track) bool {
	return strings.EqualFold(track1.Artist, track2.Artist) && strings.EqualFold(track1.Title, track2.Title)
}

// isPartialMatch checks if one track's info is contained in the other
func (d *Dispatcher) isPartialMatch(track1, track2 *Track) bool {
	artist1 := strings.ToLower(track1.Artist)
	title1 := strings.ToLower(track1.Title)
	artist2 := strings.ToLower(track2.Artist)
	title2 := strings.ToLower(track2.Title)

	// Check if artist and title contain each other (for variations)
	return (strings.Contains(artist1, artist2) || strings.Contains(artist2, artist1)) &&
		(strings.Contains(title1, title2) || strings.Contains(title2, title1))
}

// fallbackToSpotifyResults handles fallback when enhanced LLM fails
func (d *Dispatcher) fallbackToSpotifyResults(ctx context.Context, msgCtx *MessageContext, originalMsg *chat.Message, tracks []Track) {
	const (
		baseConfidence      = 0.7
		confidenceDecrement = 0.1
		minConfidence       = 0.3
	)

	// Convert Spotify tracks to LLM candidates with moderate confidence
	var candidates []LLMCandidate
	for i, track := range tracks {
		confidence := baseConfidence - float64(i)*confidenceDecrement
		if confidence < minConfidence {
			confidence = minConfidence
		}

		candidates = append(candidates, LLMCandidate{
			Track:      track,
			Confidence: confidence,
			Reasoning:  "Spotify search result",
		})
	}

	msgCtx.Candidates = candidates
	best := candidates[0]

	// Use regular approval flow since these are search results
	d.promptApproval(ctx, msgCtx, originalMsg, &best)
}

// promptEnhancedApproval asks for user approval with enhanced context
func (d *Dispatcher) promptEnhancedApproval(ctx context.Context, msgCtx *MessageContext,
	originalMsg *chat.Message, candidate *LLMCandidate) {
	msgCtx.State = StateConfirmationPrompt

	// Build format components
	albumPart := ""
	if candidate.Track.Album != "" {
		albumPart = d.localizer.T("format.album", candidate.Track.Album)
	}

	yearPart := ""
	if candidate.Track.Year > 0 {
		yearPart = d.localizer.T("format.year", candidate.Track.Year)
	}

	urlPart := ""
	if candidate.Track.URL != "" {
		urlPart = d.localizer.T("format.url", candidate.Track.URL)
	}

	prompt := d.localizer.T("prompt.enhanced_approval",
		candidate.Track.Artist, candidate.Track.Title, albumPart, yearPart, urlPart)

	approved, err := d.frontend.AwaitApproval(ctx, originalMsg, prompt, d.config.App.ConfirmTimeoutSecs)
	if err != nil {
		d.logger.Error("Failed to get enhanced approval", zap.Error(err))
		d.replyError(ctx, msgCtx, originalMsg, d.localizer.T("error.generic"))
		return
	}

	if approved {
		d.handleEnhancedApproval(ctx, msgCtx, originalMsg)
	} else {
		d.handleRejection(ctx, msgCtx, originalMsg)
	}
}

// handleEnhancedApproval processes approval for enhanced candidates
func (d *Dispatcher) handleEnhancedApproval(ctx context.Context, msgCtx *MessageContext, originalMsg *chat.Message) {
	if len(msgCtx.Candidates) == 0 {
		d.replyError(ctx, msgCtx, originalMsg, d.localizer.T("error.generic"))
		return
	}

	best := msgCtx.Candidates[0]

	// For enhanced candidates, we already have validated Spotify data
	// Try to find the exact track ID from our previous search
	tracks, err := d.spotify.SearchTrack(ctx, fmt.Sprintf("%s %s", best.Track.Artist, best.Track.Title))
	if err != nil || len(tracks) == 0 {
		d.replyError(ctx, msgCtx, originalMsg, d.localizer.T("error.spotify.not_found"))
		return
	}

	// Find the best matching track (should be the same as our enhanced result)
	var trackID string
	for _, track := range tracks {
		if track.Artist == best.Track.Artist && track.Title == best.Track.Title {
			trackID = track.ID
			break
		}
	}

	// Fallback to first result if exact match not found
	if trackID == "" {
		trackID = tracks[0].ID
	}

	if d.dedup.Has(trackID) {
		d.reactDuplicate(ctx, msgCtx, originalMsg)
		return
	}

	d.addToPlaylist(ctx, msgCtx, originalMsg, trackID)
}

// promptApproval asks for user approval with high confidence
func (d *Dispatcher) promptApproval(ctx context.Context, msgCtx *MessageContext, originalMsg *chat.Message, candidate *LLMCandidate) {
	msgCtx.State = StateConfirmationPrompt

	// Build format components
	yearPart := ""
	if candidate.Track.Year > 0 {
		yearPart = d.localizer.T("format.year", candidate.Track.Year)
	}

	urlPart := ""
	if candidate.Track.URL != "" {
		urlPart = d.localizer.T("format.url", candidate.Track.URL)
	}

	prompt := d.localizer.T("prompt.basic_approval",
		candidate.Track.Artist, candidate.Track.Title, yearPart, urlPart)

	approved, err := d.frontend.AwaitApproval(ctx, originalMsg, prompt, d.config.App.ConfirmTimeoutSecs)
	if err != nil {
		d.logger.Error("Failed to get approval", zap.Error(err))
		d.replyError(ctx, msgCtx, originalMsg, d.localizer.T("error.generic"))
		return
	}

	if approved {
		d.handleApproval(ctx, msgCtx, originalMsg)
	} else {
		d.handleRejection(ctx, msgCtx, originalMsg)
	}
}

// clarifyAsk asks for clarification with lower confidence
func (d *Dispatcher) clarifyAsk(ctx context.Context, msgCtx *MessageContext, originalMsg *chat.Message, candidate *LLMCandidate) {
	msgCtx.State = StateClarifyAsk

	prompt := d.localizer.T("prompt.clarification", candidate.Track.Artist, candidate.Track.Title)

	approved, err := d.frontend.AwaitApproval(ctx, originalMsg, prompt, d.config.App.ConfirmTimeoutSecs)
	if err != nil {
		d.logger.Error("Failed to get clarification", zap.Error(err))
		d.replyError(ctx, msgCtx, originalMsg, d.localizer.T("error.generic"))
		return
	}

	if approved {
		d.handleApproval(ctx, msgCtx, originalMsg)
	} else {
		d.askWhichSong(ctx, msgCtx, originalMsg)
	}
}

// handleApproval processes user approval
func (d *Dispatcher) handleApproval(ctx context.Context, msgCtx *MessageContext, originalMsg *chat.Message) {
	if len(msgCtx.Candidates) == 0 {
		d.replyError(ctx, msgCtx, originalMsg, d.localizer.T("error.generic"))
		return
	}

	best := msgCtx.Candidates[0]

	tracks, err := d.spotify.SearchTrack(ctx, fmt.Sprintf("%s %s", best.Track.Artist, best.Track.Title))
	if err != nil || len(tracks) == 0 {
		d.replyError(ctx, msgCtx, originalMsg, d.localizer.T("error.spotify.not_found"))
		return
	}

	trackID := tracks[0].ID
	if d.dedup.Has(trackID) {
		d.reactDuplicate(ctx, msgCtx, originalMsg)
		return
	}

	d.addToPlaylist(ctx, msgCtx, originalMsg, trackID)
}

// handleRejection processes user rejection
func (d *Dispatcher) handleRejection(ctx context.Context, msgCtx *MessageContext, originalMsg *chat.Message) {
	d.askWhichSong(ctx, msgCtx, originalMsg)
}

// addToPlaylist adds a track to the Spotify playlist
func (d *Dispatcher) addToPlaylist(ctx context.Context, msgCtx *MessageContext, originalMsg *chat.Message, trackID string) {
	msgCtx.SelectedID = trackID

	// Check if admin approval is required
	if d.isAdminApprovalRequired() {
		d.awaitAdminApproval(ctx, msgCtx, originalMsg, trackID)
		return
	}

	d.executePlaylistAdd(ctx, msgCtx, originalMsg, trackID)
}

// isAdminApprovalRequired checks if admin approval is enabled
func (d *Dispatcher) isAdminApprovalRequired() bool {
	// Check if the frontend supports admin approval
	if telegramFrontend, ok := d.frontend.(interface {
		IsAdminApprovalEnabled() bool
	}); ok {
		return telegramFrontend.IsAdminApprovalEnabled()
	}
	return false
}

// awaitAdminApproval requests admin approval before adding to playlist
func (d *Dispatcher) awaitAdminApproval(ctx context.Context, msgCtx *MessageContext, originalMsg *chat.Message, trackID string) {
	msgCtx.State = StateAwaitAdminApproval

	// Get track information for the approval request
	track, err := d.spotify.GetTrack(ctx, trackID)
	if err != nil {
		d.logger.Error("Failed to get track info for admin approval", zap.Error(err))
		d.reactError(ctx, msgCtx, originalMsg, "Failed to get track information")
		return
	}

	songInfo := fmt.Sprintf("%s - %s", track.Artist, track.Title)
	songURL := track.URL

	// Send notification to user that admin approval is required
	if _, err := d.frontend.SendText(ctx, originalMsg.ChatID, originalMsg.ID,
		d.localizer.T("admin.approval_required")); err != nil {
		d.logger.Error("Failed to notify user about admin approval", zap.Error(err))
	}

	// Request admin approval via Telegram frontend
	if telegramFrontend, ok := d.frontend.(interface {
		AwaitAdminApproval(ctx context.Context, origin *chat.Message, songInfo, songURL string, timeoutSec int) (bool, error)
	}); ok {
		approved, err := telegramFrontend.AwaitAdminApproval(ctx, originalMsg, songInfo, songURL, d.config.App.ConfirmAdminTimeoutSecs)
		if err != nil {
			d.logger.Error("Admin approval failed", zap.Error(err))
			d.reactError(ctx, msgCtx, originalMsg, d.localizer.T("error.admin.process_failed"))
			return
		}

		if approved {
			d.logger.Info("Admin approved song addition",
				zap.String("user", originalMsg.SenderName),
				zap.String("song", songInfo))

			// Skip individual approval message - will be combined with success message
			d.executePlaylistAddAfterApproval(ctx, msgCtx, originalMsg, trackID)
		} else {
			d.logger.Info("Admin denied song addition",
				zap.String("user", originalMsg.SenderName),
				zap.String("song", songInfo))

			// Notify user of denial
			if _, err := d.frontend.SendText(ctx, originalMsg.ChatID, originalMsg.ID,
				d.localizer.T("admin.denied")); err != nil {
				d.logger.Error("Failed to notify user about admin denial", zap.Error(err))
			}
		}
	} else {
		d.logger.Error("Frontend doesn't support admin approval, proceeding without")
		d.executePlaylistAdd(ctx, msgCtx, originalMsg, trackID)
	}
}

// executePlaylistAddAfterApproval performs playlist addition after admin approval
func (d *Dispatcher) executePlaylistAddAfterApproval(
	ctx context.Context, msgCtx *MessageContext, originalMsg *chat.Message, trackID string) {
	d.executePlaylistAddWithReaction(ctx, msgCtx, originalMsg, trackID, true)
}

// executePlaylistAdd performs the actual playlist addition
func (d *Dispatcher) executePlaylistAdd(ctx context.Context, msgCtx *MessageContext, originalMsg *chat.Message, trackID string) {
	d.executePlaylistAddWithReaction(ctx, msgCtx, originalMsg, trackID, false)
}

// executePlaylistAddWithReaction performs the actual playlist addition
func (d *Dispatcher) executePlaylistAddWithReaction(
	ctx context.Context, msgCtx *MessageContext, originalMsg *chat.Message, trackID string, wasAdminApproved bool) {
	msgCtx.State = StateAddToPlaylist

	for retry := 0; retry < d.config.App.MaxRetries; retry++ {
		if err := d.spotify.AddToPlaylist(ctx, d.config.Spotify.PlaylistID, trackID); err != nil {
			d.logger.Error("Failed to add to playlist",
				zap.String("trackID", trackID),
				zap.Int("retry", retry),
				zap.Error(err))

			if retry == d.config.App.MaxRetries-1 {
				d.reactError(ctx, msgCtx, originalMsg, d.localizer.T("error.playlist.add_failed"))
				return
			}

			time.Sleep(time.Duration(d.config.App.RetryDelaySecs) * time.Second)
			continue
		}

		d.dedup.Add(trackID)
		if wasAdminApproved {
			d.reactAddedAfterApproval(ctx, msgCtx, originalMsg, trackID)
		} else {
			d.reactAdded(ctx, msgCtx, originalMsg, trackID)
		}
		return
	}
}

// reactAdded reacts to successfully added tracks
func (d *Dispatcher) reactAdded(ctx context.Context, msgCtx *MessageContext, originalMsg *chat.Message, trackID string) {
	d.reactAddedWithMessage(ctx, msgCtx, originalMsg, trackID, "success.track_added")
}

// reactAddedAfterApproval reacts to successfully added tracks after admin approval
func (d *Dispatcher) reactAddedAfterApproval(ctx context.Context, msgCtx *MessageContext, originalMsg *chat.Message, trackID string) {
	d.reactAddedWithMessage(ctx, msgCtx, originalMsg, trackID, "success.admin_approved_and_added")
}

// reactAddedWithMessage reacts to successfully added tracks with a specific message
func (d *Dispatcher) reactAddedWithMessage(
	ctx context.Context, msgCtx *MessageContext, originalMsg *chat.Message, trackID, messageKey string) {
	msgCtx.State = StateReactAdded

	track, err := d.spotify.GetTrack(ctx, trackID)
	if err != nil {
		d.logger.Error("Failed to get track info", zap.Error(err))
		track = &Track{ID: trackID, Title: "Unknown", Artist: "Unknown"}
	}

	// React with thumbs up
	if err := d.frontend.React(ctx, originalMsg.ChatID, originalMsg.ID, thumbsUpReaction); err != nil {
		d.logger.Error("Failed to react with thumbs up", zap.Error(err))
	}

	// Reply with track info using the specified message key
	replyText := d.localizer.T(messageKey, track.Artist, track.Title, track.URL)
	if _, err := d.frontend.SendText(ctx, originalMsg.ChatID, originalMsg.ID, replyText); err != nil {
		d.logger.Error("Failed to reply with added confirmation", zap.Error(err))
	}
}

// reactDuplicate reacts to duplicate tracks
func (d *Dispatcher) reactDuplicate(ctx context.Context, msgCtx *MessageContext, originalMsg *chat.Message) {
	msgCtx.State = StateReactDuplicate

	// React with thumbs down
	if err := d.frontend.React(ctx, originalMsg.ChatID, originalMsg.ID, thumbsDownReaction); err != nil {
		d.logger.Error("Failed to react with thumbs down", zap.Error(err))
	}

	// Reply with duplicate message
	if _, err := d.frontend.SendText(ctx, originalMsg.ChatID, originalMsg.ID, d.localizer.T("success.duplicate")); err != nil {
		d.logger.Error("Failed to reply with duplicate message", zap.Error(err))
	}
}

// reactError reacts to error conditions
func (d *Dispatcher) reactError(ctx context.Context, msgCtx *MessageContext, originalMsg *chat.Message, message string) {
	msgCtx.State = StateReactError

	if _, err := d.frontend.SendText(ctx, originalMsg.ChatID, originalMsg.ID, message); err != nil {
		d.logger.Error("Failed to reply with error message", zap.Error(err))
	}
}

// replyError sends error messages
func (d *Dispatcher) replyError(ctx context.Context, msgCtx *MessageContext, originalMsg *chat.Message, message string) {
	d.reactError(ctx, msgCtx, originalMsg, message)
}

// loadPlaylistSnapshot loads existing tracks from the playlist
func (d *Dispatcher) loadPlaylistSnapshot(ctx context.Context) error {
	trackIDs, err := d.spotify.GetPlaylistTracks(ctx, d.config.Spotify.PlaylistID)
	if err != nil {
		return err
	}

	d.dedup.Load(trackIDs)
	d.logger.Info("Loaded playlist snapshot", zap.Int("tracks", len(trackIDs)))
	return nil
}

// cleanupContext removes message context from memory
func (d *Dispatcher) cleanupContext(messageID string) {
	d.contextMutex.Lock()
	delete(d.messageContexts, messageID)
	d.contextMutex.Unlock()
}

// convertToInputMessage converts a chat.Message to InputMessage
func (d *Dispatcher) convertToInputMessage(msg *chat.Message) InputMessage {
	// Determine message type based on URLs and content
	msgType := MessageTypeFreeText

	if len(msg.URLs) > 0 {
		for _, url := range msg.URLs {
			if _, err := d.spotify.ExtractTrackID(url); err == nil {
				msgType = MessageTypeSpotifyLink
				break
			}
		}
		if msgType == MessageTypeFreeText {
			msgType = MessageTypeNonSpotifyLink
		}
	}

	return InputMessage{
		Type:      msgType,
		Text:      msg.Text,
		URLs:      msg.URLs,
		GroupJID:  msg.ChatID,
		SenderJID: msg.SenderID,
		MessageID: msg.ID,
		Timestamp: time.Now(), // Original timestamp not available in chat.Message
	}
}

// isNotMusicRequest checks if a message is chatter that should be filtered out
func (d *Dispatcher) isNotMusicRequest(ctx context.Context, text string) bool {
	// If no LLM provider is available, use simple heuristics
	if d.llm == nil {
		return d.isLikelyChatter(text)
	}

	// Use LLM to determine if this is chatter
	isNotMusicRequest, err := d.llm.IsNotMusicRequest(ctx, text)
	if err != nil {
		d.logger.Warn("LLM chatter detection failed, using fallback",
			zap.Error(err),
			zap.String("text", text))
		return d.isLikelyChatter(text)
	}

	return isNotMusicRequest
}

// isLikelyChatter provides a simple heuristic fallback for chatter detection
func (d *Dispatcher) isLikelyChatter(text string) bool {
	chatterKeywords := []string{
		"hello", "hi", "hey", "good morning", "good afternoon", "good evening", "good night",
		"how are you", "how's everyone", "what's up", "weather", "lunch", "dinner", "work",
		"tired", "busy", "weekend", "holiday", "birthday", "thanks", "thank you", "lol",
		"haha", "see you", "bye", "goodbye", "later",
	}

	textLower := strings.ToLower(text)

	// Check for obvious chatter patterns
	for _, keyword := range chatterKeywords {
		if strings.Contains(textLower, keyword) {
			return true
		}
	}

	const minMessageLength = 3
	// If very short and no obvious music indicators, likely chatter
	if len(strings.TrimSpace(text)) < minMessageLength {
		return true
	}

	// Default to false (let through) to avoid filtering music requests
	return false
}

// sendStartupMessage sends a startup notification to the group
func (d *Dispatcher) sendStartupMessage(ctx context.Context) {
	if groupID := d.getGroupID(); groupID != "" {
		startupMessage := d.localizer.T("bot.startup")
		if _, err := d.frontend.SendText(ctx, groupID, "", startupMessage); err != nil {
			d.logger.Debug("Failed to send startup message", zap.Error(err))
		}
	}
}

// sendShutdownMessage sends a shutdown notification to the group
func (d *Dispatcher) sendShutdownMessage(ctx context.Context) {
	if groupID := d.getGroupID(); groupID != "" {
		shutdownMessage := d.localizer.T("bot.shutdown")
		if _, err := d.frontend.SendText(ctx, groupID, "", shutdownMessage); err != nil {
			d.logger.Debug("Failed to send shutdown message", zap.Error(err))
		}
	}
}

// getGroupID returns the configured group ID for the active frontend
func (d *Dispatcher) getGroupID() string {
	// Use the configuration to determine the group ID based on enabled frontend
	if d.config.Telegram.Enabled {
		return fmt.Sprintf("%d", d.config.Telegram.GroupID)
	}

	if d.config.WhatsApp.Enabled {
		return d.config.WhatsApp.GroupJID
	}

	return ""
}

// reactProcessing adds a "👀" reaction to show the message is being processed
func (d *Dispatcher) reactProcessing(ctx context.Context, msg *chat.Message) {
	if err := d.frontend.React(ctx, msg.ChatID, msg.ID, "👀"); err != nil {
		d.logger.Debug("Failed to add processing reaction", zap.Error(err))
	}
}

// reactIgnored adds a random "see/hear/speak no evil" emoji to ignored messages
func (d *Dispatcher) reactIgnored(ctx context.Context, msg *chat.Message) {
	// Randomly choose one of the three "no evil" emojis
	ignoredEmojis := []string{"🙈", "🙉", "🙊"}
	emoji := ignoredEmojis[len(msg.ID)%len(ignoredEmojis)] // Simple deterministic selection

	if err := d.frontend.React(ctx, msg.ChatID, msg.ID, chat.Reaction(emoji)); err != nil {
		d.logger.Debug("Failed to add ignored reaction", zap.Error(err))
	}
}
