package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"agent-gateway/internal/auth"
	"agent-gateway/internal/billing"
	"agent-gateway/internal/cache"
	"agent-gateway/internal/gateway"
	"agent-gateway/internal/intelligent"
	"agent-gateway/internal/observe"
	"agent-gateway/internal/store"
)

type ChatCompletionsHandler struct {
	proxy        *gateway.Proxy
	preprocessor *intelligent.Preprocessor
	billing      *billing.Service
	trace        *observe.RouteTraceService
	cache        *cache.ExactCache
}

func NewChatCompletionsHandler(proxy *gateway.Proxy, preprocessor *intelligent.Preprocessor, billingService *billing.Service, traceService *observe.RouteTraceService, cacheService *cache.ExactCache) *ChatCompletionsHandler {
	return &ChatCompletionsHandler{
		proxy:        proxy,
		preprocessor: preprocessor,
		billing:      billingService,
		trace:        traceService,
		cache:        cacheService,
	}
}

func (h *ChatCompletionsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		gateway.WriteJSONError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		gateway.WriteJSONError(w, http.StatusBadRequest, "invalid_request_error", "failed to read request body")
		return
	}

	var model string
	usedTooling := false
	startedAt := time.Now()
	principal, _ := auth.PrincipalFromContext(r.Context())
	requestID := fmt.Sprintf("req_%d", startedAt.UnixNano())
	var prepareResult *intelligent.PrepareResult
	requestHash := ""
	strictPrivacy := isStrictPrivacyMode(principal)
	if h.preprocessor != nil {
		result, err := h.preprocessor.PrepareChatRequest(r.Context(), body)
		if err != nil {
			var tooLarge *intelligent.InputTooLargeError
			if errors.As(err, &tooLarge) {
				gateway.WriteJSONError(w, http.StatusRequestEntityTooLarge, "context_too_large", err.Error())
				return
			}
			gateway.WriteJSONError(w, http.StatusBadRequest, "invalid_request_error", err.Error())
			return
		}
		prepareResult = result
		body = result.Body
		model = result.Model
		usedTooling = result.UsedTooling
	} else {
		var payload struct {
			Model string `json:"model"`
		}
		if err := json.Unmarshal(body, &payload); err != nil {
			gateway.WriteJSONError(w, http.StatusBadRequest, "invalid_request_error", "request body must be valid JSON")
			return
		}
		model = payload.Model
	}
	if model == "" {
		gateway.WriteJSONError(w, http.StatusBadRequest, "invalid_request_error", "model is required")
		return
	}

	semanticBestScore := 0.0
	semanticObserved := false
	semanticThreshold := 0.0
	if h.cache != nil {
		semanticThreshold = h.cache.SemanticThreshold()
	}

	if h.cache != nil && principal != nil && prepareResult != nil && !prepareResult.Stream && !strictPrivacy {
		requestHash, err = h.cache.BuildRequestHash(body)
		if err == nil {
			cached, cacheErr := h.cache.Lookup(r.Context(), principal.User.ID, "/v1/chat/completions", requestHash)
			if cacheErr == nil && cached != nil {
				if prepareResult != nil {
					prepareResult.DecisionReason = appendTraceReason(prepareResult.DecisionReason, "exact_cache_hit")
				}
				h.recordRouteTrace(r.Context(), requestID, principal, prepareResult, true)
				setTokenSavingHeaders(w.Header(), "exact-hit", 0, semanticThreshold)
				if cached.ContentType != "" {
					w.Header().Set("Content-Type", cached.ContentType)
				} else {
					w.Header().Set("Content-Type", "application/json")
				}
				w.WriteHeader(cached.StatusCode)
				_, _ = w.Write(cached.Body)
				h.recordUsage(r.Context(), requestID, principal, prepareResult, "exact-cache", cached.StatusCode, false, usedTooling, true, time.Since(startedAt).Milliseconds(), cached.Body, "")
				return
			}
		}

		semanticCached, semanticScore, semanticErr := h.cache.LookupSemantic(r.Context(), principal.User.ID, "/v1/chat/completions", model, prepareResult.QueryText)
		if semanticErr == nil {
			semanticObserved = true
			semanticBestScore = semanticScore
		}
		if semanticErr == nil && semanticCached != nil {
			if prepareResult != nil {
				prepareResult.DecisionReason = appendTraceReason(
					prepareResult.DecisionReason,
					fmt.Sprintf("semantic_cache_hit score=%.3f threshold=%.3f", semanticScore, semanticThreshold),
				)
			}
			h.recordRouteTrace(r.Context(), requestID, principal, prepareResult, true)
			setTokenSavingHeaders(w.Header(), "semantic-hit", semanticScore, semanticThreshold)
			if semanticCached.ContentType != "" {
				w.Header().Set("Content-Type", semanticCached.ContentType)
			} else {
				w.Header().Set("Content-Type", "application/json")
			}
			w.WriteHeader(semanticCached.StatusCode)
			_, _ = w.Write(semanticCached.Body)
			h.recordUsage(r.Context(), requestID, principal, prepareResult, "semantic-cache", semanticCached.StatusCode, false, usedTooling, true, time.Since(startedAt).Milliseconds(), semanticCached.Body, "")
			return
		}

		if semanticObserved && prepareResult != nil {
			prepareResult.DecisionReason = appendTraceReason(
				prepareResult.DecisionReason,
				fmt.Sprintf("semantic_cache_miss score=%.3f threshold=%.3f", semanticBestScore, semanticThreshold),
			)
		}
	}

	if prepareResult != nil {
		h.recordRouteTrace(r.Context(), requestID, principal, prepareResult, false)
	}

	response, err := h.proxy.Forward(r.Context(), gateway.ForwardRequest{
		Path:   "/v1/chat/completions",
		Model:  model,
		Method: http.MethodPost,
		Body:   body,
		Header: r.Header.Clone(),
		AllowRetryCodes: map[int]struct{}{
			http.StatusTooManyRequests:    {},
			http.StatusBadGateway:         {},
			http.StatusServiceUnavailable: {},
			http.StatusGatewayTimeout:     {},
		},
	})
	if err != nil {
		status := http.StatusBadGateway
		if errors.Is(err, context.DeadlineExceeded) {
			status = http.StatusGatewayTimeout
		}
		h.recordUsage(r.Context(), requestID, principal, prepareResult, "", status, false, usedTooling, false, time.Since(startedAt).Milliseconds(), nil, err.Error())
		gateway.WriteJSONError(w, status, "upstream_error", err.Error())
		return
	}
	defer response.Body.Close()

	gateway.CopyResponseHeaders(w.Header(), response.Header)
	shouldStreamToClient := prepareResult != nil && prepareResult.Stream && gateway.IsStreamingResponse(response.Header)
	if shouldStreamToClient {
		w.WriteHeader(response.StatusCode)
		var captured bytes.Buffer
		reader := io.TeeReader(response.Body, &captured)
		_ = gateway.StreamResponse(w, reader)
		h.recordUsage(r.Context(), requestID, principal, prepareResult, response.Upstream.Name, response.StatusCode, true, usedTooling, false, time.Since(startedAt).Milliseconds(), captured.Bytes(), "")
		return
	}

	responseBody, _ := io.ReadAll(response.Body)
	responseBody, contentType := normalizeBufferedResponse(responseBody, response.Header.Get("Content-Type"))
	setTokenSavingHeaders(w.Header(), "miss", semanticBestScore, semanticThreshold)
	if strings.TrimSpace(contentType) != "" {
		w.Header().Set("Content-Type", contentType)
	}
	w.WriteHeader(response.StatusCode)
	_, _ = w.Write(responseBody)
	if h.cache != nil && principal != nil && prepareResult != nil && !prepareResult.Stream && !strictPrivacy && requestHash != "" && response.StatusCode >= 200 && response.StatusCode < 300 {
		_ = h.cache.Store(r.Context(), principal.User.ID, "/v1/chat/completions", model, requestHash, prepareResult.QueryText, contentType, response.StatusCode, responseBody)
	}
	h.recordUsage(r.Context(), requestID, principal, prepareResult, response.Upstream.Name, response.StatusCode, false, usedTooling, false, time.Since(startedAt).Milliseconds(), responseBody, "")
}

