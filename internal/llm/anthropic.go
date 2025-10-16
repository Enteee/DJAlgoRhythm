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

func (a *AnthropicClient) IsMusicRequest(_ context.Context, text string) (bool, error) {
	// For now, default to true to maintain existing behavior
	// This is a basic implementation that could be enhanced with actual Anthropic API calls
	a.logger.Debug("Anthropic music request detection (basic implementation)",
		zap.String("text", text))

	// Simple heuristic: if it contains common music keywords, treat as music request
	musicKeywords := []string{"play", "add", "song", "music", "artist", "album", "track", "spotify", "youtube"}
	textLower := strings.ToLower(text)

	for _, keyword := range musicKeywords {
		if strings.Contains(textLower, keyword) {
			return true, nil
		}
	}

	// Default to false for non-music content
	return false, nil
}
