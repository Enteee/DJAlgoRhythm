package core

import (
	"context"
	"fmt"
	"testing"
	"time"

	"go.uber.org/zap"
)

// Mock implementations for testing

type mockWhatsAppClient struct {
	sentMessages    []string
	sentReactions   []string
	messageHandler  func(*InputMessage)
	reactionHandler func(groupJID, senderJID, messageID, reaction string)
}

func (m *mockWhatsAppClient) SendMessage(_ context.Context, _, text string) error {
	m.sentMessages = append(m.sentMessages, text)
	return nil
}

func (m *mockWhatsAppClient) ReplyToMessage(_ context.Context, _, _, text string) error {
	m.sentMessages = append(m.sentMessages, text)
	return nil
}

func (m *mockWhatsAppClient) ReactToMessage(_ context.Context, _, _, _, reaction string) error {
	m.sentReactions = append(m.sentReactions, reaction)
	return nil
}

func (m *mockWhatsAppClient) Start(_ context.Context) error {
	return nil
}

func (m *mockWhatsAppClient) Stop(_ context.Context) error {
	return nil
}

func (m *mockWhatsAppClient) SetMessageHandler(handler func(*InputMessage)) {
	m.messageHandler = handler
}

func (m *mockWhatsAppClient) SetReactionHandler(handler func(groupJID, senderJID, messageID, reaction string)) {
	m.reactionHandler = handler
}

type mockSpotifyClient struct {
	tracks         map[string]*Track
	playlistTracks []string
	addedTracks    []string
	searchResults  map[string][]Track
}

func (m *mockSpotifyClient) SearchTrack(_ context.Context, query string) ([]Track, error) {
	if results, exists := m.searchResults[query]; exists {
		return results, nil
	}
	return []Track{}, nil
}

func (m *mockSpotifyClient) GetTrack(_ context.Context, trackID string) (*Track, error) {
	if track, exists := m.tracks[trackID]; exists {
		return track, nil
	}
	return &Track{ID: trackID, Title: "Unknown", Artist: "Unknown"}, nil
}

func (m *mockSpotifyClient) AddToPlaylist(_ context.Context, _, trackID string) error {
	m.addedTracks = append(m.addedTracks, trackID)
	return nil
}

func (m *mockSpotifyClient) GetPlaylistTracks(_ context.Context, _ string) ([]string, error) {
	return m.playlistTracks, nil
}

func (m *mockSpotifyClient) ExtractTrackID(url string) (string, error) {
	if url == "https://open.spotify.com/track/test123" {
		return "test123", nil
	}
	return "", nil
}

type mockLLMProvider struct {
	candidates []LLMCandidate
}

func (m *mockLLMProvider) RankCandidates(_ context.Context, _ string) ([]LLMCandidate, error) {
	return m.candidates, nil
}

func (m *mockLLMProvider) ExtractSongInfo(_ context.Context, _ string) (*Track, error) {
	if len(m.candidates) > 0 {
		return &m.candidates[0].Track, nil
	}
	return nil, nil
}

type mockDedupStore struct {
	tracks map[string]bool
}

func (m *mockDedupStore) Has(trackID string) bool {
	return m.tracks[trackID]
}

func (m *mockDedupStore) Add(trackID string) {
	m.tracks[trackID] = true
}

func (m *mockDedupStore) Load(trackIDs []string) {
	m.tracks = make(map[string]bool)
	for _, id := range trackIDs {
		m.tracks[id] = true
	}
}

func (m *mockDedupStore) Size() int {
	return len(m.tracks)
}

func (m *mockDedupStore) Clear() {
	m.tracks = make(map[string]bool)
}

