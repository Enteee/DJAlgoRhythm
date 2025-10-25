package core

import (
	"context"
	"time"
)

type MessageType int

const (
	// MessageTypeSpotifyLink represents a message containing a Spotify track URL
	MessageTypeSpotifyLink MessageType = iota
	// MessageTypeNonSpotifyLink represents a message containing a non-Spotify music URL
	MessageTypeNonSpotifyLink
	// MessageTypeFreeText represents a free-form text message about music
	MessageTypeFreeText
)

type InputMessage struct {
	Type      MessageType
	Text      string
	URLs      []string
	GroupJID  string
	SenderJID string
	MessageID string
	Timestamp time.Time
}

type Track struct {
	ID       string
	Title    string
	Artist   string
	Album    string
	Year     int
	Duration time.Duration
	URL      string
}

type Playlist struct {
	ID          string
	Name        string
	Description string
	TrackCount  int
	Owner       string
}

type PlaybackCompliance struct {
	IsCorrectShuffle bool
	IsCorrectRepeat  bool
	Issues           []string
}

// IsOptimalForAutoDJ returns true if all settings are optimal for auto-DJing
func (pc PlaybackCompliance) IsOptimalForAutoDJ() bool {
	return pc.IsCorrectShuffle && pc.IsCorrectRepeat
}

type MessageState int

const (
	// StateReady indicates the orchestrator is ready to process messages
	StateReady MessageState = iota
	// StateDispatch indicates message is being dispatched for processing
	StateDispatch
	// StateHandleSpotifyLink indicates processing a Spotify link
	StateHandleSpotifyLink
	// StateAskWhichSong indicates asking user for song clarification
	StateAskWhichSong
	// StateLLMDisambiguate indicates using LLM for song disambiguation
	StateLLMDisambiguate
	// StateEnhancedLLMDisambiguate indicates using enhanced LLM disambiguation with Spotify search
	StateEnhancedLLMDisambiguate
	// StateConfirmationPrompt indicates waiting for user confirmation
	StateConfirmationPrompt
	// StateWaitThumbs indicates waiting for thumbs up/down reaction
	StateWaitThumbs
	// StateWaitReply indicates waiting for user reply
	StateWaitReply
	// StateAwaitAdminApproval indicates waiting for admin approval
	StateAwaitAdminApproval
	// StateAddToPlaylist indicates adding track to playlist
	StateAddToPlaylist
	// StateReactAdded indicates reacting to successfully added track
	StateReactAdded
	// StateReactDuplicate indicates reacting to duplicate track
	StateReactDuplicate
	// StateReactError indicates reacting to error condition
	StateReactError
	// StateClarifyAsk indicates asking for clarification
	StateClarifyAsk
	// StateGiveUp indicates giving up on processing the message
	StateGiveUp
)

type MessageContext struct {
	Input      InputMessage
	State      MessageState
	Candidates []Track
	SelectedID string
	Error      error
	RetryCount int
	StartTime  time.Time
	TimeoutAt  time.Time
	IsPriority bool
	TrackMood  string
}

type WhatsAppClient interface {
	SendMessage(ctx context.Context, groupJID, text string) error
	ReplyToMessage(ctx context.Context, groupJID, messageID, text string) error
	ReactToMessage(ctx context.Context, groupJID, senderJID, messageID, reaction string) error
	Start(ctx context.Context) error
	Stop(ctx context.Context) error
	SetMessageHandler(handler func(*InputMessage))
	SetReactionHandler(handler func(groupJID, senderJID, messageID, reaction string))
}

type SpotifyClient interface {
	SearchTrack(ctx context.Context, query string) ([]Track, error)
	GetTrack(ctx context.Context, trackID string) (*Track, error)
	AddToPlaylist(ctx context.Context, playlistID, trackID string) error
	AddToPlaylistAtPosition(ctx context.Context, playlistID, trackID string, position int) error
	AddToQueue(ctx context.Context, trackID string) error
	GetPlaylistTracksWithDetails(ctx context.Context, playlistID string) ([]Track, error)
	GetQueueTrackIDs(ctx context.Context) ([]string, error)
	GetCurrentTrackID(ctx context.Context) (string, error)
	ExtractTrackID(url string) (string, error)
	SetTargetPlaylist(playlistID string)
	GetNextPlaylistTracks(ctx context.Context, count int) ([]Track, error)
	GetNextPlaylistTracksFromPosition(ctx context.Context, startPosition, count int) ([]Track, error)
	GetRecommendedTrack(ctx context.Context) (trackID, searchQuery, newTrackMood string, err error)
	CheckPlaybackCompliance(ctx context.Context) (*PlaybackCompliance, error)
	SetShuffle(ctx context.Context, shuffle bool) error
	SetRepeat(ctx context.Context, state string) error
	GetCurrentTrackRemainingTime(ctx context.Context) (time.Duration, error)
	HasActiveDevice(ctx context.Context) (bool, error)
}

type LLMProvider interface {
	RankTracks(ctx context.Context, searchQuery string, tracks []Track) []Track
	IsNotMusicRequest(ctx context.Context, text string) (bool, error)
	IsPriorityRequest(ctx context.Context, text string) (bool, error)
	GenerateTrackMood(ctx context.Context, tracks []Track) (string, error)
	ExtractSongQuery(ctx context.Context, userText string) (string, error)
}

type DedupStore interface {
	Has(trackID string) bool
	Add(trackID string)
	Remove(trackID string)
	Load(trackIDs []string)
	Size() int
	Clear()
}
