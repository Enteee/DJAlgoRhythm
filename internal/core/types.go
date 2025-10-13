package core

import (
	"context"
	"time"
)

type MessageType int

const (
	MessageTypeSpotifyLink MessageType = iota
	MessageTypeNonSpotifyLink
	MessageTypeFreeText
)

type InputMessage struct {
	Type        MessageType
	Text        string
	URLs        []string
	GroupJID    string
	SenderJID   string
	MessageID   string
	Timestamp   time.Time
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

type LLMCandidate struct {
	Track      Track
	Confidence float64
	Reasoning  string
}

type MessageState int

const (
	StateReady MessageState = iota
	StateDispatch
	StateHandleSpotifyLink
	StateAskWhichSong
	StateLLMDisambiguate
	StateConfirmationPrompt
	StateWaitThumbs
	StateWaitReply
	StateAddToPlaylist
	StateReactAdded
	StateReactDuplicate
	StateReactError
	StateClarifyAsk
	StateGiveUp
)

type MessageContext struct {
	Input       InputMessage
	State       MessageState
	Candidates  []LLMCandidate
	SelectedID  string
	Error       error
	RetryCount  int
	StartTime   time.Time
	TimeoutAt   time.Time
}

type WhatsAppClient interface {
	SendMessage(ctx context.Context, groupJID, text string) error
	ReplyToMessage(ctx context.Context, groupJID, messageID, text string) error
	ReactToMessage(ctx context.Context, groupJID, senderJID, messageID, reaction string) error
	Start(ctx context.Context) error
	Stop(ctx context.Context) error
	SetMessageHandler(handler func(InputMessage))
	SetReactionHandler(handler func(groupJID, senderJID, messageID, reaction string))
}

type SpotifyClient interface {
	SearchTrack(ctx context.Context, query string) ([]Track, error)
	GetTrack(ctx context.Context, trackID string) (*Track, error)
	AddToPlaylist(ctx context.Context, playlistID, trackID string) error
	GetPlaylistTracks(ctx context.Context, playlistID string) ([]string, error)
	ExtractTrackID(url string) (string, error)
}

type LLMProvider interface {
	RankCandidates(ctx context.Context, text string) ([]LLMCandidate, error)
	ExtractSongInfo(ctx context.Context, text string) (*Track, error)
}

type DedupStore interface {
	Has(trackID string) bool
	Add(trackID string)
	Load(trackIDs []string)
	Size() int
	Clear()
}