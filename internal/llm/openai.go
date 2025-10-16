package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"github.com/openai/openai-go/shared"
	"go.uber.org/zap"

	"whatdj/internal/core"
)

type OpenAIClient struct {
	config *core.LLMConfig
	logger *zap.Logger
	client *openai.Client
}

type OpenAIResponse struct {
	Candidates []struct {
		Title      string  `json:"title"`
		Artist     string  `json:"artist"`
		Album      string  `json:"album,omitempty"`
		Year       int     `json:"year,omitempty"`
		Confidence float64 `json:"confidence"`
		Reasoning  string  `json:"reasoning,omitempty"`
	} `json:"candidates"`
}

type SongExtractResponse struct {
	Found  bool   `json:"found"`
	Title  string `json:"title,omitempty"`
	Artist string `json:"artist,omitempty"`
	Album  string `json:"album,omitempty"`
	Year   int    `json:"year,omitempty"`
	Reason string `json:"reason,omitempty"`
}

const (
	defaultTemperature  = 0.1
	maxTokensRanking    = 1000
	maxTokensExtraction = 500
	defaultModel        = "gpt-3.5-turbo"
)

func NewOpenAIClient(config *core.LLMConfig, logger *zap.Logger) (*OpenAIClient, error) {
	if config.APIKey == "" {
		return nil, fmt.Errorf("OpenAI API key is required")
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

func (o *OpenAIClient) RankCandidates(ctx context.Context, text string) ([]core.LLMCandidate, error) {
	if strings.TrimSpace(text) == "" {
		return nil, fmt.Errorf("empty text provided")
	}

	prompt := o.buildRankCandidatesPrompt(text)

	o.logger.Debug("Calling OpenAI for candidate ranking",
		zap.String("text", text),
		zap.String("model", o.config.Model))

	resp, err := o.client.Chat.Completions.New(ctx, openai.ChatCompletionNewParams{
		Messages: []openai.ChatCompletionMessageParamUnion{
			openai.SystemMessage(prompt),
			openai.UserMessage(text),
		},
		Model:       o.getModel(),
		Temperature: openai.Float(defaultTemperature),
		MaxTokens:   openai.Int(maxTokensRanking),
	})
	if err != nil {
		o.logger.Error("OpenAI API call failed", zap.Error(err))
		return nil, fmt.Errorf("OpenAI API call failed: %w", err)
	}

	if len(resp.Choices) == 0 {
		return nil, fmt.Errorf("no response from OpenAI")
	}

	content := resp.Choices[0].Message.Content
	o.logger.Debug("OpenAI response received", zap.String("content", content))

	var response OpenAIResponse
	if err := json.Unmarshal([]byte(content), &response); err != nil {
		o.logger.Error("Failed to parse OpenAI response",
			zap.Error(err),
			zap.String("content", content))
		return nil, fmt.Errorf("failed to parse OpenAI response: %w", err)
	}

	var candidates []core.LLMCandidate
	for _, candidate := range response.Candidates {
		if candidate.Confidence < o.config.Threshold {
			o.logger.Debug("Skipping low confidence candidate",
				zap.String("title", candidate.Title),
				zap.String("artist", candidate.Artist),
				zap.Float64("confidence", candidate.Confidence),
				zap.Float64("threshold", o.config.Threshold))
			continue
		}

		track := core.Track{
			Title:  candidate.Title,
			Artist: candidate.Artist,
			Album:  candidate.Album,
			Year:   candidate.Year,
		}

		candidates = append(candidates, core.LLMCandidate{
			Track:      track,
			Confidence: candidate.Confidence,
			Reasoning:  candidate.Reasoning,
		})
	}

	o.logger.Info("OpenAI candidate ranking completed",
		zap.Int("total_candidates", len(response.Candidates)),
		zap.Int("filtered_candidates", len(candidates)))

	return candidates, nil
}

func (o *OpenAIClient) ExtractSongInfo(ctx context.Context, text string) (*core.Track, error) {
	if strings.TrimSpace(text) == "" {
		return nil, fmt.Errorf("empty text provided")
	}

	prompt := o.buildExtractSongPrompt()

	o.logger.Debug("Calling OpenAI for song extraction",
		zap.String("text", text),
		zap.String("model", o.config.Model))

	resp, err := o.client.Chat.Completions.New(ctx, openai.ChatCompletionNewParams{
		Messages: []openai.ChatCompletionMessageParamUnion{
			openai.SystemMessage(prompt),
			openai.UserMessage(text),
		},
		Model:       o.getModel(),
		Temperature: openai.Float(defaultTemperature),
		MaxTokens:   openai.Int(maxTokensExtraction),
	})
	if err != nil {
		o.logger.Error("OpenAI API call failed", zap.Error(err))
		return nil, fmt.Errorf("OpenAI API call failed: %w", err)
	}

	if len(resp.Choices) == 0 {
		return nil, fmt.Errorf("no response from OpenAI")
	}

	content := resp.Choices[0].Message.Content
	o.logger.Debug("OpenAI response received", zap.String("content", content))

	var response SongExtractResponse
	if err := json.Unmarshal([]byte(content), &response); err != nil {
		o.logger.Error("Failed to parse OpenAI response",
			zap.Error(err),
			zap.String("content", content))
		return nil, fmt.Errorf("failed to parse OpenAI response: %w", err)
	}

	if !response.Found {
		o.logger.Debug("No song found in text", zap.String("reason", response.Reason))
		return nil, fmt.Errorf("no song information found: %s", response.Reason)
	}

	track := &core.Track{
		Title:  response.Title,
		Artist: response.Artist,
		Album:  response.Album,
		Year:   response.Year,
	}

	o.logger.Info("Song extracted successfully",
		zap.String("title", track.Title),
		zap.String("artist", track.Artist))

	return track, nil
}

type MusicRequestResponse struct {
	IsMusicRequest bool    `json:"is_music_request"`
	Confidence     float64 `json:"confidence"`
	Reasoning      string  `json:"reasoning,omitempty"`
}

func (o *OpenAIClient) IsMusicRequest(ctx context.Context, text string) (bool, error) {
	if strings.TrimSpace(text) == "" {
		return false, fmt.Errorf("empty text provided")
	}

	prompt := o.buildMusicRequestPrompt()

	o.logger.Debug("Calling OpenAI for music request detection",
		zap.String("text", text),
		zap.String("model", o.config.Model))

	resp, err := o.client.Chat.Completions.New(ctx, openai.ChatCompletionNewParams{
		Messages: []openai.ChatCompletionMessageParamUnion{
			openai.SystemMessage(prompt),
			openai.UserMessage(text),
		},
		Model:       o.getModel(),
		Temperature: openai.Float(defaultTemperature),
		MaxTokens:   openai.Int(maxTokensExtraction),
	})
	if err != nil {
		o.logger.Error("OpenAI API call failed", zap.Error(err))
		return false, fmt.Errorf("OpenAI API call failed: %w", err)
	}

	if len(resp.Choices) == 0 {
		return false, fmt.Errorf("no response from OpenAI")
	}

	content := resp.Choices[0].Message.Content
	o.logger.Debug("OpenAI response received", zap.String("content", content))

	var response MusicRequestResponse
	if err := json.Unmarshal([]byte(content), &response); err != nil {
		o.logger.Error("Failed to parse OpenAI response",
			zap.Error(err),
			zap.String("content", content))
		return false, fmt.Errorf("failed to parse OpenAI response: %w", err)
	}

	o.logger.Debug("Music request detection completed",
		zap.Bool("is_music_request", response.IsMusicRequest),
		zap.Float64("confidence", response.Confidence),
		zap.String("reasoning", response.Reasoning))

	return response.IsMusicRequest, nil
}

func (o *OpenAIClient) getModel() shared.ChatModel {
	if o.config.Model != "" {
		return o.config.Model
	}
	return defaultModel
}

func (o *OpenAIClient) buildRankCandidatesPrompt(_ string) string {
	return `You are a music expert helping to identify songs from user messages.

Your task is to analyze the user's message and identify potential song candidates. The user might mention:
- Song title and artist
- Just a song title
- Just an artist name
- Lyrics or parts of lyrics
- Album name
- Description of a song

Respond with a JSON object in this exact format:
{
  "candidates": [
    {
      "title": "Song Title",
      "artist": "Artist Name",
      "album": "Album Name (optional)",
      "year": 2023,
      "confidence": 0.85,
      "reasoning": "Why this is likely the correct song"
    }
  ]
}

Rules:
1. confidence should be between 0.0 and 1.0
2. Only include candidates you're reasonably confident about (>0.5)
3. Order by confidence (highest first)
4. Include up to 3 candidates maximum
5. Be conservative - if unclear, use lower confidence scores
6. If no clear song can be identified, return empty candidates array

Examples of good confidence scoring:
- 0.9+: Exact title + artist match
- 0.7-0.9: Title + artist with minor variations
- 0.5-0.7: Partial matches or common song references
- <0.5: Unclear or very uncertain matches`
}

func (o *OpenAIClient) buildExtractSongPrompt() string {
	return `You are a music expert helping to extract song information from user messages.

Your task is to determine if the user's message contains information about a specific song, and if so, extract the song details.

Respond with a JSON object in this exact format:
{
  "found": true/false,
  "title": "Song Title",
  "artist": "Artist Name",
  "album": "Album Name (optional)",
  "year": 2023,
  "reason": "Explanation of why song was/wasn't found"
}

Rules:
1. Set "found" to true only if you can identify a specific song
2. If found=false, include a brief reason in the "reason" field
3. Be conservative - only extract when you're confident about the song
4. Handle common music references, lyrics, and descriptions
5. If multiple songs are mentioned, extract the most prominent one

Examples of when to set found=true:
- "Play Bohemian Rhapsody by Queen"
- "I love that song 'Imagine' by John Lennon"
- "Put on some Beatles - Yesterday"

Examples of when to set found=false:
- "Play some music"
- "I like rock music"
- "What's that song that goes 'na na na'?" (too vague)`
}

func (o *OpenAIClient) buildMusicRequestPrompt() string {
	return `You are a music bot assistant helping to identify if a message is a music/song request.

Your task is to determine if the user's message is requesting a song to be added to a playlist, or if it's just general chat.

Respond with a JSON object in this exact format:
{
  "is_music_request": true/false,
  "confidence": 0.85,
  "reasoning": "Brief explanation of the decision"
}

Rules:
1. confidence should be between 0.0 and 1.0
2. Set is_music_request to true only if the user wants a specific song added/played
3. Be conservative - if unclear, set to false
4. Consider context clues like "play", "add", "put on", song titles, artist names
5. Ignore general music discussion that isn't requesting a specific song

Examples of is_music_request = TRUE:
- "Play Bohemian Rhapsody by Queen"
- "Add some Taylor Swift"
- "Put on that new song by Drake"
- "Can you play Yesterday by The Beatles?"
- "I want to hear some Radiohead"
- spotify.com/track/xyz (sharing a link)
- "Play that song that goes 'hey jude'"

Examples of is_music_request = FALSE:
- "Hello everyone"
- "How's everyone doing?"
- "I love music"
- "What's your favorite band?"
- "I went to a concert yesterday"
- "The weather is nice today"
- "Music is great"
- "I like rock music" (too general)
- "What are you listening to?" (asking, not requesting)
- Random conversation not about music

Be very strict: only return true if the user is clearly requesting a specific song to be played/added.`
}
