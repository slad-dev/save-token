package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"agent-gateway/internal/auth"
	"agent-gateway/internal/store"
)

type Proxy struct {
	registry *Registry
	client   *http.Client
	store    *store.SQLiteStore
	healthMu sync.RWMutex
	health   map[string]upstreamHealth
}

const (
	upstreamFailureThreshold = 2
	upstreamCooldownDuration = 20 * time.Second
)

type upstreamHealth struct {
	consecutiveFailures int
	cooldownUntil       time.Time
}

type ForwardRequest struct {
	Path            string
	Model           string
	Method          string
	Body            []byte
	Header          http.Header
	AllowRetryCodes map[int]struct{}
}

type ProxyResponse struct {
	StatusCode int
	Header     http.Header
	Body       io.ReadCloser
	Upstream   Upstream
}

func NewProxy(registry *Registry, client *http.Client, st *store.SQLiteStore) *Proxy {
	return &Proxy{
		registry: registry,
		client:   client,
		store:    st,
		health:   make(map[string]upstreamHealth),
	}
}

func (p *Proxy) Forward(ctx context.Context, request ForwardRequest) (*ProxyResponse, error) {
	candidates, err := p.candidates(ctx, request.Model)
	if err != nil {
		return nil, err
	}
	candidates = p.prioritizeCandidates(candidates)

	var lastErr error
	for i, upstream := range candidates {
		if p.isCoolingDown(upstream.Name) && i < len(candidates)-1 {
			lastErr = fmt.Errorf("upstream %s is cooling down after consecutive failures", upstream.Name)
			continue
		}

		response, err := p.do(ctx, upstream, request)
		if err != nil {
			p.markFailure(upstream.Name)
			lastErr = err
			continue
		}

		if _, retryable := request.AllowRetryCodes[response.StatusCode]; retryable && i < len(candidates)-1 {
			p.markFailure(upstream.Name)
			_ = response.Body.Close()
			lastErr = fmt.Errorf("upstream %s returned retryable status %d", upstream.Name, response.StatusCode)
			continue
		}
		if _, retryable := request.AllowRetryCodes[response.StatusCode]; retryable {
			p.markFailure(upstream.Name)
		} else {
			p.markSuccess(upstream.Name)
		}

		return response, nil
	}

	if lastErr == nil {
		lastErr = errors.New("all upstreams failed")
	}

	return nil, lastErr
}

func (p *Proxy) prioritizeCandidates(candidates []Upstream) []Upstream {
	if len(candidates) <= 1 {
		return candidates
	}

	healthy := make([]Upstream, 0, len(candidates))
	cooling := make([]Upstream, 0, len(candidates))
	for _, candidate := range candidates {
		if p.isCoolingDown(candidate.Name) {
			cooling = append(cooling, candidate)
			continue
		}
		healthy = append(healthy, candidate)
	}

	if len(healthy) == 0 {
		return candidates
	}
	return append(healthy, cooling...)
}

func (p *Proxy) isCoolingDown(name string) bool {
	p.healthMu.RLock()
	state, ok := p.health[name]
	p.healthMu.RUnlock()
	if !ok {
		return false
	}
	return time.Now().Before(state.cooldownUntil)
}

func (p *Proxy) markFailure(name string) {
	now := time.Now()
	p.healthMu.Lock()
	state := p.health[name]
	state.consecutiveFailures++
	if state.consecutiveFailures >= upstreamFailureThreshold {
		state.cooldownUntil = now.Add(upstreamCooldownDuration)
		state.consecutiveFailures = 0
	}
	p.health[name] = state
	p.healthMu.Unlock()
}

func (p *Proxy) markSuccess(name string) {
	p.healthMu.Lock()
	delete(p.health, name)
	p.healthMu.Unlock()
}

func (p *Proxy) Models(ctx context.Context) []string {
	models := p.registry.Models()
	principal, ok := auth.PrincipalFromContext(ctx)
	if !ok || p.store == nil || principal.APIKey.ProjectID == nil {
		return models
	}

	if principal.APIKey.Project != nil && principal.APIKey.Project.Mode == "managed" {
		return models
	}

	sources, err := p.store.ListActiveSourcesByProject(ctx, principal.User.ID, *principal.APIKey.ProjectID)
	if err != nil {
		return models
	}

	seen := make(map[string]struct{}, len(models))
	for _, model := range models {
		seen[model] = struct{}{}
	}
	for _, source := range sources {
		for _, model := range splitSupportedModels(source.SupportedModels) {
			if _, ok := seen[model]; ok {
				continue
			}
			seen[model] = struct{}{}
			models = append(models, model)
		}
	}

	sort.Strings(models)
	return models
}

