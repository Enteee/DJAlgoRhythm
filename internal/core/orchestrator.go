package core

import (
	"context"
	"fmt"
	"sync"
	"time"

	"go.uber.org/zap"
)

const (
	thumbsUpReaction   = "👍"
	thumbsDownReaction = "👎"
)

type Orchestrator struct {
	config   *Config
	whatsapp WhatsAppClient
	spotify  SpotifyClient
	llm      LLMProvider
	dedup    DedupStore
	logger   *zap.Logger

	messageContexts map[string]*MessageContext
	contextMutex    sync.RWMutex
}

func NewOrchestrator(
	config *Config,
	whatsapp WhatsAppClient,
	spotify SpotifyClient,
	llm LLMProvider,
	dedup DedupStore,
	logger *zap.Logger,
) *Orchestrator {
	return &Orchestrator{
		config:          config,
		whatsapp:        whatsapp,
		spotify:         spotify,
		llm:             llm,
		dedup:           dedup,
		logger:          logger,
		messageContexts: make(map[string]*MessageContext),
	}
}

func (o *Orchestrator) Start(ctx context.Context) error {
	o.logger.Info("Starting orchestrator")

	if err := o.loadPlaylistSnapshot(ctx); err != nil {
		o.logger.Warn("Failed to load playlist snapshot", zap.Error(err))
	}

	o.whatsapp.SetMessageHandler(o.handleMessage)
	o.whatsapp.SetReactionHandler(o.handleReaction)

	return o.whatsapp.Start(ctx)
}

func (o *Orchestrator) Stop(ctx context.Context) error {
	o.logger.Info("Stopping orchestrator")
	return o.whatsapp.Stop(ctx)
}

func (o *Orchestrator) handleMessage(msg *InputMessage) {
	ctx := context.Background()

	if msg.GroupJID != o.config.WhatsApp.GroupJID {
		return
	}

	msgCtx := &MessageContext{
		Input:     *msg,
		State:     StateDispatch,
		StartTime: time.Now(),
		TimeoutAt: time.Now().Add(time.Duration(o.config.App.ConfirmTimeoutSecs) * time.Second),
	}

	o.contextMutex.Lock()
	o.messageContexts[msg.MessageID] = msgCtx
	o.contextMutex.Unlock()

	go o.processMessage(ctx, msgCtx)
}

func (o *Orchestrator) handleReaction(groupJID, senderJID, messageID, reaction string) {
	if groupJID != o.config.WhatsApp.GroupJID {
		return
	}

	o.contextMutex.RLock()
	msgCtx, exists := o.messageContexts[messageID]
	o.contextMutex.RUnlock()

	if !exists || msgCtx.State != StateWaitThumbs {
		return
	}

	if senderJID != msgCtx.Input.SenderJID {
		return
	}

	ctx := context.Background()

	if reaction == thumbsUpReaction && time.Now().Before(msgCtx.TimeoutAt) {
		go o.handleThumbsUp(ctx, msgCtx)
	} else {
		go o.handleThumbsDown(ctx, msgCtx)
	}
}

func (o *Orchestrator) processMessage(ctx context.Context, msgCtx *MessageContext) {
	defer o.cleanupContext(msgCtx.Input.MessageID)

	o.logger.Debug("Processing message",
		zap.String("messageID", msgCtx.Input.MessageID),
		zap.String("sender", msgCtx.Input.SenderJID),
		zap.String("text", msgCtx.Input.Text),
	)

	switch msgCtx.Input.Type {
	case MessageTypeSpotifyLink:
		o.handleSpotifyLink(ctx, msgCtx)
	case MessageTypeNonSpotifyLink:
		o.askWhichSong(ctx, msgCtx)
	case MessageTypeFreeText:
		o.llmDisambiguate(ctx, msgCtx)
	}
}

