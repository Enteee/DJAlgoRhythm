// Package llm provides LLM (Large Language Model) integration for song disambiguation.
package llm

import (
	"context"
	"fmt"
	"strings"

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
		return nil, fmt.Errorf("anthropic API key is required")
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

func (a *AnthropicClient) RankCandidates(_ context.Context, _ string) ([]core.LLMCandidate, error) {
	// TODO: Implement Anthropic integration when API is stable
	return nil, fmt.Errorf("anthropic integration not yet implemented")
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

func (a *AnthropicClient) IsNotMusicRequest(_ context.Context, text string) (bool, error) {
	// Conservative approach: only filter out obvious chatter
	a.logger.Debug("Anthropic chatter detection (basic implementation)",
		zap.String("text", text))

	// Filter out obvious non-music chatter
	chatterKeywords := []string{
		"hello", "hi", "hey", "good morning", "good afternoon", "good evening", "good night",
		"how are you", "how's everyone", "what's up", "weather", "lunch", "dinner", "work",
		"tired", "busy", "weekend", "holiday", "birthday", "thanks", "thank you", "lol",
		"haha", "see you", "bye", "goodbye", "later",
	}

	textLower := strings.ToLower(text)

	// Check for obvious chatter patterns
	for _, keyword := range chatterKeywords {
		if strings.Contains(textLower, keyword) {
			return true, nil
		}
	}

	const minMessageLength = 3
	// If very short and no music indicators, likely chatter
	if len(strings.TrimSpace(text)) < minMessageLength {
		return true, nil
	}

	// Default to false (let through) to avoid filtering music requests
	return false, nil
}