func isStrictPrivacyMode(principal *auth.Principal) bool {
	if principal == nil || principal.APIKey.Project == nil {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(principal.APIKey.Project.PrivacyMode), store.PrivacyModeStrict)
}

func (h *ChatCompletionsHandler) recordUsage(ctx context.Context, requestID string, principal *auth.Principal, result *intelligent.PrepareResult, upstreamName string, statusCode int, stream bool, toolUsed bool, cacheHit bool, durationMs int64, body []byte, errorMessage string) {
	if h.billing == nil || principal == nil {
		return
	}
	model := ""
	originalModel := ""
	originalInputChars := 0
	finalInputChars := 0
	compressionApplied := false
	if result != nil {
		model = result.Model
		originalModel = result.OriginalModel
		originalInputChars = result.OriginalInputChars
		finalInputChars = result.EstimatedInputChars
		compressionApplied = result.CompressionApplied
	}
	if err := h.billing.RecordUsage(ctx, billing.RecordInput{
		Principal:          principal,
		RequestID:          requestID,
		Endpoint:           "/v1/chat/completions",
		Model:              model,
		OriginalModel:      originalModel,
		UpstreamName:       upstreamName,
		StatusCode:         statusCode,
		Stream:             stream,
		ToolUsed:           toolUsed,
		CacheHit:           cacheHit,
		CompressionApplied: compressionApplied,
		OriginalInputChars: originalInputChars,
		FinalInputChars:    finalInputChars,
		DurationMs:         durationMs,
		ResponseBody:       body,
		RequestKind:        "chat",
		ErrorMessage:       errorMessage,
	}); err != nil && !errors.Is(err, store.ErrInsufficientBalance) {
		return
	}
}

