package llm

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
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
	prompt := fmt.Sprintf(`You are a music resolver. Given raw chat text, propose up to 3 likely Spotify tracks as JSON.

Input text: "%s"

Return JSON in this exact format:
{
  "candidates": [
    {
      "title": "Song Title",
      "artist": "Artist Name",
      "album": "Album Name",
      "year": 2020,
      "confidence": 0.85,
      "reasoning": "Brief explanation"
    }
  ]
}

Rules:
- confidence: 0.0-1.0 (higher = more certain)
- Only include real songs that exist
- If unsure, use lower confidence
- Maximum 3 candidates`, text)

	model := o.config.Model
	if model == "" {
		model = "gpt-3.5-turbo"
	}

	chatCompletion, err := o.client.Chat.Completions.New(ctx, openai.ChatCompletionNewParams{
		Messages: []openai.ChatCompletionMessageParamUnion{
			openai.UserMessage(prompt),
		},
		Model:       openai.String(model),
		MaxTokens:   openai.Int(500),
		Temperature: openai.Float(0.3),
	})

	if err != nil {
		return nil, fmt.Errorf("OpenAI API call failed: %w", err)
	}

	if len(chatCompletion.Choices) == 0 {
		return nil, fmt.Errorf("no response from OpenAI")
	}

	content := chatCompletion.Choices[0].Message.Content

	var response OpenAIResponse
	if err := json.Unmarshal([]byte(content), &response); err != nil {
		o.logger.Error("Failed to parse OpenAI response", zap.Error(err), zap.String("content", content))
		return nil, fmt.Errorf("failed to parse OpenAI response: %w", err)
	}

	var candidates []core.LLMCandidate
	for _, candidate := range response.Candidates {
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

	o.logger.Debug("OpenAI candidates generated",
		zap.String("input", text),
		zap.Int("count", len(candidates)))

	return candidates, nil
}

func (o *OpenAIClient) ExtractSongInfo(ctx context.Context, text string) (*core.Track, error) {
	candidates, err := o.RankCandidates(ctx, text)
	if err != nil {
		return nil, err
	}

	if len(candidates) == 0 {
		return nil, fmt.Errorf("no song information extracted")
	}

	return &candidates[0].Track, nil
}