func (p *Proxy) candidates(ctx context.Context, model string) ([]Upstream, error) {
	managedCandidates, managedErr := p.registry.Candidates(model)
	if p.store == nil {
		return managedCandidates, managedErr
	}

	principal, ok := auth.PrincipalFromContext(ctx)
	if !ok || principal.APIKey.ProjectID == nil {
		return managedCandidates, managedErr
	}

	mode := "hybrid"
	if principal.APIKey.Project != nil && strings.TrimSpace(principal.APIKey.Project.Mode) != "" {
		mode = strings.ToLower(strings.TrimSpace(principal.APIKey.Project.Mode))
	}

	customSources, err := p.store.ListActiveSourcesByProject(ctx, principal.User.ID, *principal.APIKey.ProjectID)
	if err != nil {
		if mode == "managed" {
			return managedCandidates, managedErr
		}
		return nil, err
	}

	customCandidates := make([]Upstream, 0, len(customSources))
	for _, source := range customSources {
		upstream := Upstream{
			ID:              int64(source.ID),
			Name:            fmt.Sprintf("source:%d:%s", source.ID, source.Name),
			BaseURL:         source.BaseURL,
			APIKey:          source.APIKey,
			Weight:          1,
			SupportedModels: splitSupportedModels(source.SupportedModels),
		}
		if supportsModel(upstream, model) {
			customCandidates = append(customCandidates, upstream)
		}
	}

	switch mode {
	case "managed":
		return managedCandidates, managedErr
	case "byok":
		if len(customCandidates) == 0 {
			return nil, fmt.Errorf("no active project source available for model %q", model)
		}
		return customCandidates, nil
	default:
		if len(customCandidates) == 0 {
			return managedCandidates, managedErr
		}
		merged := make([]Upstream, 0, len(customCandidates)+len(managedCandidates))
		for _, upstream := range customCandidates {
			merged = appendUniqueUpstream(merged, upstream)
		}
		for _, upstream := range managedCandidates {
			merged = appendUniqueUpstream(merged, upstream)
		}
		return merged, nil
	}
}

func (p *Proxy) do(ctx context.Context, upstream Upstream, request ForwardRequest) (*ProxyResponse, error) {
	targetURL, err := resolveURL(upstream.BaseURL, request.Path)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, request.Method, targetURL, bytes.NewReader(request.Body))
	if err != nil {
		return nil, err
	}

	copyRequestHeaders(req.Header, request.Header)
	req.Header.Set("Authorization", "Bearer "+upstream.APIKey)
	if len(request.Body) > 0 {
		req.Header.Set("Content-Length", fmt.Sprintf("%d", len(request.Body)))
	}

	resp, err := p.client.Do(req)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, fmt.Errorf("upstream %s timeout: %w", upstream.Name, err)
		}
		return nil, fmt.Errorf("upstream %s request failed: %w", upstream.Name, err)
	}

	return &ProxyResponse{
		StatusCode: resp.StatusCode,
		Header:     resp.Header.Clone(),
		Body:       resp.Body,
		Upstream:   upstream,
	}, nil
}

func resolveURL(base, path string) (string, error) {
	parsed, err := url.Parse(base)
	if err != nil {
		return "", err
	}

	basePath := strings.TrimRight(parsed.Path, "/")
	if strings.HasSuffix(basePath, "/v1") && strings.HasPrefix(path, "/v1/") {
		path = strings.TrimPrefix(path, "/v1")
	}

	parsed.Path = basePath + path
	return parsed.String(), nil
}

func splitSupportedModels(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	items := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			items = append(items, part)
		}
	}
	return items
}

func CopyResponseHeaders(dst, src http.Header) {
	for key, values := range src {
		switch http.CanonicalHeaderKey(key) {
		case "Content-Length", "Transfer-Encoding", "Connection", "Content-Encoding":
			continue
		}
		for _, value := range values {
			dst.Add(key, value)
		}
	}
}

func copyRequestHeaders(dst, src http.Header) {
	for key, values := range src {
		switch http.CanonicalHeaderKey(key) {
		case "Authorization", "Host", "Content-Length":
			continue
		}
		for _, value := range values {
			dst.Add(key, value)
		}
	}
	if dst.Get("Content-Type") == "" {
		dst.Set("Content-Type", "application/json")
	}
}

func IsStreamingResponse(header http.Header) bool {
	return strings.Contains(strings.ToLower(header.Get("Content-Type")), "text/event-stream")
}

func StreamResponse(w http.ResponseWriter, body io.Reader) error {
	flusher, ok := w.(http.Flusher)
	if !ok {
		return errors.New("response writer does not support flushing")
	}

	buffer := make([]byte, 32*1024)
	for {
		n, err := body.Read(buffer)
		if n > 0 {
			if _, writeErr := w.Write(buffer[:n]); writeErr != nil {
				return writeErr
			}
			flusher.Flush()
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
	}
}

func WriteJSONError(w http.ResponseWriter, status int, errType, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]any{
			"message": message,
			"type":    errType,
		},
	})
}
