package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"go.uber.org/zap"

	"whatdj/internal/core"
)

type OllamaClient struct {
	config     *core.LLMConfig
	logger     *zap.Logger
	httpClient *http.Client
	baseURL    string
}

type OllamaRequest struct {
	Model  string `json:"model"`
	Prompt string `json:"prompt"`
	Stream bool   `json:"stream"`
	Format string `json:"format,omitempty"`
	Options map[string]interface{} `json:"options,omitempty"`
}

type OllamaResponse struct {
	Response string `json:"response"`
	Done     bool   `json:"done"`
}

func NewOllamaClient(config *core.LLMConfig, logger *zap.Logger) (*OllamaClient, error) {
	baseURL := config.BaseURL
	if baseURL == "" {
		baseURL = "http://localhost:11434"
	}

	httpClient := &http.Client{
		Timeout: 60 * time.Second,
	}

	return &OllamaClient{
		config:     config,
		logger:     logger,
		httpClient: httpClient,
		baseURL:    baseURL,
	}, nil
}

func (o *OllamaClient) RankCandidates(ctx context.Context, text string) ([]core.LLMCandidate, error) {
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
- Maximum 3 candidates

Respond with valid JSON only.`, text)

	model := o.config.Model
	if model == "" {
		model = "llama3.2"
	}

	reqBody := OllamaRequest{
		Model:  model,
		Prompt: prompt,
		Stream: false,
		Format: "json",
		Options: map[string]interface{}{
			"temperature": 0.3,
			"num_predict": 500,
		},
	}

	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", o.baseURL+"/api/generate", bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := o.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("Ollama API call failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Ollama API returned status %d", resp.StatusCode)
	}

	var ollamaResp OllamaResponse
	if err := json.NewDecoder(resp.Body).Decode(&ollamaResp); err != nil {
		return nil, fmt.Errorf("failed to decode Ollama response: %w", err)
	}

	var response OpenAIResponse // Reuse the same struct
	if err := json.Unmarshal([]byte(ollamaResp.Response), &response); err != nil {
		o.logger.Error("Failed to parse Ollama response", zap.Error(err), zap.String("content", ollamaResp.Response))
		return nil, fmt.Errorf("failed to parse Ollama response: %w", err)
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

	o.logger.Debug("Ollama candidates generated",
		zap.String("input", text),
		zap.Int("count", len(candidates)))

	return candidates, nil
}

func (o *OllamaClient) ExtractSongInfo(ctx context.Context, text string) (*core.Track, error) {
	candidates, err := o.RankCandidates(ctx, text)
	if err != nil {
		return nil, err
	}

	if len(candidates) == 0 {
		return nil, fmt.Errorf("no song information extracted")
	}

	return &candidates[0].Track, nil
}