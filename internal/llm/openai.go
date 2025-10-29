// Package llm provides LLM (Large Language Model) integration for music request processing.
package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"strings"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"github.com/openai/openai-go/shared"
	"go.uber.org/zap"

	"djalgorhythm/internal/core"
)

// OpenAIClient implements the LLM provider interface using OpenAI's GPT models.
type OpenAIClient struct {
	config *core.LLMConfig
	logger *zap.Logger
	client *openai.Client
}

const (
	defaultTemperature    = 0.1
	rankingTemperature    = 0.3
	extractionTemperature = 0.1 // Deterministic for extraction
	moodTemperature       = 0.2 // Slightly creative for mood descriptions
	maxTokensRanking      = 1000
	maxTokensChatter      = 200
	maxTokensPriority     = 200
	maxTokensSearchQuery  = 50
	maxTokensTrackRanking = 100
	maxTokensExtraction   = 500 // For song extraction response
	maxTokensMood         = 50  // For track mood generation
	defaultModel          = "gpt-3.5-turbo"
)

// NewOpenAIClient creates a new OpenAI client with the provided configuration.
func NewOpenAIClient(config *core.LLMConfig, logger *zap.Logger) (*OpenAIClient, error) {
	if config.APIKey == "" {
		return nil, errors.New("OpenAI API key is required")
	}

	var opts []option.RequestOption
	opts = append(opts, option.WithAPIKey(config.APIKey))

	if config.BaseURL != "" {
		opts = append(opts, option.WithBaseURL(config.BaseURL))
	}

	client := openai.NewClient(opts...)

	return &OpenAIClient{
		config: config,
		logger: logger,
		client: &client,
	}, nil
}

// ChatterDetectionResponse represents the response from OpenAI for chatter detection.
type ChatterDetectionResponse struct {
	IsNotMusicRequest bool    `json:"is_not_music_request"`
	Confidence        float64 `json:"confidence"`
	Reasoning         string  `json:"reasoning,omitempty"`
}

// IsNotMusicRequest determines if the given text is not a music-related request using OpenAI.
//
//nolint:dupl // Similar structure to IsPriorityRequest but different response types
func (o *OpenAIClient) IsNotMusicRequest(ctx context.Context, text string) (bool, error) {
	if strings.TrimSpace(text) == "" {
		return true, errors.New("empty text provided")
	}

	prompt := o.buildChatterDetectionPrompt()

	o.logger.Debug("Calling OpenAI for chatter detection",
		zap.String("text", text),
		zap.String("model", o.config.Model))

	resp, err := o.client.Chat.Completions.New(ctx, openai.ChatCompletionNewParams{
		Messages: []openai.ChatCompletionMessageParamUnion{
			openai.SystemMessage(prompt),
			openai.UserMessage(text),
		},
		Model:       o.getModel(),
		Temperature: openai.Float(defaultTemperature),
		MaxTokens:   openai.Int(maxTokensChatter),
	})
	if err != nil {
		o.logger.Error("OpenAI API call failed", zap.Error(err))
		return false, fmt.Errorf("OpenAI API call failed: %w", err)
	}

	if len(resp.Choices) == 0 {
		return false, errors.New("no response from OpenAI")
	}

	content := resp.Choices[0].Message.Content
	o.logger.Debug("OpenAI response received", zap.String("content", content))

	var response ChatterDetectionResponse
	if err := json.Unmarshal([]byte(content), &response); err != nil {
		o.logger.Error("Failed to parse OpenAI response",
			zap.Error(err),
			zap.String("content", content))
		return false, fmt.Errorf("failed to parse OpenAI response: %w", err)
	}

	o.logger.Debug("Chatter detection completed",
		zap.Bool("is_not_music_request", response.IsNotMusicRequest),
		zap.Float64("confidence", response.Confidence),
		zap.String("reasoning", response.Reasoning))

	return response.IsNotMusicRequest, nil
}

// PriorityDetectionResponse represents the response from OpenAI for priority detection.
type PriorityDetectionResponse struct {
	IsPriorityRequest bool    `json:"is_priority_request"`
	Confidence        float64 `json:"confidence"`
	Reasoning         string  `json:"reasoning,omitempty"`
}