func (o *Orchestrator) handleSpotifyLink(ctx context.Context, msgCtx *MessageContext) {
	msgCtx.State = StateHandleSpotifyLink

	var trackID string
	var err error

	for _, url := range msgCtx.Input.URLs {
		if trackID, err = o.spotify.ExtractTrackID(url); err == nil && trackID != "" {
			break
		}
	}

	if trackID == "" {
		o.replyError(ctx, msgCtx, "Couldn't extract Spotify track ID from the link")
		return
	}

	if o.dedup.Has(trackID) {
		o.reactDuplicate(ctx, msgCtx)
		return
	}

	o.addToPlaylist(ctx, msgCtx, trackID)
}

func (o *Orchestrator) askWhichSong(ctx context.Context, msgCtx *MessageContext) {
	msgCtx.State = StateAskWhichSong

	if err := o.whatsapp.ReplyToMessage(ctx, msgCtx.Input.GroupJID, msgCtx.Input.MessageID, "Which song do you mean by that?"); err != nil {
		o.logger.Error("Failed to ask which song", zap.Error(err))
	}
}

func (o *Orchestrator) llmDisambiguate(ctx context.Context, msgCtx *MessageContext) {
	msgCtx.State = StateLLMDisambiguate

	if o.llm == nil {
		o.replyError(ctx, msgCtx, "I couldn't guess. Could you type the song and artist?")
		return
	}

	candidates, err := o.llm.RankCandidates(ctx, msgCtx.Input.Text)
	if err != nil {
		o.logger.Error("LLM disambiguation failed", zap.Error(err))
		o.replyError(ctx, msgCtx, "I couldn't guess. Could you type the song and artist?")
		return
	}

	if len(candidates) == 0 {
		o.replyError(ctx, msgCtx, "I couldn't guess. Could you type the song and artist?")
		return
	}

	msgCtx.Candidates = candidates
	best := candidates[0]

	if best.Confidence >= o.config.LLM.Threshold {
		o.promptThumbsUp(ctx, msgCtx, &best)
	} else {
		o.clarifyAsk(ctx, msgCtx, &best)
	}
}

func (o *Orchestrator) promptThumbsUp(ctx context.Context, msgCtx *MessageContext, candidate *LLMCandidate) {
	msgCtx.State = StateConfirmationPrompt

	text := fmt.Sprintf("Did you mean %s - %s (%d)? React %s to confirm.",
		candidate.Track.Artist, candidate.Track.Title, candidate.Track.Year, thumbsUpReaction)

	if err := o.whatsapp.ReplyToMessage(ctx, msgCtx.Input.GroupJID, msgCtx.Input.MessageID, text); err != nil {
		o.logger.Error("Failed to prompt thumbs up", zap.Error(err))
		return
	}

	msgCtx.State = StateWaitThumbs
}

func (o *Orchestrator) clarifyAsk(ctx context.Context, msgCtx *MessageContext, candidate *LLMCandidate) {
	msgCtx.State = StateClarifyAsk

	text := fmt.Sprintf("Did you mean \"%s - %s\"? If yes, react %s; otherwise reply with the correct name.",
		candidate.Track.Artist, candidate.Track.Title, thumbsUpReaction)

	if err := o.whatsapp.ReplyToMessage(ctx, msgCtx.Input.GroupJID, msgCtx.Input.MessageID, text); err != nil {
		o.logger.Error("Failed to clarify ask", zap.Error(err))
		return
	}

	msgCtx.State = StateWaitThumbs
}

func (o *Orchestrator) handleThumbsUp(ctx context.Context, msgCtx *MessageContext) {
	if len(msgCtx.Candidates) == 0 {
		o.replyError(ctx, msgCtx, "Something went wrong. Please try again.")
		return
	}

	best := msgCtx.Candidates[0]

	tracks, err := o.spotify.SearchTrack(ctx, fmt.Sprintf("%s %s", best.Track.Artist, best.Track.Title))
	if err != nil || len(tracks) == 0 {
		o.replyError(ctx, msgCtx, "Couldn't find on Spotify—mind clarifying?")
		return
	}

	trackID := tracks[0].ID
	if o.dedup.Has(trackID) {
		o.reactDuplicate(ctx, msgCtx)
		return
	}

	o.addToPlaylist(ctx, msgCtx, trackID)
}

