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

// Dispatcher handles messages from any chat frontend using the unified interface.
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
}

// NewDispatcher creates a new dispatcher with the provided chat frontend.
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
		warningManager:          NewAdminWarningManager(frontend, logger),
		messageContexts:         make(map[string]*MessageContext),
		pendingApprovalMessages: make(map[string]*queueApprovalContext),
		queueManagementFlows:    make(map[string]*QueueManagementFlow),
		shadowQueue:             make([]ShadowQueueItem, 0),
		lastShadowQueueModified: time.Now(),
		lastSuccessfulSync:      time.Now(),
		priorityTracks:          make(map[string]PriorityTrackInfo),
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

const (
	playbackSettingsCheckInterval = 30 * time.Second // Check playback settings every 30 seconds
	adminPermissionsCheckInterval = 60 * time.Second // Check admin permissions every 60 seconds
	maxPlaylistTracksToQueue      = 10               // Maximum playlist tracks to queue at once
)
