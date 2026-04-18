package billing

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"

	"agent-gateway/internal/auth"
	"agent-gateway/internal/config"
	"agent-gateway/internal/store"
)

type Service struct {
	cfg   config.BillingConfig
	store *store.SQLiteStore
}

type Usage struct {
	PromptTokens     int64
	CompletionTokens int64
	TotalTokens      int64
}

type RecordInput struct {
	Principal          *auth.Principal
	RequestID          string
	Endpoint           string
	Model              string
	OriginalModel      string
	UpstreamName       string
	StatusCode         int
	Stream             bool
	ToolUsed           bool
	CacheHit           bool
	CompressionApplied bool
	OriginalInputChars int
	FinalInputChars    int
	DurationMs         int64
	ResponseBody       []byte
	RequestKind        string
	ErrorMessage       string
}

func NewService(cfg config.BillingConfig, st *store.SQLiteStore) *Service {
	return &Service{cfg: cfg, store: st}
}

func (s *Service) RecordUsage(ctx context.Context, input RecordInput) error {
	if input.Principal == nil {
		return nil
	}

	usage := extractUsage(input.ResponseBody, input.Stream)
	success := input.StatusCode >= 200 && input.StatusCode < 300
	actualCost := int64(0)
	if s.cfg.Enabled && success && !input.CacheHit {
		actualCost = s.calculateCharge(input.RequestKind, input.Model, usage, input.ToolUsed)
	}
	baselineTokens := s.estimateBaselineTokens(usage.TotalTokens, input)
	savedTokens := baselineTokens - usage.TotalTokens
	if input.CacheHit {
		savedTokens = baselineTokens
	}
	if savedTokens < 0 {
		savedTokens = 0
	}

	baselineCost := int64(0)
	if s.cfg.Enabled && success {
		baselineUsage := usage
		baselineUsage.TotalTokens = baselineTokens
		baselineCost = s.calculateCharge(input.RequestKind, firstNonEmpty(input.OriginalModel, input.Model), baselineUsage, input.ToolUsed)
	}
	savedCost := baselineCost - actualCost
	if savedCost < 0 {
		savedCost = 0
	}

	log := store.UsageLog{
		RequestID:        input.RequestID,
		UserID:           input.Principal.User.ID,
		APIKeyID:         input.Principal.APIKey.ID,
		Endpoint:         input.Endpoint,
		Model:            input.Model,
		UpstreamName:     input.UpstreamName,
		StatusCode:       input.StatusCode,
		PromptTokens:     usage.PromptTokens,
		CompletionTokens: usage.CompletionTokens,
		TotalTokens:      usage.TotalTokens,
		BaselineTokens:   baselineTokens,
		SavedTokens:      savedTokens,
		CreditsCharged:   actualCost,
		BaselineCost:     baselineCost,
		SavedCost:        savedCost,
		RequestKind:      input.RequestKind,
		Stream:           input.Stream,
		ToolUsed:         input.ToolUsed,
		CacheHit:         input.CacheHit,
		DurationMs:       input.DurationMs,
		Success:          success,
		ErrorMessage:     input.ErrorMessage,
	}

	if err := s.store.RecordUsageAndDeduct(ctx, log); err != nil {
		if errors.Is(err, store.ErrInsufficientBalance) {
			return err
		}
		return err
	}

	return nil
}

func (s *Service) estimateBaselineTokens(actualTokens int64, input RecordInput) int64 {
	if actualTokens <= 0 {
		return 0
	}
	if input.CacheHit {
		if input.CompressionApplied && input.FinalInputChars > 0 && input.OriginalInputChars > input.FinalInputChars {
			return scaleTokensByChars(actualTokens, input.OriginalInputChars, input.FinalInputChars)
		}
		return actualTokens
	}
	if input.CompressionApplied && input.FinalInputChars > 0 && input.OriginalInputChars > input.FinalInputChars {
		return scaleTokensByChars(actualTokens, input.OriginalInputChars, input.FinalInputChars)
	}
	return actualTokens
}

func scaleTokensByChars(actualTokens int64, originalChars, finalChars int) int64 {
	if actualTokens <= 0 || originalChars <= 0 || finalChars <= 0 || originalChars <= finalChars {
		return actualTokens
	}
	scaled := (actualTokens * int64(originalChars)) / int64(finalChars)
	if scaled < actualTokens {
		return actualTokens
	}
	return scaled
}

func (s *Service) calculateCharge(kind, model string, usage Usage, toolUsed bool) int64 {
	totalTokens := usage.TotalTokens
	charge := int64(0)
	if totalTokens <= 0 {
		charge = s.cfg.MinimumRequestCharge
	} else {
		per1k := s.resolveRate(kind, model)
		charge = (totalTokens*per1k + 999) / 1000
		if charge < s.cfg.MinimumRequestCharge {
			charge = s.cfg.MinimumRequestCharge
		}
	}

	if toolUsed {
		charge += s.cfg.WebSearchSurcharge
	}
	return charge
}

func (s *Service) resolveRate(kind, model string) int64 {
	for _, pricing := range s.cfg.ModelPricing {
		if !matchesPattern(pricing.ModelPattern, model) {
			continue
		}
		if pricing.RequestKind != "" && pricing.RequestKind != kind {
			continue
		}
		return pricing.Per1KTokens
	}

	if kind == "embedding" {
		return s.cfg.DefaultEmbeddingPer1K
	}
	return s.cfg.DefaultChatPer1K
}

func extractUsage(body []byte, stream bool) Usage {
	if len(body) == 0 {
		return Usage{}
	}
	if usage := extractUsageFromJSON(body); usage.TotalTokens > 0 || usage.PromptTokens > 0 || usage.CompletionTokens > 0 {
		return usage
	}
	if usage := extractUsageFromSSE(body); usage.TotalTokens > 0 || usage.PromptTokens > 0 || usage.CompletionTokens > 0 {
		return usage
	}
	if stream {
		return Usage{}
	}
	return Usage{}
}

func extractUsageFromJSON(body []byte) Usage {
	var payload struct {
		Usage struct {
			PromptTokens     int64 `json:"prompt_tokens"`
			CompletionTokens int64 `json:"completion_tokens"`
			TotalTokens      int64 `json:"total_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return Usage{}
	}
	return Usage(payload.Usage)
}

func extractUsageFromSSE(body []byte) Usage {
	scanner := bufio.NewScanner(bytes.NewReader(body))
	scanner.Buffer(make([]byte, 0, 64*1024), 2*1024*1024)

	usage := Usage{}
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "" || data == "[DONE]" {
			continue
		}
		parsed := extractUsageFromJSON([]byte(data))
		if parsed.TotalTokens > 0 || parsed.PromptTokens > 0 || parsed.CompletionTokens > 0 {
			usage = parsed
		}
	}
	return usage
}

func matchesPattern(pattern, value string) bool {
	if pattern == "*" {
		return true
	}
	if !strings.Contains(pattern, "*") {
		return pattern == value
	}
	prefix := strings.TrimSuffix(pattern, "*")
	return strings.HasPrefix(value, prefix)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
