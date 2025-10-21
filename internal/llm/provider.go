package llm

import (
	"context"
	"fmt"

	"go.uber.org/zap"

	"whatdj/internal/core"
)

const fallbackSearchQuery = "popular music"

type Provider struct {
	config *core.LLMConfig
	logger *zap.Logger
	client Client
}

type Client interface {
	RankCandidates(ctx context.Context, text string) ([]core.LLMCandidate, error)
	IsNotMusicRequest(ctx context.Context, text string) (bool, error)
	IsPriorityRequest(ctx context.Context, text string) (bool, error)
	GenerateSearchQuery(ctx context.Context, seedTracks []core.Track) (string, error)
}

func NewProvider(config *core.LLMConfig, logger *zap.Logger) (*Provider, error) {
	var client Client
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

func (p *Provider) IsNotMusicRequest(ctx context.Context, text string) (bool, error) {
	return p.client.IsNotMusicRequest(ctx, text)
}

func (p *Provider) IsPriorityRequest(ctx context.Context, text string) (bool, error) {
	return p.client.IsPriorityRequest(ctx, text)
}

func (p *Provider) GenerateSearchQuery(ctx context.Context, seedTracks []core.Track) (string, error) {
	return p.client.GenerateSearchQuery(ctx, seedTracks)
}

type NoOpClient struct{}

func (n *NoOpClient) RankCandidates(_ context.Context, _ string) ([]core.LLMCandidate, error) {
	return nil, fmt.Errorf("LLM provider not configured")
}

func (n *NoOpClient) IsNotMusicRequest(_ context.Context, _ string) (bool, error) {
	// When no LLM provider is configured, don't filter anything (return false)
	return false, nil
}

func (n *NoOpClient) IsPriorityRequest(_ context.Context, _ string) (bool, error) {
	// When no LLM provider is configured, assume no priority (return false)
	return false, nil
}

func (n *NoOpClient) GenerateSearchQuery(_ context.Context, _ []core.Track) (string, error) {
	return "", fmt.Errorf("LLM provider not configured")
}
