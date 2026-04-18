package intelligent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"agent-gateway/internal/config"
)

type TavilyClient struct {
	baseURL    string
	apiKey     string
	maxResults int
	client     *http.Client
}

type tavilySearchResponse struct {
	Results []struct {
		Title   string `json:"title"`
		URL     string `json:"url"`
		Content string `json:"content"`
	} `json:"results"`
}

func NewTavilyClient(cfg config.WebSearchConfig) *TavilyClient {
	return &TavilyClient{
		baseURL:    strings.TrimRight(cfg.BaseURL, "/"),
		apiKey:     cfg.APIKey,
		maxResults: cfg.MaxResults,
		client: &http.Client{
			Timeout: time.Duration(cfg.Timeout),
		},
	}
}

func (c *TavilyClient) Search(ctx context.Context, query string) ([]SearchResult, error) {
	if strings.TrimSpace(query) == "" {
		return nil, nil
	}

	payload := map[string]any{
		"query":               query,
		"search_depth":        "basic",
		"topic":               "general",
		"max_results":         c.maxResults,
		"include_raw_content": false,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/search", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= http.StatusBadRequest {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
		return nil, fmt.Errorf("tavily search failed with status %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}

	var parsed tavilySearchResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, err
	}

	results := make([]SearchResult, 0, len(parsed.Results))
	for _, item := range parsed.Results {
		results = append(results, SearchResult{
			Title:   item.Title,
			URL:     item.URL,
			Content: item.Content,
		})
	}

	return results, nil
}
