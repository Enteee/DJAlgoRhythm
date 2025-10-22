package llm

import (
	"context"
	"fmt"
	"strconv"
	"strings"

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
	RankTracks(ctx context.Context, searchQuery string, tracks []core.Track) []core.Track
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

func (p *Provider) RankTracks(ctx context.Context, searchQuery string, tracks []core.Track) []core.Track {
	return p.client.RankTracks(ctx, searchQuery, tracks)
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

func (n *NoOpClient) RankTracks(_ context.Context, _ string, tracks []core.Track) []core.Track {
	// When no LLM provider is configured, return tracks in original order
	return tracks
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

// parseTrackRanking parses LLM ranking response and returns tracks in ranked order
func parseTrackRanking(rankingText string, originalTracks []core.Track, logger *zap.Logger) []core.Track {
	// Expected format: "3,1,5,2,4" (comma-separated track numbers)
	parts := strings.Split(strings.ReplaceAll(rankingText, " ", ""), ",")
	var rankedTracks []core.Track
	usedIndices := make(map[int]bool)

	// Parse each ranking number and add corresponding track
	for _, part := range parts {
		if idx, err := strconv.Atoi(part); err == nil {
			// Convert from 1-based to 0-based indexing
			arrayIdx := idx - 1
			if arrayIdx >= 0 && arrayIdx < len(originalTracks) && !usedIndices[arrayIdx] {
				rankedTracks = append(rankedTracks, originalTracks[arrayIdx])
				usedIndices[arrayIdx] = true
			}
		}
	}

	// Add any tracks that weren't included in the ranking (fallback)
	for i, track := range originalTracks {
		if !usedIndices[i] {
			rankedTracks = append(rankedTracks, track)
		}
	}

	// If parsing completely failed, return original order
	if len(rankedTracks) == 0 {
		logger.Warn("Failed to parse track ranking response, using original order")
		return originalTracks
	}

	return rankedTracks
}