// IsPriorityRequest determines if the given text represents a priority request using OpenAI.
//
//nolint:dupl // Similar structure to IsNotMusicRequest but different response types
func (o *OpenAIClient) IsPriorityRequest(ctx context.Context, text string) (bool, error) {
	if strings.TrimSpace(text) == "" {
		return false, errors.New("empty text provided")
	}

	prompt := o.buildPriorityDetectionPrompt()

	o.logger.Debug("Calling OpenAI for priority detection",
		zap.String("text", text),
		zap.String("model", o.config.Model))

	resp, err := o.client.Chat.Completions.New(ctx, openai.ChatCompletionNewParams{
		Messages: []openai.ChatCompletionMessageParamUnion{
			openai.SystemMessage(prompt),
			openai.UserMessage(text),
		},
		Model:       o.getModel(),
		Temperature: openai.Float(defaultTemperature),
		MaxTokens:   openai.Int(maxTokensPriority),
	})
	if err != nil {
		o.logger.Error("OpenAI API call failed", zap.Error(err))
		return false, fmt.Errorf("OpenAI API call failed: %w", err)
	}

	if len(resp.Choices) == 0 {
		return false, errors.New("no response from OpenAI")
	}

	content := resp.Choices[0].Message.Content
	o.logger.Debug("OpenAI priority detection response received", zap.String("content", content))

	var response PriorityDetectionResponse
	if err := json.Unmarshal([]byte(content), &response); err != nil {
		o.logger.Error("Failed to parse OpenAI priority detection response",
			zap.Error(err),
			zap.String("content", content))
		return false, fmt.Errorf("failed to parse OpenAI response: %w", err)
	}

	o.logger.Debug("Priority detection completed",
		zap.Bool("is_priority_request", response.IsPriorityRequest),
		zap.Float64("confidence", response.Confidence),
		zap.String("reasoning", response.Reasoning))

	return response.IsPriorityRequest, nil
}

// GenerateTrackMood generates a mood description for the given tracks using OpenAI.
func (o *OpenAIClient) GenerateTrackMood(ctx context.Context, tracks []core.Track) (string, error) {
	if len(tracks) == 0 {
		return fallbackSearchQuery, nil
	}

	// Shuffle tracks to eliminate order bias
	shuffledTracks := make([]core.Track, len(tracks))
	copy(shuffledTracks, tracks)
	rand.Shuffle(len(shuffledTracks), func(i, j int) {
		shuffledTracks[i], shuffledTracks[j] = shuffledTracks[j], shuffledTracks[i]
	})

	// Build user prompt with track information
	userPrompt := "Based on all these songs:\n"
	for _, track := range shuffledTracks {
		userPrompt += fmt.Sprintf("- %s by %s", track.Title, track.Artist)
		if track.Album != "" {
			userPrompt += fmt.Sprintf(" (from %s)", track.Album)
		}
		userPrompt += "\n"
	}

	systemPrompt := o.buildTrackMoodPrompt()

	o.logger.Debug("Calling OpenAI for track mood generation",
		zap.Int("tracks", len(tracks)),
		zap.String("model", o.config.Model))

	resp, err := o.client.Chat.Completions.New(ctx, openai.ChatCompletionNewParams{
		Messages: []openai.ChatCompletionMessageParamUnion{
			openai.SystemMessage(systemPrompt),
			openai.UserMessage(userPrompt),
		},
		Model:       o.getModel(),
		Temperature: openai.Float(moodTemperature),
		MaxTokens:   openai.Int(maxTokensMood),
	})
	if err != nil {
		o.logger.Error("OpenAI API call failed for track mood generation", zap.Error(err))
		return fallbackSearchQuery, nil
	}

	if len(resp.Choices) == 0 {
		o.logger.Warn("OpenAI returned no response for track mood generation")
		return fallbackSearchQuery, nil
	}

	content := resp.Choices[0].Message.Content
	if content == "" {
		o.logger.Warn("OpenAI returned empty response for track mood generation")
		return fallbackSearchQuery, nil
	}

	trackMood := strings.TrimSpace(content)
	o.logger.Debug("Track mood generation completed",
		zap.String("mood", trackMood),
		zap.Int("tracks", len(tracks)))

	return trackMood, nil
}