func (o *Orchestrator) handleThumbsDown(ctx context.Context, msgCtx *MessageContext) {
	o.clarifyAsk(ctx, msgCtx, &msgCtx.Candidates[0])
}

func (o *Orchestrator) addToPlaylist(ctx context.Context, msgCtx *MessageContext, trackID string) {
	msgCtx.State = StateAddToPlaylist
	msgCtx.SelectedID = trackID

	for retry := 0; retry < o.config.App.MaxRetries; retry++ {
		if err := o.spotify.AddToPlaylist(ctx, o.config.Spotify.PlaylistID, trackID); err != nil {
			o.logger.Error("Failed to add to playlist",
				zap.String("trackID", trackID),
				zap.Int("retry", retry),
				zap.Error(err))

			if retry == o.config.App.MaxRetries-1 {
				o.reactError(ctx, msgCtx, "Failed to add track to playlist")
				return
			}

			time.Sleep(time.Duration(o.config.App.RetryDelaySecs) * time.Second)
			continue
		}

		o.dedup.Add(trackID)
		o.reactAdded(ctx, msgCtx, trackID)
		return
	}
}

func (o *Orchestrator) reactAdded(ctx context.Context, msgCtx *MessageContext, trackID string) {
	msgCtx.State = StateReactAdded

	track, err := o.spotify.GetTrack(ctx, trackID)
	if err != nil {
		o.logger.Error("Failed to get track info", zap.Error(err))
		track = &Track{ID: trackID, Title: "Unknown", Artist: "Unknown"}
	}

	if err := o.whatsapp.ReactToMessage(
		ctx, msgCtx.Input.GroupJID, msgCtx.Input.SenderJID, msgCtx.Input.MessageID, thumbsUpReaction); err != nil {
		o.logger.Error("Failed to react with thumbs up", zap.Error(err))
	}

	replyText := fmt.Sprintf("Added: %s - %s (%s)", track.Artist, track.Title, track.URL)
	if err := o.whatsapp.ReplyToMessage(ctx, msgCtx.Input.GroupJID, msgCtx.Input.MessageID, replyText); err != nil {
		o.logger.Error("Failed to reply with added confirmation", zap.Error(err))
	}
}

func (o *Orchestrator) reactDuplicate(ctx context.Context, msgCtx *MessageContext) {
	msgCtx.State = StateReactDuplicate

	if err := o.whatsapp.ReactToMessage(
		ctx, msgCtx.Input.GroupJID, msgCtx.Input.SenderJID, msgCtx.Input.MessageID, thumbsDownReaction); err != nil {
		o.logger.Error("Failed to react with thumbs down", zap.Error(err))
	}

	if err := o.whatsapp.ReplyToMessage(ctx, msgCtx.Input.GroupJID, msgCtx.Input.MessageID, "Already in playlist."); err != nil {
		o.logger.Error("Failed to reply with duplicate message", zap.Error(err))
	}
}

func (o *Orchestrator) reactError(ctx context.Context, msgCtx *MessageContext, message string) {
	msgCtx.State = StateReactError

	if err := o.whatsapp.ReplyToMessage(ctx, msgCtx.Input.GroupJID, msgCtx.Input.MessageID, message); err != nil {
		o.logger.Error("Failed to reply with error message", zap.Error(err))
	}
}

func (o *Orchestrator) replyError(ctx context.Context, msgCtx *MessageContext, message string) {
	o.reactError(ctx, msgCtx, message)
}

func (o *Orchestrator) loadPlaylistSnapshot(ctx context.Context) error {
	trackIDs, err := o.spotify.GetPlaylistTracks(ctx, o.config.Spotify.PlaylistID)
	if err != nil {
		return err
	}

	o.dedup.Load(trackIDs)
	o.logger.Info("Loaded playlist snapshot", zap.Int("tracks", len(trackIDs)))
	return nil
}

func (o *Orchestrator) cleanupContext(messageID string) {
	o.contextMutex.Lock()
	delete(o.messageContexts, messageID)
	o.contextMutex.Unlock()
}