func TestOrchestrator_HandleSpotifyLink(t *testing.T) {
	// Setup mocks
	whatsapp := &mockWhatsAppClient{}
	spotify := &mockSpotifyClient{
		tracks: map[string]*Track{
			"test123": {
				ID:     "test123",
				Title:  "Test Song",
				Artist: "Test Artist",
				URL:    "https://open.spotify.com/track/test123",
			},
		},
	}
	dedup := &mockDedupStore{tracks: make(map[string]bool)}
	logger := zap.NewNop()

	config := &Config{
		WhatsApp: WhatsAppConfig{GroupJID: "test-group"},
		App:      AppConfig{MaxRetries: 3},
	}

	orchestrator := NewOrchestrator(config, whatsapp, spotify, nil, dedup, logger)

	// Test case 1: New Spotify track
	msg := InputMessage{
		Type:      MessageTypeSpotifyLink,
		Text:      "Check this out: https://open.spotify.com/track/test123",
		URLs:      []string{"https://open.spotify.com/track/test123"},
		GroupJID:  "test-group",
		SenderJID: "sender123",
		MessageID: "msg123",
		Timestamp: time.Now(),
	}

	orchestrator.handleMessage(&msg)

	// Give it a moment to process
	time.Sleep(100 * time.Millisecond)

	// Verify track was added
	if len(spotify.addedTracks) != 1 {
		t.Errorf("Expected 1 track to be added, got %d", len(spotify.addedTracks))
	}

	if spotify.addedTracks[0] != "test123" {
		t.Errorf("Expected track test123 to be added, got %s", spotify.addedTracks[0])
	}

	// Verify positive reaction
	if len(whatsapp.sentReactions) != 1 || whatsapp.sentReactions[0] != "👍" {
		t.Errorf("Expected thumbs up reaction, got %v", whatsapp.sentReactions)
	}

	// Verify dedup store was updated
	if !dedup.Has("test123") {
		t.Error("Expected track to be added to dedup store")
	}
}

func TestOrchestrator_HandleDuplicate(t *testing.T) {
	// Setup mocks
	whatsapp := &mockWhatsAppClient{}
	spotify := &mockSpotifyClient{}
	dedup := &mockDedupStore{tracks: map[string]bool{"test123": true}}
	logger := zap.NewNop()

	config := &Config{
		WhatsApp: WhatsAppConfig{GroupJID: "test-group"},
		App:      AppConfig{MaxRetries: 3},
	}

	orchestrator := NewOrchestrator(config, whatsapp, spotify, nil, dedup, logger)

	// Test duplicate track
	msg := InputMessage{
		Type:      MessageTypeSpotifyLink,
		Text:      "https://open.spotify.com/track/test123",
		URLs:      []string{"https://open.spotify.com/track/test123"},
		GroupJID:  "test-group",
		SenderJID: "sender123",
		MessageID: "msg123",
		Timestamp: time.Now(),
	}

	orchestrator.handleMessage(&msg)

	// Give it a moment to process
	time.Sleep(100 * time.Millisecond)

	// Verify track was NOT added
	if len(spotify.addedTracks) != 0 {
		t.Errorf("Expected 0 tracks to be added for duplicate, got %d", len(spotify.addedTracks))
	}

	// Verify negative reaction
	if len(whatsapp.sentReactions) != 1 || whatsapp.sentReactions[0] != "👎" {
		t.Errorf("Expected thumbs down reaction for duplicate, got %v", whatsapp.sentReactions)
	}

	// Verify duplicate message was sent
	if len(whatsapp.sentMessages) != 1 || whatsapp.sentMessages[0] != "Already in playlist." {
		t.Errorf("Expected duplicate message, got %v", whatsapp.sentMessages)
	}
}

func TestOrchestrator_HandleLLMDisambiguation(t *testing.T) {
	// Setup mocks
	whatsapp := &mockWhatsAppClient{}
	spotify := &mockSpotifyClient{
		searchResults: map[string][]Track{
			"test artist test song": {
				{ID: "found123", Title: "Test Song", Artist: "Test Artist"},
			},
		},
	}
	llm := &mockLLMProvider{
		candidates: []LLMCandidate{
			{
				Track:      Track{Title: "Test Song", Artist: "Test Artist", Year: 2023},
				Confidence: 0.9,
				Reasoning:  "High confidence match",
			},
		},
	}
	dedup := &mockDedupStore{tracks: make(map[string]bool)}
	logger := zap.NewNop()

	config := &Config{
		WhatsApp: WhatsAppConfig{GroupJID: "test-group"},
		LLM:      LLMConfig{Threshold: 0.8},
		App:      AppConfig{MaxRetries: 3, ConfirmTimeoutSecs: 120},
	}

	orchestrator := NewOrchestrator(config, whatsapp, spotify, llm, dedup, logger)

	// Test free text message
	msg := InputMessage{
		Type:      MessageTypeFreeText,
		Text:      "play test song by test artist",
		GroupJID:  "test-group",
		SenderJID: "sender123",
		MessageID: "msg123",
		Timestamp: time.Now(),
	}

	orchestrator.handleMessage(&msg)

	// Give it a moment to process
	time.Sleep(100 * time.Millisecond)

	// Verify confirmation prompt was sent
	if len(whatsapp.sentMessages) == 0 {
		t.Error("Expected confirmation message to be sent")
	}

	expectedMsg := "Did you mean Test Artist - Test Song (2023)? React 👍 to confirm."
	if whatsapp.sentMessages[0] != expectedMsg {
		t.Errorf("Expected confirmation message %q, got %q", expectedMsg, whatsapp.sentMessages[0])
	}
}