// RankTracks ranks the given tracks based on their relevance to the search query using OpenAI.
func (o *OpenAIClient) RankTracks(ctx context.Context, searchQuery string, tracks []core.Track) []core.Track {
	if len(tracks) == 0 {
		return tracks
	}

	if len(tracks) == 1 {
		// No need to rank a single track
		return tracks
	}

	o.logger.Debug("Calling OpenAI for track ranking",
		zap.String("searchQuery", searchQuery),
		zap.Int("trackCount", len(tracks)))

	// Create a prompt for ranking tracks based on search query relevance
	prompt := fmt.Sprintf("You are a music expert. Given the search query %q, rank these tracks by "+
		"how well they match the search intent.\n\nTracks to rank:\n", searchQuery)
	for i, track := range tracks {
		prompt += fmt.Sprintf("%d. %s by %s", i+1, track.Title, track.Artist)
		if track.Album != "" {
			prompt += fmt.Sprintf(" (from %s)", track.Album)
		}
		prompt += "\n"
	}
	prompt += fmt.Sprintf("\nRespond with only the track numbers in order of best match first "+
		"(e.g., \"3,1,5,2,4\"). Consider genre, mood, tempo, and lyrical themes that would "+
		"match the search query %q.", searchQuery)

	// Use OpenAI to rank the tracks
	resp, err := o.client.Chat.Completions.New(ctx, openai.ChatCompletionNewParams{
		Messages: []openai.ChatCompletionMessageParamUnion{
			openai.SystemMessage("You are a music expert helping to rank tracks by relevance to a search query."),
			openai.UserMessage(prompt),
		},
		Model:       o.getModel(),
		Temperature: openai.Float(rankingTemperature),  // Lower temperature for more consistent ranking
		MaxTokens:   openai.Int(maxTokensTrackRanking), // Short response expected
	})
	if err != nil {
		o.logger.Warn("Failed to rank tracks with OpenAI, using original order", zap.Error(err))
		return tracks // Fallback to original order
	}

	if len(resp.Choices) == 0 || resp.Choices[0].Message.Content == "" {
		o.logger.Warn("OpenAI returned empty response for track ranking, using original order")
		return tracks
	}

	// Parse the ranking response
	rankingText := strings.TrimSpace(resp.Choices[0].Message.Content)
	rankedTracks := parseTrackRanking(rankingText, tracks, o.logger)

	o.logger.Info("Ranked tracks with OpenAI",
		zap.String("searchQuery", searchQuery),
		zap.Int("originalCount", len(tracks)),
		zap.Int("rankedCount", len(rankedTracks)),
		zap.String("ranking", rankingText))

	return rankedTracks
}

func (o *OpenAIClient) getModel() shared.ChatModel {
	if o.config.Model != "" {
		return o.config.Model
	}
	return defaultModel
}

func (o *OpenAIClient) buildChatterDetectionPrompt() string {
	return `You are a music bot assistant helping to filter out general chat from actual music requests.

Your task is to identify if a message is CLEARLY general chatter/conversation that should be ignored by a music bot.

Respond with a JSON object in this exact format:
{
  "is_not_music_request": true/false,
  "confidence": 0.85,
  "reasoning": "Brief explanation of the decision"
}

Rules:
1. confidence should be between 0.0 and 1.0
2. Set is_not_music_request to TRUE for obvious general chat/conversation
3. Set is_not_music_request to FALSE if there's ANY possibility it could be music-related
4. When in doubt, return FALSE (let the music bot process it)
5. Be VERY conservative - only filter out obvious non-music chatter

Examples of is_not_music_request = TRUE (FILTER OUT):
- "Hello everyone"
- "Good morning!"
- "How's everyone doing?"
- "What's the weather like?"
- "Anyone going to lunch?"
- "LOL that's funny"
- "Thanks for the help"
- "See you later"
- "Happy birthday!"
- "How was your weekend?"
- "I'm tired"
- "Working late today"
- Random emoji-only messages like "😂😂😂"
- Pure greetings and social conversation

Examples of is_not_music_request = FALSE (LET THROUGH):
- "Play Bohemian Rhapsody"
- "Add some Taylor Swift"
- "I love this song"
- "What song is this?"
- "Great music choice"
- "This reminds me of..."
- "I saw them in concert"
- "What's your favorite album?"
- spotify.com/track/xyz
- Any mention of songs, artists, albums, music
- Anything even remotely music-related
- Ambiguous messages that could be music requests

IMPORTANT: When uncertain, always return FALSE. Better to process a non-music message than to miss a music request.`
}

