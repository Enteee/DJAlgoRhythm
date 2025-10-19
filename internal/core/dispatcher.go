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

// autoPlayApprovalContext tracks pending auto-play approval messages with timeout information
type autoPlayApprovalContext struct {
	trackID    string
	chatID     string
	messageID  string
	expiresAt  time.Time
	cancelFunc context.CancelFunc
}

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

	// Playlist monitoring
	lastPlaylistWarning  time.Time
	playlistWarningMutex sync.RWMutex

	// Auto-play approval tracking
	pendingAutoPlayTracks    map[string]string                   // trackID -> track name for pending approvals
	pendingApprovalMessages  map[string]*autoPlayApprovalContext // messageID -> approval context for timeout tracking
	autoPlayReplacementCount int                                 // tracks how many replacements have been attempted in current workflow
	autoPlayMutex            sync.RWMutex
	autoPlayActive           bool // tracks if auto-play prevention is currently running
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
	d := &Dispatcher{
		config:                  config,
		frontend:                frontend,
		spotify:                 spotify,
		llm:                     llm,
		dedup:                   dedup,
		logger:                  logger,
		localizer:               i18n.NewLocalizer(config.App.Language),
		messageContexts:         make(map[string]*MessageContext),
		pendingAutoPlayTracks:   make(map[string]string),
		pendingApprovalMessages: make(map[string]*autoPlayApprovalContext),
	}

	return d
}

