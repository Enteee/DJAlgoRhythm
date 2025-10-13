package llm

import (
	"context"
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

func (o *OpenAIClient) RankCandidates(_ context.Context, _ string) ([]core.LLMCandidate, error) {
	// TODO: Implement OpenAI integration when API is stable
	return nil, fmt.Errorf("OpenAI integration not yet implemented")
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
