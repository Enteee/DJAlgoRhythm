package llm

import (
	"context"
	"fmt"

	"go.uber.org/zap"

	"whatdj/internal/core"
)

type Provider struct {
	config *core.LLMConfig
	logger *zap.Logger
	client LLMClient
}

type LLMClient interface {
	RankCandidates(ctx context.Context, text string) ([]core.LLMCandidate, error)
	ExtractSongInfo(ctx context.Context, text string) (*core.Track, error)
}

func NewProvider(config *core.LLMConfig, logger *zap.Logger) (*Provider, error) {
	var client LLMClient
	var err error

	switch config.Provider {
	case "openai":
		client, err = NewOpenAIClient(config, logger)
	case "anthropic":
		client, err = NewAnthropicClient(config, logger)
	case "ollama":
		client, err = NewOllamaClient(config, logger)
	case "none", "":
		return &Provider{
			config: config,
			logger: logger,
			client: &NoOpClient{},
		}, nil
	default:
		return nil, fmt.Errorf("unsupported LLM provider: %s", config.Provider)
	}

	if err != nil {
		return nil, fmt.Errorf("failed to create %s client: %w", config.Provider, err)
	}

	return &Provider{
		config: config,
		logger: logger,
		client: client,
	}, nil
}

func (p *Provider) RankCandidates(ctx context.Context, text string) ([]core.LLMCandidate, error) {
	candidates, err := p.client.RankCandidates(ctx, text)
	if err != nil {
		return nil, err
	}

	if len(candidates) > p.config.MaxCandidates {
		candidates = candidates[:p.config.MaxCandidates]
	}

	return candidates, nil
}

func (p *Provider) ExtractSongInfo(ctx context.Context, text string) (*core.Track, error) {
	return p.client.ExtractSongInfo(ctx, text)
}

type NoOpClient struct{}

func (n *NoOpClient) RankCandidates(ctx context.Context, text string) ([]core.LLMCandidate, error) {
	return nil, fmt.Errorf("LLM provider not configured")
}

func (n *NoOpClient) ExtractSongInfo(ctx context.Context, text string) (*core.Track, error) {
	return nil, fmt.Errorf("LLM provider not configured")
}