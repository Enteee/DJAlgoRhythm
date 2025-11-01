package core

import (
	"context"
	"fmt"
	"sync"
	"time"

	"go.uber.org/zap"

	"djalgorhythm/internal/chat"
	"djalgorhythm/internal/i18n"
)

// MusicLinkResolver defines the interface for resolving music links from various providers.
type MusicLinkResolver interface {
	// Resolve attempts to resolve a music link to track information.
	Resolve(ctx context.Context, url string) (*MusicLinkTrackInfo, error)
	// CanResolve checks if this resolver can handle the given URL.
	CanResolve(url string) bool
}

// MusicLinkTrackInfo holds track information extracted from a music provider link.
type MusicLinkTrackInfo struct {
	Title  string // Track title.
	Artist string // Artist name(s).
	ISRC   string // International Standard Recording Code (if available).
}

// Dispatcher handles messages from any chat frontend using the unified interface.
type Dispatcher struct {
	config         *Config
	frontend       chat.Frontend
	spotify        SpotifyClient
	llm            LLMProvider
	dedup          DedupStore
	logger         *zap.Logger
	localizer      *i18n.Localizer
	musicLinkMgr   MusicLinkResolver // Music link resolver for multi-provider support.

	messageContexts map[string]*MessageContext
	contextMutex    sync.RWMutex

	// Unified admin warning management
	warningManager *AdminWarningManager

	// Queue management approval tracking
	pendingApprovalMessages map[string]*queueApprovalContext // messageID -> approval context for timeout tracking
	queueManagementFlows    map[string]*QueueManagementFlow  // flowID -> flow state for per-flow rejection tracking
	queueManagementMutex    sync.RWMutex
	queueManagementActive   bool // tracks if queue management is currently running

	// Shadow queue tracking for reliable queue management
	shadowQueue             []ShadowQueueItem // tracks we've actually queued to Spotify
	shadowQueueMutex        sync.RWMutex
	lastCurrentTrackID      string    // track when current song changes for progression tracking
	lastShadowQueueModified time.Time // when shadow queue was last modified (addition/removal)
	lastSuccessfulSync      time.Time // when sync with Spotify queue last succeeded
	consecutiveSyncRemovals int       // count of consecutive sync operations that removed items

	// Priority track registry for resume logic
	priorityTracks      map[string]PriorityTrackInfo // track IDs of priority tracks with resume info
	priorityTracksMutex sync.RWMutex                 // protects priority tracks map

	// Queue management wake-up channel for event-driven queue filling
	queueManagementWakeup chan struct{} // buffered channel to wake up queue manager when playlist changes
}

// NewDispatcher creates a new dispatcher with the provided chat frontend.
func NewDispatcher(
	config *Config,
	frontend chat.Frontend,
	spotify SpotifyClient,
	llm LLMProvider,
	dedup DedupStore,
	musicLinkMgr MusicLinkResolver,
	logger *zap.Logger,
) *Dispatcher {
	d := &Dispatcher{
		config:                  config,
		frontend:                frontend,
		spotify:                 spotify,
		llm:                     llm,
		dedup:                   dedup,
		musicLinkMgr:            musicLinkMgr,
		logger:                  logger,
		localizer:               i18n.NewLocalizer(config.App.Language),
		warningManager:          NewAdminWarningManager(frontend, logger),
		messageContexts:         make(map[string]*MessageContext),
		pendingApprovalMessages: make(map[string]*queueApprovalContext),
		queueManagementFlows:    make(map[string]*QueueManagementFlow),
		shadowQueue:             make([]ShadowQueueItem, 0),
		lastShadowQueueModified: time.Now(),
		lastSuccessfulSync:      time.Now(),
		priorityTracks:          make(map[string]PriorityTrackInfo),
		queueManagementWakeup:   make(chan struct{}, 1), // Buffer size 1 to coalesce multiple events
	}

	return d
}