// ExtractSongQuery extracts a normalized search query from user text using OpenAI.
func (o *OpenAIClient) ExtractSongQuery(ctx context.Context, userText string) (string, error) {
	if strings.TrimSpace(userText) == "" {
		return "", errors.New("empty text provided")
	}

	prompt := o.buildSongExtractionPrompt()

	o.logger.Debug("Calling OpenAI for song extraction",
		zap.String("text", userText),
		zap.String("model", o.config.Model))

	resp, err := o.client.Chat.Completions.New(ctx, openai.ChatCompletionNewParams{
		Messages: []openai.ChatCompletionMessageParamUnion{
			openai.SystemMessage(prompt),
			openai.UserMessage(userText),
		},
		Model:       o.getModel(),
		Temperature: openai.Float(extractionTemperature),
		MaxTokens:   openai.Int(maxTokensExtraction),
	})
	if err != nil {
		o.logger.Error("OpenAI API call failed for song extraction", zap.Error(err))
		// Fallback to original text
		return userText, nil
	}

	if len(resp.Choices) == 0 {
		o.logger.Warn("OpenAI returned no response for song extraction")
		return userText, nil
	}

	extractedQuery := strings.TrimSpace(resp.Choices[0].Message.Content)
	o.logger.Debug("Song extraction completed",
		zap.String("original_text", userText),
		zap.String("extracted_query", extractedQuery))

	// If extraction is empty, fall back to original text
	if extractedQuery == "" {
		return userText, nil
	}

	return extractedQuery, nil
}

func (o *OpenAIClient) buildSongExtractionPrompt() string {
	return `Extract and normalize song requests from chat messages. Return only the normalized search query as plain text.

TASK: Transform casual song requests into clean search queries suitable for music services like Spotify.

RULES:
- Remove polite fillers: "please play", "can you queue", "yo bot", etc.
- Fix common misspellings and normalize artist/song names
- Preserve proper nouns and diacritics
- For artist-only requests, return just the artist name
- For song-only requests, return just the song title
- For artist + song, return "Artist Song Title" format

EXAMPLES:
"please play acdc hells bells" → "AC/DC Hells Bells"
"hells bells" → "Hells Bells"
"acdc" → "AC/DC"
"queue some taylor swift" → "Taylor Swift"
"can you add bohemian rhapsody by queen" → "Queen Bohemian Rhapsody"

If the message is clearly not a song request, return empty string.
Return only the normalized query, no explanations or formatting.`
}

func (o *OpenAIClient) buildPriorityDetectionPrompt() string {
	return `You are analyzing messages to detect priority music requests from group administrators.

Your task is to determine if a message contains indicators that a song should be prioritized " +
		"(added to the front of the playback queue).

Respond with a JSON object in this exact format:
{
  "is_priority_request": true,
  "confidence": 0.85,
  "reasoning": "Explanation of why this is/isn't a priority request"
}

PRIORITY INDICATORS (set is_priority_request = TRUE):
- Explicit priority keywords: "prio:", "priority:", "urgent:", "next:", "asap", "now"
- Phrases like "play this next", "add this to the front", "skip the queue"
- "This is urgent", "emergency song", "important"
- Time-sensitive context: "for the speech", "before the break", "while they're here"
- Event-related urgency: "for the toast", "entrance song", "while the boss is here"

NON-PRIORITY REQUESTS (set is_priority_request = FALSE):
- Regular song requests without urgency indicators
- General music requests like "add this song", "I like this"
- Casual mentions: "good song", "nice choice"
- Future requests: "add this later", "for the playlist"

CONFIDENCE SCORING:
- 0.9+: Clear explicit priority keywords ("prio:", "priority:", "next:")
- 0.7-0.9: Strong urgency language ("play this now", "urgent")
- 0.5-0.7: Contextual urgency ("for the speech", time-sensitive)
- 0.3-0.5: Weak urgency indicators
- 0.0-0.3: No priority indicators detected

EXAMPLES:
- "prio: Bohemian Rhapsody" → TRUE (0.95)
- "priority: add this song next" → TRUE (0.90)
- "play this asap" → TRUE (0.85)
- "we need this for the speech" → TRUE (0.75)
- "add this song" → FALSE (0.10)
- "good song choice" → FALSE (0.05)

IMPORTANT: Be conservative. Only mark as priority when there are clear indicators. Default to FALSE when uncertain.`
}

func (o *OpenAIClient) buildTrackMoodPrompt() string {
	return `You are a music expert helping to describe musical moods and styles.

Generate a short, descriptive mood/style phrase (3-6 words) describing the overall musical style of the provided songs.

IMPORTANT: Give equal weight to each song in your analysis.

Consider the common themes, genres, and moods that span across ALL the songs, not just the first one.

Look for the overall consensus and dominant characteristics that best represent the collection as a whole.

Focus on genre, mood, or artist style that captures the essence of the entire set. " +
		"Respond with just the mood phrase, no other text.

Examples:
- "energetic rock anthems"
- "mellow indie folk"
- "upbeat pop hits"
- "dark electronic beats"
- "classic jazz standards"`
}
