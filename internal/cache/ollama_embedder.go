package cache

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"agent-gateway/internal/config"
)

type OllamaEmbedder struct {
	baseURL string
	model   string
	client  *http.Client
}

func NewSemanticEmbedder(cfg config.SemanticEmbeddingConfig) SemanticEmbedder {
	switch strings.ToLower(strings.TrimSpace(cfg.Provider)) {
	case "", "ollama":
		return NewOllamaEmbedder(cfg)
	default:
		return nil
	}
}

func NewOllamaEmbedder(cfg config.SemanticEmbeddingConfig) *OllamaEmbedder {
	timeout := time.Duration(cfg.Timeout)
	if timeout <= 0 {
		timeout = 15 * time.Second
	}

	baseURL := strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	if baseURL == "" {
		baseURL = "http://127.0.0.1:11434"
	}

	return &OllamaEmbedder{
		baseURL: baseURL,
		model:   strings.TrimSpace(cfg.Model),
		client: &http.Client{
			Timeout: timeout,
		},
	}
}

func (e *OllamaEmbedder) Embed(ctx context.Context, texts []string) ([][]float64, error) {
	if e == nil || len(texts) == 0 {
		return nil, nil
	}

	payload := map[string]any{
		"model": e.model,
		"input": texts,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.baseURL+"/api/embed", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := e.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("ollama embed request failed with status %d", resp.StatusCode)
	}

	var parsed struct {
		Embeddings [][]float64 `json:"embeddings"`
		Embedding  []float64   `json:"embedding"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, err
	}

	if len(parsed.Embeddings) > 0 {
		return parsed.Embeddings, nil
	}
	if len(parsed.Embedding) > 0 {
		return [][]float64{parsed.Embedding}, nil
	}

	return nil, fmt.Errorf("ollama embed response did not contain embeddings")
}