// Start initializes the dispatcher and begins processing messages.
func (d *Dispatcher) Start(ctx context.Context) error {
	d.logger.Info("Starting message dispatcher")

	// Set target playlist
	if spotifyClient, ok := d.spotify.(interface{ SetTargetPlaylist(_ string) }); ok {
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

	// Set up queue decision handler
	d.frontend.SetQueueTrackDecisionHandler(d.handleQueueTrackDecision)

	// Send startup message to the group
	d.sendStartupMessage(ctx)

	// Start queue and playlist management
	go d.runQueueAndPlaylistManagement(ctx)

	// Start playback settings monitoring
	go d.runPlaybackSettingsMonitoring(ctx)

	// Start admin permissions monitoring
	go d.runAdminPermissionsMonitoring(ctx)

	// Start shadow queue maintenance
	go d.runShadowQueueMaintenance(ctx)

	// Begin listening for messages
	return d.frontend.Listen(ctx, d.handleMessage)
}

// Stop gracefully shuts down the dispatcher.
func (d *Dispatcher) Stop(ctx context.Context) error {
	d.logger.Info("Stopping message dispatcher")

	// Send shutdown message to the group
	d.sendShutdownMessage(ctx)

	return nil
}

// handleMessage processes incoming chat messages.
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

// processMessage handles the main message processing logic.
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
		d.handleNonSpotifyLink(ctx, msgCtx, originalMsg)
	case MessageTypeFreeText:
		// Filter out obvious chatter
		if d.isNotMusicRequest(ctx, msgCtx.Input.Text) {
			d.logger.Debug("Message filtered out as chatter",
				zap.String("text", msgCtx.Input.Text))
			// Check if this is a help request
			if d.isHelpRequest(ctx, msgCtx.Input.Text) {
				d.logger.Debug("Help request detected",
					zap.String("text", msgCtx.Input.Text))
				// React with OK hand emoji to acknowledge the help request
				if err := d.frontend.React(ctx, originalMsg.ChatID, originalMsg.ID, "👌"); err != nil {
					d.logger.Debug("Failed to add OK hand reaction", zap.Error(err))
				}
				d.replyHelp(ctx, originalMsg)
				return
			}
			d.reactIgnored(ctx, originalMsg)
			return
		}
		d.llmDisambiguate(ctx, msgCtx, originalMsg)
	}
}

// handleSpotifyLink processes Spotify links.
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

// handleNonSpotifyLink processes non-Spotify music links by resolving them to Spotify tracks.
func (d *Dispatcher) handleNonSpotifyLink(ctx context.Context, msgCtx *MessageContext, originalMsg *chat.Message) {
	if len(msgCtx.Input.URLs) == 0 {
		d.logger.Debug("No URLs found in non-Spotify link message")
		d.askWhichSong(ctx, msgCtx, originalMsg)
		return
	}

	// Try to resolve the first URL (handle one link at a time).
	linkURL := msgCtx.Input.URLs[0]

	// If no music link manager is available, fall back to AI disambiguation.
	if d.musicLinkMgr == nil {
		d.logger.Debug("No music link manager available, falling back to AI disambiguation")
		d.askWhichSong(ctx, msgCtx, originalMsg)
		return
	}

	// Check if we can resolve this link.
	if !d.musicLinkMgr.CanResolve(linkURL) {
		d.logger.Debug("Music link manager cannot resolve this URL, falling back to AI disambiguation",
			zap.String("url", linkURL))
		d.askWhichSong(ctx, msgCtx, originalMsg)
		return
	}

	// Resolve the link to track information.
	trackInfo, err := d.musicLinkMgr.Resolve(ctx, linkURL)
	if err != nil {
		d.logger.Warn("Failed to resolve music link, falling back to AI disambiguation",
			zap.String("url", linkURL),
			zap.Error(err))
		d.askWhichSong(ctx, msgCtx, originalMsg)
		return
	}

	// Try to find the track on Spotify.
	trackID, err := d.searchSpotifyForTrack(ctx, trackInfo)
	if err != nil {
		d.logger.Warn("Failed to find track on Spotify, falling back to AI disambiguation",
			zap.String("url", linkURL),
			zap.String("title", trackInfo.Title),
			zap.String("artist", trackInfo.Artist),
			zap.Error(err))
		d.askWhichSong(ctx, msgCtx, originalMsg)
		return
	}

	// Check for duplicates.
	if d.dedup.Has(trackID) {
		d.reactDuplicate(ctx, msgCtx, originalMsg)
		return
	}

	// Add to playlist.
	d.addToPlaylist(ctx, msgCtx, originalMsg, trackID)
}

