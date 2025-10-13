package llm

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"go.uber.org/zap"

	"whatdj/internal/core"
)

type AnthropicClient struct {
	config *core.LLMConfig
	logger *zap.Logger
	client *anthropic.Client
}

func NewAnthropicClient(config *core.LLMConfig, logger *zap.Logger) (*AnthropicClient, error) {
	if config.APIKey == "" {
		return nil, fmt.Errorf("Anthropic API key is required")
	}

	var opts []option.RequestOption
	opts = append(opts, option.WithAPIKey(config.APIKey))

	if config.BaseURL != "" {
		opts = append(opts, option.WithBaseURL(config.BaseURL))
	}

	client := anthropic.NewClient(opts...)

	return &AnthropicClient{
		config: config,
		logger: logger,
		client: &client,
	}, nil
}

func (a *AnthropicClient) RankCandidates(ctx context.Context, text string) ([]core.LLMCandidate, error) {
	systemPrompt := `You are a music resolver. Given raw chat text, propose up to 3 likely Spotify tracks as JSON.

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
- Maximum 3 candidates`

	userPrompt := fmt.Sprintf(`Input text: "%s"`, text)

	model := a.config.Model
	if model == "" {
		model = "claude-3-haiku-20240307"
	}

	message, err := a.client.Messages.New(ctx, anthropic.MessageNewParams{
		Model:     anthropic.String(model),
		MaxTokens: anthropic.Int(500),
		System: []anthropic.TextBlockParam{{
			Type: "text",
			Text: anthropic.String(systemPrompt),
		}},
		Messages: []anthropic.MessageParam{
			anthropic.NewUserMessage(anthropic.NewTextBlock(userPrompt)),
		},
		Temperature: anthropic.Float(0.3),
	})

	if err != nil {
		return nil, fmt.Errorf("Anthropic API call failed: %w", err)
	}

	if len(message.Content) == 0 {
		return nil, fmt.Errorf("no response from Anthropic")
	}

	content := message.Content[0].Text

	var response OpenAIResponse // Reuse the same struct
	if err := json.Unmarshal([]byte(content), &response); err != nil {
		a.logger.Error("Failed to parse Anthropic response", zap.Error(err), zap.String("content", content))
		return nil, fmt.Errorf("failed to parse Anthropic response: %w", err)
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

	a.logger.Debug("Anthropic candidates generated",
		zap.String("input", text),
		zap.Int("count", len(candidates)))

	return candidates, nil
}

func (a *AnthropicClient) ExtractSongInfo(ctx context.Context, text string) (*core.Track, error) {
	candidates, err := a.RankCandidates(ctx, text)
	if err != nil {
		return nil, err
	}

	if len(candidates) == 0 {
		return nil, fmt.Errorf("no song information extracted")
	}

	return &candidates[0].Track, nil
}