func (h *ChatCompletionsHandler) recordRouteTrace(ctx context.Context, requestID string, principal *auth.Principal, result *intelligent.PrepareResult, cacheHit bool) {
	if h.trace == nil || principal == nil || result == nil {
		return
	}
	_ = h.trace.Record(ctx, observe.RecordRouteTraceInput{
		RequestID:            requestID,
		Principal:            principal,
		Endpoint:             "/v1/chat/completions",
		OriginalModel:        result.OriginalModel,
		FinalModel:           result.Model,
		RouteTier:            result.RouteTier,
		IntentClass:          result.IntentClass,
		IntentConfidence:     result.IntentConfidence,
		EstimatedInputChars:  result.EstimatedInputChars,
		EstimatedInputTokens: result.EstimatedInputTokens,
		SearchApplied:        result.UsedTooling,
		CompressionApplied:   result.CompressionApplied,
		CacheHit:             cacheHit,
		DecisionReason:       result.DecisionReason,
		IntentReasons:        result.IntentReasons,
	})
}

func normalizeBufferedResponse(body []byte, contentType string) ([]byte, string) {
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 {
		return body, contentType
	}

	if json.Valid(trimmed) {
		return trimmed, "application/json"
	}

	if !strings.Contains(strings.ToLower(contentType), "text/event-stream") {
		return body, contentType
	}

	if payload, ok := extractJSONFromEventStream(trimmed); ok {
		return payload, "application/json"
	}

	return body, contentType
}

func extractJSONFromEventStream(body []byte) ([]byte, bool) {
	lines := bytes.Split(body, []byte{'\n'})
	candidates := make([][]byte, 0, len(lines))
	for _, rawLine := range lines {
		line := bytes.TrimSpace(rawLine)
		if !bytes.HasPrefix(line, []byte("data:")) {
			continue
		}
		payload := bytes.TrimSpace(bytes.TrimPrefix(line, []byte("data:")))
		if len(payload) == 0 || bytes.Equal(payload, []byte("[DONE]")) {
			continue
		}
		if json.Valid(payload) {
			candidates = append(candidates, append([]byte(nil), payload...))
		}
	}
	if len(candidates) == 0 {
		return nil, false
	}
	return candidates[len(candidates)-1], true
}

func appendTraceReason(base, extra string) string {
	base = strings.TrimSpace(base)
	extra = strings.TrimSpace(extra)
	switch {
	case base == "":
		return extra
	case extra == "":
		return base
	default:
		return base + "; " + extra
	}
}

func setTokenSavingHeaders(header http.Header, cacheStatus string, semanticScore, threshold float64) {
	if header == nil {
		return
	}
	if strings.TrimSpace(cacheStatus) != "" {
		header.Set("X-SaveToken-Cache", cacheStatus)
	}
	if semanticScore > 0 {
		header.Set("X-SaveToken-Semantic-Score", fmt.Sprintf("%.3f", semanticScore))
	}
	if threshold > 0 {
		header.Set("X-SaveToken-Semantic-Threshold", fmt.Sprintf("%.3f", threshold))
	}
}