func TestOrchestrator_HandleNonSpotifyLink(t *testing.T) {
	// Setup mocks
	whatsapp := &mockWhatsAppClient{}
	spotify := &mockSpotifyClient{}
	dedup := &mockDedupStore{tracks: make(map[string]bool)}
	logger := zap.NewNop()

	config := &Config{
		WhatsApp: WhatsAppConfig{GroupJID: "test-group"},
		App:      AppConfig{MaxRetries: 3},
	}

	orchestrator := NewOrchestrator(config, whatsapp, spotify, nil, dedup, logger)

	// Test non-Spotify link
	msg := InputMessage{
		Type:      MessageTypeNonSpotifyLink,
		Text:      "https://www.youtube.com/watch?v=dQw4w9WgXcQ",
		URLs:      []string{"https://www.youtube.com/watch?v=dQw4w9WgXcQ"},
		GroupJID:  "test-group",
		SenderJID: "sender123",
		MessageID: "msg123",
		Timestamp: time.Now(),
	}

	orchestrator.handleMessage(&msg)

	// Give it a moment to process
	time.Sleep(100 * time.Millisecond)

	// Verify clarification message was sent
	if len(whatsapp.sentMessages) != 1 {
		t.Errorf("Expected 1 message to be sent, got %d", len(whatsapp.sentMessages))
	}

	if whatsapp.sentMessages[0] != "Which song do you mean by that?" {
		t.Errorf("Expected clarification message, got %q", whatsapp.sentMessages[0])
	}
}

func TestOrchestrator_IgnoreWrongGroup(t *testing.T) {
	// Setup mocks
	whatsapp := &mockWhatsAppClient{}
	spotify := &mockSpotifyClient{}
	dedup := &mockDedupStore{tracks: make(map[string]bool)}
	logger := zap.NewNop()

	config := &Config{
		WhatsApp: WhatsAppConfig{GroupJID: "correct-group"},
		App:      AppConfig{MaxRetries: 3},
	}

	orchestrator := NewOrchestrator(config, whatsapp, spotify, nil, dedup, logger)

	// Test message from wrong group
	msg := InputMessage{
		Type:      MessageTypeSpotifyLink,
		Text:      "https://open.spotify.com/track/test123",
		GroupJID:  "wrong-group", // Different group
		SenderJID: "sender123",
		MessageID: "msg123",
		Timestamp: time.Now(),
	}

	orchestrator.handleMessage(&msg)

	// Give it a moment to process
	time.Sleep(100 * time.Millisecond)

	// Verify nothing was processed
	if len(spotify.addedTracks) != 0 {
		t.Error("Expected no tracks to be added from wrong group")
	}

	if len(whatsapp.sentMessages) != 0 {
		t.Error("Expected no messages to be sent for wrong group")
	}

	if len(whatsapp.sentReactions) != 0 {
		t.Error("Expected no reactions to be sent for wrong group")
	}
}

func BenchmarkOrchestrator_HandleSpotifyLink(b *testing.B) {
	// Setup mocks
	whatsapp := &mockWhatsAppClient{}
	spotify := &mockSpotifyClient{
		tracks: map[string]*Track{
			"test123": {ID: "test123", Title: "Test Song", Artist: "Test Artist"},
		},
	}
	dedup := &mockDedupStore{tracks: make(map[string]bool)}
	logger := zap.NewNop()

	config := &Config{
		WhatsApp: WhatsAppConfig{GroupJID: "test-group"},
		App:      AppConfig{MaxRetries: 3},
	}

	orchestrator := NewOrchestrator(config, whatsapp, spotify, nil, dedup, logger)

	msg := InputMessage{
		Type:      MessageTypeSpotifyLink,
		Text:      "https://open.spotify.com/track/test123",
		URLs:      []string{"https://open.spotify.com/track/test123"},
		GroupJID:  "test-group",
		SenderJID: "sender123",
		MessageID: "msg123",
		Timestamp: time.Now(),
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		msg.MessageID = fmt.Sprintf("msg%d", i)
		orchestrator.handleMessage(&msg)
	}
}
