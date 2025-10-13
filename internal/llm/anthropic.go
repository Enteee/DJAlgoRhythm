// Package llm provides LLM (Large Language Model) integration for song disambiguation.
package llm

import (
	"context"
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