// searchSpotifyForTrack searches for a track on Spotify using the provided track information.
func (d *Dispatcher) searchSpotifyForTrack(ctx context.Context, trackInfo *MusicLinkTrackInfo) (string, error) {
	// If we have an ISRC, use that for exact matching.
	if trackInfo.ISRC != "" {
		// Check if the Spotify client supports ISRC search.
		if spotifyClient, ok := d.spotify.(interface {
			SearchTrackByISRC(ctx context.Context, isrc string) (*Track, error)
		}); ok {
			track, err := spotifyClient.SearchTrackByISRC(ctx, trackInfo.ISRC)
			if err == nil && track != nil {
				d.logger.Info("Found track on Spotify using ISRC",
					zap.String("isrc", trackInfo.ISRC),
					zap.String("trackID", track.ID),
					zap.String("title", track.Title),
					zap.String("artist", track.Artist))
				return track.ID, nil
			}
			d.logger.Debug("ISRC search failed, falling back to title/artist search",
				zap.String("isrc", trackInfo.ISRC),
				zap.Error(err))
		}
	}

	// Fall back to title/artist search.
	if trackInfo.Title == "" {
		return "", fmt.Errorf("no title available for search")
	}

	// Check if the Spotify client supports title/artist search.
	if spotifyClient, ok := d.spotify.(interface {
		SearchTrackByTitleArtist(ctx context.Context, title, artist string) (*Track, error)
	}); ok {
		track, err := spotifyClient.SearchTrackByTitleArtist(ctx, trackInfo.Title, trackInfo.Artist)
		if err != nil {
			return "", fmt.Errorf("title/artist search failed: %w", err)
		}
		if track == nil {
			return "", fmt.Errorf("no track found for title/artist")
		}

		d.logger.Info("Found track on Spotify using title/artist",
			zap.String("title", trackInfo.Title),
			zap.String("artist", trackInfo.Artist),
			zap.String("trackID", track.ID),
			zap.String("foundTitle", track.Title),
			zap.String("foundArtist", track.Artist))

		return track.ID, nil
	}

	return "", fmt.Errorf("Spotify client does not support enhanced search methods")
}

// cleanupContext removes message context from memory.
func (d *Dispatcher) cleanupContext(messageID string) {
	d.contextMutex.Lock()
	delete(d.messageContexts, messageID)
	d.contextMutex.Unlock()
}

// isNotMusicRequest checks if a message is chatter that should be filtered out.
func (d *Dispatcher) isNotMusicRequest(ctx context.Context, text string) bool {
	// If no LLM provider is available, always return false (let everything through)
	if d.llm == nil {
		return false
	}

	// Use LLM to determine if this is chatter
	isNotMusicRequest, err := d.llm.IsNotMusicRequest(ctx, text)
	if err != nil {
		d.logger.Warn("LLM chatter detection failed, defaulting to false (letting message through)",
			zap.Error(err),
			zap.String("text", text))
		return false
	}

	return isNotMusicRequest
}

// isHelpRequest checks if a message is asking for help or instructions.
func (d *Dispatcher) isHelpRequest(ctx context.Context, text string) bool {
	// If no LLM provider is available, always return false
	if d.llm == nil {
		return false
	}

	// Use LLM to determine if this is a help request
	isHelpRequest, err := d.llm.IsHelpRequest(ctx, text)
	if err != nil {
		d.logger.Warn("LLM help request detection failed, defaulting to false",
			zap.Error(err),
			zap.String("text", text))
		return false
	}

	return isHelpRequest
}

const (
	playbackSettingsCheckInterval = 30 * time.Second // Check playback settings every 30 seconds
	adminPermissionsCheckInterval = 60 * time.Second // Check admin permissions every 60 seconds
	maxPlaylistTracksToQueue      = 10               // Maximum playlist tracks to queue at once
)