// Start initializes the dispatcher and begins processing messages
func (d *Dispatcher) Start(ctx context.Context) error {
	d.logger.Info("Starting message dispatcher")

	// Set target playlist
	if spotifyClient, ok := d.spotify.(interface{ SetTargetPlaylist(string) }); ok {
		spotifyClient.SetTargetPlaylist(d.config.Spotify.PlaylistID)
	}

	// Load existing playlist tracks into dedup store
	if err := d.loadPlaylistSnapshot(ctx); err != nil {
		d.logger.Warn("Failed to load playlist snapshot", zap.Error(err))
	}

	// Start the chat frontend
	if err := d.frontend.Start(ctx); err != nil {
		return fmt.Errorf("failed to start chat frontend: %w", err)
	}

	// Set up auto-play decision handler
	d.frontend.SetAutoPlayDecisionHandler(d.handleAutoPlayDecision)

	// Send startup message to the group
	d.sendStartupMessage(ctx)

	// Start auto-play prevention monitoring
	go d.runAutoPlayPrevention(ctx)

	// Start playlist monitoring
	go d.runPlaylistMonitoring(ctx)

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

	message := d.formatMessageWithMention(originalMsg, d.localizer.T("prompt.which_song"))
	_, err := d.frontend.SendText(ctx, originalMsg.ChatID, originalMsg.ID, message)
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
	promptWithMention := d.formatMessageWithMention(originalMsg, prompt)

	approved, err := d.frontend.AwaitApproval(ctx, originalMsg, promptWithMention, d.config.App.ConfirmTimeoutSecs)
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
	promptWithMention := d.formatMessageWithMention(originalMsg, prompt)

	approved, err := d.frontend.AwaitApproval(ctx, originalMsg, promptWithMention, d.config.App.ConfirmTimeoutSecs)
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
	promptWithMention := d.formatMessageWithMention(originalMsg, prompt)

	approved, err := d.frontend.AwaitApproval(ctx, originalMsg, promptWithMention, d.config.App.ConfirmTimeoutSecs)
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

// addToPlaylist adds a track to the Spotify playlist or queue based on priority
func (d *Dispatcher) addToPlaylist(ctx context.Context, msgCtx *MessageContext, originalMsg *chat.Message, trackID string) {
	msgCtx.SelectedID = trackID

	// Check if this is a priority request from an admin
	isAdmin := d.isUserAdmin(ctx, originalMsg)
	isPriority := false

	if isAdmin && d.llm != nil {
		var err error
		isPriority, err = d.llm.IsPriorityRequest(ctx, originalMsg.Text)
		if err != nil {
			d.logger.Warn("Failed to check priority status, treating as regular request",
				zap.Error(err),
				zap.String("text", originalMsg.Text))
		}

		d.logger.Debug("Priority request check completed",
			zap.Bool("isAdmin", isAdmin),
			zap.Bool("isPriority", isPriority),
			zap.String("text", originalMsg.Text))
	}

	// If it's a priority request from an admin, add to queue for priority playback
	if isAdmin && isPriority {
		d.executePriorityQueue(ctx, msgCtx, originalMsg, trackID)
		return
	}

	// Check if admin approval is required
	// If AdminNeedsApproval is enabled, even admins need approval
	// Otherwise, only non-admins need approval when AdminApproval is enabled
	needsApproval := d.isAdminApprovalRequired() && (!isAdmin || d.isAdminNeedsApproval())
	if needsApproval {
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

// isAdminNeedsApproval checks if admins also need approval
func (d *Dispatcher) isAdminNeedsApproval() bool {
	return d.config.Telegram.AdminNeedsApproval
}

// isUserAdmin checks if the message sender is an admin in the chat
func (d *Dispatcher) isUserAdmin(ctx context.Context, msg *chat.Message) bool {
	isAdmin, err := d.frontend.IsUserAdmin(ctx, msg.ChatID, msg.SenderID)
	if err != nil {
		d.logger.Warn("Failed to check admin status, assuming non-admin",
			zap.String("userID", msg.SenderID),
			zap.String("chatID", msg.ChatID),
			zap.Error(err))
		return false
	}

	d.logger.Debug("Admin status checked",
		zap.String("userID", msg.SenderID),
		zap.String("userName", msg.SenderName),
		zap.Bool("isAdmin", isAdmin))

	return isAdmin
}

// executePriorityQueue adds priority track to queue and playlist
func (d *Dispatcher) executePriorityQueue(ctx context.Context, msgCtx *MessageContext, originalMsg *chat.Message, trackID string) {
	msgCtx.State = StateAddToPlaylist

	// Add priority track to queue (will play next)
	queueErr := d.spotify.AddToQueue(ctx, trackID)
	if queueErr != nil {
		d.logger.Warn("Failed to add priority track to queue, proceeding with playlist only",
			zap.String("trackID", trackID),
			zap.Error(queueErr))
		// If queue fails, fall back to regular playlist addition
		d.executePlaylistAddWithReaction(ctx, msgCtx, originalMsg, trackID, false)
		return
	}

	d.logger.Info("Priority track added to queue",
		zap.String("trackID", trackID))

	// Add to playlist at position 0 (top) for history/deduplication to avoid replaying later
	for retry := 0; retry < d.config.App.MaxRetries; retry++ {
		if err := d.spotify.AddToPlaylistAtPosition(ctx, d.config.Spotify.PlaylistID, trackID, 0); err != nil {
			d.logger.Error("Failed to add priority track to playlist",
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
		d.reactPriorityQueued(ctx, msgCtx, originalMsg, trackID)
		return
	}
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

	// Send notification to user that admin approval is required with song details
	approvalMessage := d.formatAdminApprovalMessage(originalMsg, track)
	approvalMsgID, err := d.frontend.SendText(ctx, originalMsg.ChatID, originalMsg.ID, approvalMessage)
	if err != nil {
		d.logger.Error("Failed to notify user about admin approval", zap.Error(err))
	}

	// Add reaction buttons if message was sent successfully and community approval is enabled
	if approvalMsgID != "" {
		d.addApprovalReactions(ctx, originalMsg.ChatID, approvalMsgID)
	}

	// Check if frontend supports both admin approval and community approval
	telegramFrontend, supportsAdminApproval := d.frontend.(interface {
		AwaitAdminApproval(ctx context.Context, origin *chat.Message, songInfo, songURL string, timeoutSec int) (bool, error)
	})

	communityApprovalFrontend, supportsCommunityApproval := d.frontend.(interface {
		AwaitCommunityApproval(ctx context.Context, msgID string, requiredReactions int, timeoutSec int) (bool, error)
	})

	if !supportsAdminApproval {
		d.logger.Error("Frontend doesn't support admin approval, proceeding without")
		d.executePlaylistAdd(ctx, msgCtx, originalMsg, trackID)
		return
	}

	// Check if community approval is enabled and supported
	communityApprovalThreshold := d.config.Telegram.CommunityApproval
	if supportsCommunityApproval && communityApprovalThreshold > 0 && approvalMsgID != "" {
		// Run both admin approval and community approval concurrently
		d.awaitConcurrentApproval(ctx, msgCtx, originalMsg, trackID, songInfo, songURL, approvalMsgID,
			telegramFrontend, communityApprovalFrontend, communityApprovalThreshold)
	} else {
		// Only admin approval
		d.awaitAdminApprovalOnly(ctx, msgCtx, originalMsg, trackID, songInfo, songURL, approvalMsgID, telegramFrontend)
	}
}

// awaitConcurrentApproval runs both admin and community approval concurrently
func (d *Dispatcher) awaitConcurrentApproval(
	ctx context.Context, msgCtx *MessageContext, originalMsg *chat.Message, trackID, songInfo, songURL, approvalMsgID string,
	adminFrontend interface {
		AwaitAdminApproval(ctx context.Context, origin *chat.Message, songInfo, songURL string, timeoutSec int) (bool, error)
	},
	communityFrontend interface {
		AwaitCommunityApproval(ctx context.Context, msgID string, requiredReactions int, timeoutSec int) (bool, error)
	},
	communityThreshold int,
) {
	// Create channels for results
	adminResult := make(chan bool, 1)
	communityResult := make(chan bool, 1)
	const maxConcurrentApprovals = 2
	errorResult := make(chan error, maxConcurrentApprovals)

	// Start admin approval in goroutine
	go func() {
		approved, err := adminFrontend.AwaitAdminApproval(ctx, originalMsg, songInfo, songURL, d.config.App.ConfirmAdminTimeoutSecs)
		if err != nil {
			errorResult <- err
			return
		}
		adminResult <- approved
	}()

	// Start community approval in goroutine
	go func() {
		approved, err := communityFrontend.AwaitCommunityApproval(ctx, approvalMsgID, communityThreshold, d.config.App.ConfirmAdminTimeoutSecs)
		if err != nil {
			errorResult <- err
			return
		}
		communityResult <- approved
	}()

	// Wait for first approval or timeout
	select {
	case approved := <-adminResult:
		d.handleApprovalResult(ctx, msgCtx, originalMsg, trackID, songInfo, approvalMsgID, approved, "admin")
	case approved := <-communityResult:
		if approved {
			d.handleApprovalResult(ctx, msgCtx, originalMsg, trackID, songInfo, approvalMsgID, true, "community")
		} else {
			// Community approval failed, still wait for admin
			select {
			case approved := <-adminResult:
				d.handleApprovalResult(ctx, msgCtx, originalMsg, trackID, songInfo, approvalMsgID, approved, "admin")
			case err := <-errorResult:
				d.logger.Error("Admin approval failed", zap.Error(err))
				d.reactError(ctx, msgCtx, originalMsg, d.localizer.T("error.admin.process_failed"))
			case <-ctx.Done():
				d.handleApprovalResult(ctx, msgCtx, originalMsg, trackID, songInfo, approvalMsgID, false, "timeout")
			}
		}
	case err := <-errorResult:
		d.logger.Error("Approval process failed", zap.Error(err))
		d.reactError(ctx, msgCtx, originalMsg, d.localizer.T("error.admin.process_failed"))
	case <-ctx.Done():
		d.handleApprovalResult(ctx, msgCtx, originalMsg, trackID, songInfo, approvalMsgID, false, "timeout")
	}
}

// awaitAdminApprovalOnly handles only admin approval (legacy behavior)
func (d *Dispatcher) awaitAdminApprovalOnly(
	ctx context.Context, msgCtx *MessageContext, originalMsg *chat.Message, trackID, songInfo, songURL, approvalMsgID string,
	adminFrontend interface {
		AwaitAdminApproval(ctx context.Context, origin *chat.Message, songInfo, songURL string, timeoutSec int) (bool, error)
	},
) {
	approved, err := adminFrontend.AwaitAdminApproval(ctx, originalMsg, songInfo, songURL, d.config.App.ConfirmAdminTimeoutSecs)
	if err != nil {
		d.logger.Error("Admin approval failed", zap.Error(err))
		d.reactError(ctx, msgCtx, originalMsg, d.localizer.T("error.admin.process_failed"))
		return
	}

	d.handleApprovalResult(ctx, msgCtx, originalMsg, trackID, songInfo, approvalMsgID, approved, "admin")
}

// handleApprovalResult processes the approval result regardless of source
func (d *Dispatcher) handleApprovalResult(
	ctx context.Context, msgCtx *MessageContext, originalMsg *chat.Message, trackID, songInfo, approvalMsgID string,
	approved bool, approvalSource string,
) {
	// Delete the admin approval required message
	if approvalMsgID != "" {
		if deleteErr := d.frontend.DeleteMessage(ctx, originalMsg.ChatID, approvalMsgID); deleteErr != nil {
			d.logger.Debug("Failed to delete admin approval message", zap.Error(deleteErr))
		}
	}

	if approved {
		d.logger.Info("Song addition approved",
			zap.String("user", originalMsg.SenderName),
			zap.String("song", songInfo),
			zap.String("approval_source", approvalSource))

		// Skip individual approval message - will be combined with success message
		d.executePlaylistAddAfterApproval(ctx, msgCtx, originalMsg, trackID)
	} else {
		d.logger.Info("Song addition denied",
			zap.String("user", originalMsg.SenderName),
			zap.String("song", songInfo),
			zap.String("approval_source", approvalSource))

		// Notify user of denial
		denialMessage := d.formatMessageWithMention(originalMsg, d.localizer.T("admin.denied"))
		if _, err := d.frontend.SendText(ctx, originalMsg.ChatID, originalMsg.ID, denialMessage); err != nil {
			d.logger.Error("Failed to notify user about denial", zap.Error(err))
		}
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

	// Add track to playlist
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

// reactPriorityQueued reacts to priority tracks that were queued successfully
func (d *Dispatcher) reactPriorityQueued(ctx context.Context, msgCtx *MessageContext, originalMsg *chat.Message, trackID string) {
	d.reactAddedWithMessage(ctx, msgCtx, originalMsg, trackID, "success.track_priority_playing")
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

	// Try to get playlist position for added track (more reliable than queue position)
	var replyText string
	if playlistPosition, err := d.spotify.GetPlaylistPosition(ctx, trackID); err == nil && playlistPosition >= 0 {
		// Track found in playlist - use message with queue position
		var queueMessageKey string
		switch messageKey {
		case "success.track_added":
			queueMessageKey = "success.track_added_with_queue"
		case "success.admin_approved_and_added":
			queueMessageKey = "success.admin_approved_and_added_queue"
		default:
			// For other messages (like priority playing), fall back to original
			queueMessageKey = messageKey
		}

		if queueMessageKey != messageKey {
			// Use queue position message with 1-based indexing for user display
			replyText = d.localizer.T(queueMessageKey, track.Artist, track.Title, track.URL, playlistPosition+1)
		} else {
			// Use original message format
			replyText = d.localizer.T(messageKey, track.Artist, track.Title, track.URL)
		}
	} else {
		// Playlist position not available or error occurred - use original message
		if err != nil {
			d.logger.Debug("Could not get playlist position", zap.Error(err))
		}
		replyText = d.localizer.T(messageKey, track.Artist, track.Title, track.URL)
	}

	messageWithMention := d.formatMessageWithMention(originalMsg, replyText)
	if _, err := d.frontend.SendText(ctx, originalMsg.ChatID, originalMsg.ID, messageWithMention); err != nil {
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
	duplicateMessage := d.formatMessageWithMention(originalMsg, d.localizer.T("success.duplicate"))
	if _, err := d.frontend.SendText(ctx, originalMsg.ChatID, originalMsg.ID, duplicateMessage); err != nil {
		d.logger.Error("Failed to reply with duplicate message", zap.Error(err))
	}
}

// reactError reacts to error conditions
func (d *Dispatcher) reactError(ctx context.Context, msgCtx *MessageContext, originalMsg *chat.Message, message string) {
	msgCtx.State = StateReactError

	errorMessage := d.formatMessageWithMention(originalMsg, message)
	if _, err := d.frontend.SendText(ctx, originalMsg.ChatID, originalMsg.ID, errorMessage); err != nil {
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
		playlistURL := fmt.Sprintf("https://open.spotify.com/playlist/%s", d.config.Spotify.PlaylistID)
		startupMessage := d.localizer.T("bot.startup", playlistURL)
		if _, err := d.frontend.SendText(ctx, groupID, "", startupMessage); err != nil {
			d.logger.Debug("Failed to send startup message", zap.Error(err))
		}
	}
}

// sendShutdownMessage sends a shutdown notification to the group
func (d *Dispatcher) sendShutdownMessage(ctx context.Context) {
	if groupID := d.getGroupID(); groupID != "" {
		playlistURL := fmt.Sprintf("https://open.spotify.com/playlist/%s", d.config.Spotify.PlaylistID)
		shutdownMessage := d.localizer.T("bot.shutdown", playlistURL)
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

// formatUserMention creates a user mention string based on the frontend type
func (d *Dispatcher) formatUserMention(msg *chat.Message) string {
	// Always add @ prefix if not already present
	if strings.HasPrefix(msg.SenderName, "@") {
		return msg.SenderName
	}
	return "@" + msg.SenderName
}

// formatMessageWithMention adds user mention to a message
func (d *Dispatcher) formatMessageWithMention(msg *chat.Message, messageText string) string {
	mention := d.formatUserMention(msg)
	return fmt.Sprintf("%s %s", mention, messageText)
}

// formatAdminApprovalMessage creates an enhanced admin approval message with song details
func (d *Dispatcher) formatAdminApprovalMessage(originalMsg *chat.Message, track *Track) string {
	// Format album and year information
	albumInfo := ""
	if track.Album != "" {
		albumInfo = d.localizer.T("format.album", track.Album)
	}

	yearInfo := ""
	if track.Year > 0 {
		yearInfo = d.localizer.T("format.year", track.Year)
	}

	urlInfo := ""
	if track.URL != "" {
		urlInfo = d.localizer.T("format.url", track.URL)
	}

	// Check if community approval is enabled and supported
	communityApprovalThreshold := d.config.Telegram.CommunityApproval
	supportsCommunityApproval := false
	if _, ok := d.frontend.(interface {
		AwaitCommunityApproval(ctx context.Context, msgID string, requiredReactions, timeoutSec int) (bool, error)
	}); ok {
		supportsCommunityApproval = true
	}

	var message string
	if supportsCommunityApproval && communityApprovalThreshold > 0 {
		// Enhanced message with community approval instructions
		message = d.localizer.T("admin.approval_required_community",
			track.Artist, track.Title, albumInfo, yearInfo, urlInfo, communityApprovalThreshold)
	} else {
		// Enhanced message without community approval
		message = d.localizer.T("admin.approval_required_enhanced",
			track.Artist, track.Title, albumInfo, yearInfo, urlInfo)
	}

	return d.formatMessageWithMention(originalMsg, message)
}

// addApprovalReactions adds 👍 reaction button to admin approval messages to encourage community voting
func (d *Dispatcher) addApprovalReactions(ctx context.Context, chatID, msgID string) {
	// Check if community approval is enabled
	communityApprovalThreshold := d.config.Telegram.CommunityApproval
	if communityApprovalThreshold <= 0 {
		return // Community approval disabled, no need for reaction buttons
	}

	// Add 👍 reaction to encourage users to vote for community approval
	// This shows users they can react with 👍 to support the song
	if err := d.frontend.React(ctx, chatID, msgID, chat.ReactionThumbsUp); err != nil {
		d.logger.Warn("Failed to add thumbs up reaction to approval message", zap.Error(err))
	} else {
		d.logger.Debug("Successfully added 👍 reaction to encourage community voting", zap.String("message_id", msgID))
	}

	d.logger.Info("Added 👍 reaction to admin approval message to encourage community voting",
		zap.String("chat_id", chatID),
		zap.String("message_id", msgID),
		zap.Int("community_threshold", communityApprovalThreshold))
}

const (
	autoPlayCheckInterval   = 30 * time.Second
	playlistCheckInterval   = 15 * time.Second // Check playlist more frequently
	playlistWarningDebounce = 5 * time.Minute  // Wait 5 minutes between warnings
)

// runAutoPlayPrevention monitors for playlist end and adds tracks to prevent auto-play
func (d *Dispatcher) runAutoPlayPrevention(ctx context.Context) {
	d.logger.Info("Starting auto-play prevention monitoring")

	ticker := time.NewTicker(autoPlayCheckInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			d.logger.Info("Auto-play prevention monitoring stopped")
			return
		case <-ticker.C:
			d.checkAndPreventAutoPlay(ctx)
		}
	}
}

// checkAndPreventAutoPlay checks if we're near the end of the playlist and adds a track if needed
func (d *Dispatcher) checkAndPreventAutoPlay(ctx context.Context) {
	// Check if auto-play prevention is already active
	d.autoPlayMutex.Lock()
	if d.autoPlayActive {
		d.autoPlayMutex.Unlock()
		d.logger.Debug("Auto-play prevention already active, skipping")
		return
	}
	d.autoPlayActive = true
	d.autoPlayMutex.Unlock()

	// Check if we're near the end of the playlist
	nearEnd, err := d.spotify.IsNearPlaylistEnd(ctx)
	if err != nil {
		d.logger.Debug("Could not check playlist end status", zap.Error(err))
		d.resetAutoPlayFlag()
		return
	}

	if !nearEnd {
		// Not near the end, reset flag and return
		d.resetAutoPlayFlag()
		return
	}

	d.logger.Info("Near end of playlist detected, adding auto-play prevention track")

	// Get a track from auto-play recommendations
	trackID, err := d.spotify.GetAutoPlayPreventionTrack(ctx)
	if err != nil {
		d.logger.Warn("Failed to get auto-play prevention track", zap.Error(err))
		d.resetAutoPlayFlag()
		return
	}

	// Add track to playlist with retry logic (same as user requests)
	for retry := 0; retry < d.config.App.MaxRetries; retry++ {
		err = d.spotify.AddToPlaylist(ctx, d.config.Spotify.PlaylistID, trackID)
		if err != nil {
			d.logger.Error("Failed to add auto-play prevention track to playlist",
				zap.String("trackID", trackID),
				zap.Int("retry", retry),
				zap.Error(err))

			if retry == d.config.App.MaxRetries-1 {
				d.logger.Warn("Failed to add auto-play prevention track after all retries", zap.Error(err))
				d.resetAutoPlayFlag()
				return
			}

			time.Sleep(time.Duration(d.config.App.RetryDelaySecs) * time.Second)
			continue
		}

		d.logger.Info("Successfully added auto-play prevention track",
			zap.String("trackID", trackID),
			zap.Int("attempt", retry+1))
		break
	}

	// Add to dedup store
	d.dedup.Add(trackID)

	// Get track info for the message
	track, err := d.spotify.GetTrack(ctx, trackID)
	if err != nil {
		d.logger.Warn("Could not get track info for auto-play prevention message", zap.Error(err))
		track = &Track{Title: "Unknown", Artist: "Unknown"}
	}

	// Track this auto-play track for approval
	trackName := fmt.Sprintf("%s - %s", track.Artist, track.Title)
	d.autoPlayMutex.Lock()
	d.pendingAutoPlayTracks[trackID] = trackName
	d.autoPlayMutex.Unlock()

	// Send message to chat about the auto-added track with approval buttons
	d.sendAutoPlayApprovalMessage(ctx, trackID, track, "bot.autoplay_prevention", "auto-play prevention")
}

// handleAutoPlayDecision processes auto-play approval/denial decisions
func (d *Dispatcher) handleAutoPlayDecision(trackID string, approved bool) {
	ctx := context.Background()

	d.autoPlayMutex.Lock()
	trackName, exists := d.pendingAutoPlayTracks[trackID]
	if exists {
		delete(d.pendingAutoPlayTracks, trackID)
	}

	// Cancel any pending timeout for this track
	var messageToCancel string
	for messageID, approvalCtx := range d.pendingApprovalMessages {
		if approvalCtx.trackID == trackID {
			approvalCtx.cancelFunc() // Cancel the timeout
			delete(d.pendingApprovalMessages, messageID)
			messageToCancel = messageID
			break
		}
	}
	d.autoPlayMutex.Unlock()

	if !exists {
		d.logger.Warn("Received auto-play decision for unknown track", zap.String("trackID", trackID))
		return
	}

	if messageToCancel != "" {
		d.logger.Debug("Canceled auto-play approval timeout",
			zap.String("trackID", trackID),
			zap.String("messageID", messageToCancel))
	}

	if approved {
		d.logger.Info("Auto-play track approved",
			zap.String("trackID", trackID),
			zap.String("trackName", trackName))
		// No message sent - the frontend handles the visual feedback (thumbs up reaction)

		// Auto-play workflow is complete, reset the flag
		d.resetAutoPlayFlag()
	} else {
		d.logger.Info("Auto-play track denied, removing from playlist",
			zap.String("trackID", trackID),
			zap.String("trackName", trackName))

		// Remove the denied track from the playlist
		if err := d.spotify.RemoveFromPlaylist(ctx, d.config.Spotify.PlaylistID, trackID); err != nil {
			d.logger.Warn("Failed to remove denied auto-play track from playlist",
				zap.String("trackID", trackID),
				zap.Error(err))
		} else {
			// Remove from dedup store so it can be added again if requested
			d.dedup.Remove(trackID)
		}

		// Try to get a new auto-play prevention track to replace the denied one
		go d.findAndSuggestReplacementTrack(ctx)
	}
}

// resetAutoPlayFlag resets the auto-play active flag and replacement count
func (d *Dispatcher) resetAutoPlayFlag() {
	d.autoPlayMutex.Lock()
	d.autoPlayActive = false
	d.autoPlayReplacementCount = 0
	d.autoPlayMutex.Unlock()
}

// findAndSuggestReplacementTrack finds a replacement auto-play track and suggests it for approval
func (d *Dispatcher) findAndSuggestReplacementTrack(ctx context.Context) {
	d.logger.Info("Attempting to find replacement auto-play track")

	// Increment replacement count and check if we've exceeded the maximum
	d.autoPlayMutex.Lock()
	d.autoPlayReplacementCount++
	replacementCount := d.autoPlayReplacementCount
	maxReplacements := d.config.App.MaxAutoPlayReplacements
	d.autoPlayMutex.Unlock()

	// Get a replacement track
	newTrackID, err := d.spotify.GetAutoPlayPreventionTrack(ctx)
	if err != nil {
		d.logger.Warn("Failed to get replacement auto-play track", zap.Error(err))

		// Inform users that we couldn't find a replacement
		if groupID := d.getGroupID(); groupID != "" {
			message := d.localizer.T("bot.autoplay_replacement_failed")
			if _, sendErr := d.frontend.SendText(ctx, groupID, "", message); sendErr != nil {
				d.logger.Warn("Failed to send replacement failure message", zap.Error(sendErr))
			}
		}

		// Auto-play workflow failed, reset the flag
		d.resetAutoPlayFlag()
		return
	}

	// Add the replacement track to the playlist
	if addErr := d.spotify.AddToPlaylist(ctx, d.config.Spotify.PlaylistID, newTrackID); addErr != nil {
		d.logger.Warn("Failed to add replacement auto-play track", zap.Error(addErr))

		// Inform users that we couldn't add the replacement
		if groupID := d.getGroupID(); groupID != "" {
			message := d.localizer.T("bot.autoplay_replacement_failed")
			if _, sendErr := d.frontend.SendText(ctx, groupID, "", message); sendErr != nil {
				d.logger.Warn("Failed to send replacement failure message", zap.Error(sendErr))
			}
		}

		// Auto-play workflow failed, reset the flag
		d.resetAutoPlayFlag()
		return
	}

	// Add to dedup store
	d.dedup.Add(newTrackID)

	// Get track info for the replacement message
	track, err := d.spotify.GetTrack(ctx, newTrackID)
	if err != nil {
		d.logger.Warn("Could not get track info for replacement auto-play track", zap.Error(err))
		track = &Track{Title: "Unknown", Artist: "Unknown"}
	}

	// Check if we've reached the maximum replacement attempts
	if replacementCount >= maxReplacements && maxReplacements > 0 {
		d.logger.Info("Maximum auto-play replacements reached, auto-accepting track",
			zap.Int("replacementCount", replacementCount),
			zap.Int("maxReplacements", maxReplacements),
			zap.String("trackID", newTrackID))

		// Send notification that track was auto-accepted due to max replacements
		if groupID := d.getGroupID(); groupID != "" {
			message := d.localizer.T("success.track_added", track.Artist, track.Title, track.URL)
			if _, sendErr := d.frontend.SendText(ctx, groupID, "", message); sendErr != nil {
				d.logger.Warn("Failed to send auto-accepted track message", zap.Error(sendErr))
			}
		}

		// Auto-play workflow complete, reset the flag
		d.resetAutoPlayFlag()
		return
	}

	// Track this replacement for approval
	trackName := fmt.Sprintf("%s - %s", track.Artist, track.Title)
	d.autoPlayMutex.Lock()
	d.pendingAutoPlayTracks[newTrackID] = trackName
	d.autoPlayMutex.Unlock()

	// Send message to chat about the replacement track with approval buttons
	d.sendAutoPlayApprovalMessage(ctx, newTrackID, track, "bot.autoplay_replacement", "replacement auto-play track")
}

// sendAutoPlayApprovalMessage sends an auto-play approval message with fallback to regular text
func (d *Dispatcher) sendAutoPlayApprovalMessage(ctx context.Context, trackID string, track *Track, messageKey, logContext string) {
	groupID := d.getGroupID()
	if groupID == "" {
		return
	}

	message := d.localizer.T(messageKey, track.Artist, track.Title, track.URL)

	if messageID, err := d.frontend.SendAutoPlayApproval(ctx, groupID, trackID, message); err != nil {
		d.logger.Warn("Failed to send "+logContext+" message", zap.Error(err))
		// If sending approval message fails, fall back to regular text
		if _, fallbackErr := d.frontend.SendText(ctx, groupID, "", message); fallbackErr != nil {
			d.logger.Warn("Failed to send fallback "+logContext+" message", zap.Error(fallbackErr))
		}
	} else {
		// Start timeout tracking for this approval message
		d.startAutoPlayApprovalTimeout(ctx, messageID, trackID, groupID)

		d.logger.Info("Sent "+logContext+" message with approval buttons",
			zap.String("trackID", trackID),
			zap.String("messageID", messageID),
			zap.String("artist", track.Artist),
			zap.String("title", track.Title))
	}
}

// startAutoPlayApprovalTimeout starts timeout tracking for an auto-play approval message
func (d *Dispatcher) startAutoPlayApprovalTimeout(ctx context.Context, messageID, trackID, chatID string) {
	timeoutSecs := d.config.App.AutoPlayApprovalTimeoutSecs
	if timeoutSecs <= 0 {
		return // Timeout disabled
	}

	timeoutCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSecs)*time.Second)

	approvalCtx := &autoPlayApprovalContext{
		trackID:    trackID,
		chatID:     chatID,
		messageID:  messageID,
		expiresAt:  time.Now().Add(time.Duration(timeoutSecs) * time.Second),
		cancelFunc: cancel,
	}

	d.autoPlayMutex.Lock()
	d.pendingApprovalMessages[messageID] = approvalCtx
	d.autoPlayMutex.Unlock()

	// Start timeout goroutine
	go d.handleAutoPlayApprovalTimeout(timeoutCtx, messageID)
}

// handleAutoPlayApprovalTimeout handles timeout expiry for auto-play approval messages
func (d *Dispatcher) handleAutoPlayApprovalTimeout(ctx context.Context, messageID string) {
	<-ctx.Done()

	// Check if timeout expired (not canceled by approval/denial)
	if ctx.Err() == context.DeadlineExceeded {
		d.autoPlayMutex.Lock()
		approvalCtx, exists := d.pendingApprovalMessages[messageID]
		if !exists {
			d.autoPlayMutex.Unlock()
			return // Already handled
		}

		trackID := approvalCtx.trackID
		chatID := approvalCtx.chatID

		// Clean up pending approval
		delete(d.pendingApprovalMessages, messageID)
		d.autoPlayMutex.Unlock()

		d.logger.Info("Auto-play approval timed out, auto-accepting track",
			zap.String("trackID", trackID),
			zap.String("messageID", messageID))

		// Auto-accept the track by removing buttons and keeping the track in playlist
		d.removeAutoPlayApprovalButtons(context.Background(), chatID, messageID)

		// Reset auto-play flag to allow new workflows
		d.resetAutoPlayFlag()
	}
}

// removeAutoPlayApprovalButtons removes approval buttons from an auto-play message
func (d *Dispatcher) removeAutoPlayApprovalButtons(ctx context.Context, chatID, messageID string) {
	// For Telegram, we can edit the message to remove the inline keyboard
	// For WhatsApp, this is a no-op since it doesn't support inline buttons

	// Get the message content without buttons
	expiredMessage := d.localizer.T("callback.autoplay_expired")

	// Try to edit the message to remove buttons (Telegram-specific)
	// This will gracefully fail for WhatsApp and other platforms that don't support message editing
	if err := d.editMessageToRemoveButtons(ctx, chatID, messageID, expiredMessage); err != nil {
		d.logger.Debug("Could not edit message to remove buttons (expected for WhatsApp)",
			zap.String("messageID", messageID),
			zap.Error(err))
	}
}

// editMessageToRemoveButtons attempts to edit a message to remove inline buttons (Telegram-specific)
func (d *Dispatcher) editMessageToRemoveButtons(ctx context.Context, chatID, messageID, newText string) error {
	return d.frontend.EditMessage(ctx, chatID, messageID, newText)
}

// runPlaylistMonitoring monitors if we're playing from the correct playlist
func (d *Dispatcher) runPlaylistMonitoring(ctx context.Context) {
	d.logger.Info("Starting playlist monitoring")

	ticker := time.NewTicker(playlistCheckInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			d.logger.Info("Playlist monitoring stopped")
			return
		case <-ticker.C:
			d.checkPlaylistCompliance(ctx)
		}
	}
}

// checkPlaylistCompliance checks if we're playing from the correct playlist and sends warnings if needed
func (d *Dispatcher) checkPlaylistCompliance(ctx context.Context) {
	// Check comprehensive playback compliance
	compliance, err := d.spotify.CheckPlaybackCompliance(ctx)
	if err != nil {
		d.logger.Debug("Could not check playback compliance", zap.Error(err))
		return
	}

	if compliance.IsOptimalForAutoDJ() {
		// Everything is fine
		return
	}

	// Not playing from correct playlist - check if we should send a warning
	d.playlistWarningMutex.RLock()
	timeSinceLastWarning := time.Since(d.lastPlaylistWarning)
	d.playlistWarningMutex.RUnlock()

	if timeSinceLastWarning < playlistWarningDebounce {
		// Too soon to send another warning
		d.logger.Debug("Playback compliance issues detected but warning debounced",
			zap.Duration("timeSinceLastWarning", timeSinceLastWarning),
			zap.Duration("debounceThreshold", playlistWarningDebounce),
			zap.Strings("issues", compliance.Issues))
		return
	}

	d.logger.Info("Playback compliance issues detected, sending admin warning",
		zap.Strings("issues", compliance.Issues))

	// Get group ID and admin IDs
	groupID := d.getGroupID()
	if groupID == "" {
		d.logger.Warn("No group ID available for playlist warning")
		return
	}

	// Get list of admin user IDs
	adminUserIDs, err := d.frontend.GetAdminUserIDs(ctx, groupID)
	if err != nil {
		d.logger.Warn("Failed to get admin user IDs for playlist warning", zap.Error(err))
		return
	}

	if len(adminUserIDs) == 0 {
		d.logger.Warn("No admin user IDs found for playlist warning")
		return
	}

	// Generate Spotify playlist URL for easy recovery
	playlistURL := fmt.Sprintf("https://open.spotify.com/playlist/%s", d.config.Spotify.PlaylistID)

	// Generate detailed warning message based on compliance issues
	message := d.generateComplianceWarningMessage(compliance, playlistURL)
	successCount := 0
	var errors []string

	for _, adminUserID := range adminUserIDs {
		if err := d.frontend.SendDirectMessage(ctx, adminUserID, message); err != nil {
			d.logger.Warn("Failed to send playlist warning to admin",
				zap.String("adminUserID", adminUserID),
				zap.Error(err))
			errors = append(errors, err.Error())
		} else {
			successCount++
			d.logger.Debug("Sent playlist warning to admin",
				zap.String("adminUserID", adminUserID))
		}
	}

	if successCount == 0 {
		d.logger.Error("Failed to send playlist warning to any admins",
			zap.Strings("errors", errors))
		return
	}

	// Update last warning time
	d.playlistWarningMutex.Lock()
	d.lastPlaylistWarning = time.Now()
	d.playlistWarningMutex.Unlock()

	d.logger.Info("Sent playlist compliance warning message")
}

// generateComplianceWarningMessage creates a detailed warning message based on compliance issues
func (d *Dispatcher) generateComplianceWarningMessage(compliance *PlaybackCompliance, playlistURL string) string {
	var parts []string

	// Add appropriate warning based on specific issues
	if !compliance.IsCorrectPlaylist {
		parts = append(parts, d.localizer.T("bot.playlist_warning", playlistURL))
	}

	if !compliance.IsCorrectShuffle {
		parts = append(parts, d.localizer.T("bot.shuffle_warning"))
	}

	if !compliance.IsCorrectRepeat {
		parts = append(parts, d.localizer.T("bot.repeat_warning"))
	}

	// If we have multiple issues, combine them; otherwise use the single issue message
	if len(parts) > 1 {
		// Multiple issues - use comprehensive warning
		return d.localizer.T("bot.playback_compliance_warning", playlistURL)
	} else if len(parts) == 1 {
		// Single issue
		return parts[0]
	}

	// Fallback (shouldn't happen)
	return d.localizer.T("bot.playlist_warning", playlistURL)
}
