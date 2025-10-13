package core

import (
	"context"
	"fmt"
	"sync"
	"time"

	"go.uber.org/zap"

	"whatdj/internal/chat"
)

const (
	thumbsUpReaction   = chat.ReactionThumbsUp
	thumbsDownReaction = chat.ReactionThumbsDown
)

// Dispatcher handles messages from any chat frontend using the unified interface
type Dispatcher struct {
	config   *Config
	frontend chat.Frontend
	spotify  SpotifyClient
	llm      LLMProvider
	dedup    DedupStore
	logger   *zap.Logger

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

	// Begin listening for messages
	return d.frontend.Listen(ctx, d.handleMessage)
}

// Stop gracefully shuts down the dispatcher
func (d *Dispatcher) Stop(_ context.Context) error {
	d.logger.Info("Stopping message dispatcher")
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

	switch msgCtx.Input.Type {
	case MessageTypeSpotifyLink:
		d.handleSpotifyLink(ctx, msgCtx, originalMsg)
	case MessageTypeNonSpotifyLink:
		d.askWhichSong(ctx, msgCtx, originalMsg)
	case MessageTypeFreeText:
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
		d.replyError(ctx, msgCtx, originalMsg, "Couldn't extract Spotify track ID from the link")
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

	_, err := d.frontend.SendText(ctx, originalMsg.ChatID, originalMsg.ID, "Which song do you mean by that?")
	if err != nil {
		d.logger.Error("Failed to ask which song", zap.Error(err))
	}
}

// llmDisambiguate uses LLM to disambiguate song requests
func (d *Dispatcher) llmDisambiguate(ctx context.Context, msgCtx *MessageContext, originalMsg *chat.Message) {
	msgCtx.State = StateLLMDisambiguate

	if d.llm == nil {
		d.replyError(ctx, msgCtx, originalMsg, "I couldn't guess. Could you send me a spotify link to the song?")
		return
	}

	candidates, err := d.llm.RankCandidates(ctx, msgCtx.Input.Text)
	if err != nil {
		d.logger.Error("LLM disambiguation failed", zap.Error(err))
		d.replyError(ctx, msgCtx, originalMsg, "I couldn't guess. Could you type the song and artist?")
		return
	}

	if len(candidates) == 0 {
		d.replyError(ctx, msgCtx, originalMsg, "I couldn't guess. Could you type the song and artist?")
		return
	}

	msgCtx.Candidates = candidates
	best := candidates[0]

	if best.Confidence >= d.config.LLM.Threshold {
		d.promptApproval(ctx, msgCtx, originalMsg, &best)
	} else {
		d.clarifyAsk(ctx, msgCtx, originalMsg, &best)
	}
}

// promptApproval asks for user approval with high confidence
func (d *Dispatcher) promptApproval(ctx context.Context, msgCtx *MessageContext, originalMsg *chat.Message, candidate *LLMCandidate) {
	msgCtx.State = StateConfirmationPrompt

	prompt := fmt.Sprintf("Did you mean **%s - %s** (%d)?",
		candidate.Track.Artist, candidate.Track.Title, candidate.Track.Year)

	approved, err := d.frontend.AwaitApproval(ctx, originalMsg, prompt, d.config.App.ConfirmTimeoutSecs)
	if err != nil {
		d.logger.Error("Failed to get approval", zap.Error(err))
		d.replyError(ctx, msgCtx, originalMsg, "Something went wrong. Please try again.")
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

	prompt := fmt.Sprintf("Did you mean **%s - %s**? If not, please clarify.", candidate.Track.Artist, candidate.Track.Title)

	approved, err := d.frontend.AwaitApproval(ctx, originalMsg, prompt, d.config.App.ConfirmTimeoutSecs)
	if err != nil {
		d.logger.Error("Failed to get clarification", zap.Error(err))
		d.replyError(ctx, msgCtx, originalMsg, "Something went wrong. Please try again.")
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
		d.replyError(ctx, msgCtx, originalMsg, "Something went wrong. Please try again.")
		return
	}

	best := msgCtx.Candidates[0]

	tracks, err := d.spotify.SearchTrack(ctx, fmt.Sprintf("%s %s", best.Track.Artist, best.Track.Title))
	if err != nil || len(tracks) == 0 {
		d.replyError(ctx, msgCtx, originalMsg, "Couldn't find on Spotify—mind clarifying?")
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
	msgCtx.State = StateAddToPlaylist
	msgCtx.SelectedID = trackID

	for retry := 0; retry < d.config.App.MaxRetries; retry++ {
		if err := d.spotify.AddToPlaylist(ctx, d.config.Spotify.PlaylistID, trackID); err != nil {
			d.logger.Error("Failed to add to playlist",
				zap.String("trackID", trackID),
				zap.Int("retry", retry),
				zap.Error(err))

			if retry == d.config.App.MaxRetries-1 {
				d.reactError(ctx, msgCtx, originalMsg, "Failed to add track to playlist")
				return
			}

			time.Sleep(time.Duration(d.config.App.RetryDelaySecs) * time.Second)
			continue
		}

		d.dedup.Add(trackID)
		d.reactAdded(ctx, msgCtx, originalMsg, trackID)
		return
	}
}

// reactAdded reacts to successfully added tracks
func (d *Dispatcher) reactAdded(ctx context.Context, msgCtx *MessageContext, originalMsg *chat.Message, trackID string) {
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

	// Reply with track info
	replyText := fmt.Sprintf("Added: %s - %s (%s)", track.Artist, track.Title, track.URL)
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
	if _, err := d.frontend.SendText(ctx, originalMsg.ChatID, originalMsg.ID, "Already in playlist."); err != nil {
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
