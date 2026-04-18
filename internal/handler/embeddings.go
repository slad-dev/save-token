package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"agent-gateway/internal/auth"
	"agent-gateway/internal/billing"
	"agent-gateway/internal/gateway"
	"agent-gateway/internal/store"
)

type EmbeddingsHandler struct {
	proxy   *gateway.Proxy
	billing *billing.Service
}

type embeddingsRequest struct {
	Model string `json:"model"`
}

func NewEmbeddingsHandler(proxy *gateway.Proxy, billingService *billing.Service) *EmbeddingsHandler {
	return &EmbeddingsHandler{
		proxy:   proxy,
		billing: billingService,
	}
}

func (h *EmbeddingsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		gateway.WriteJSONError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		gateway.WriteJSONError(w, http.StatusBadRequest, "invalid_request_error", "failed to read request body")
		return
	}

	var payload embeddingsRequest
	if err := json.Unmarshal(body, &payload); err != nil {
		gateway.WriteJSONError(w, http.StatusBadRequest, "invalid_request_error", "request body must be valid JSON")
		return
	}
	if payload.Model == "" {
		gateway.WriteJSONError(w, http.StatusBadRequest, "invalid_request_error", "model is required")
		return
	}
	startedAt := time.Now()
	principal, _ := auth.PrincipalFromContext(r.Context())
	requestID := fmt.Sprintf("req_%d", startedAt.UnixNano())

	response, err := h.proxy.Forward(r.Context(), gateway.ForwardRequest{
		Path:   "/v1/embeddings",
		Model:  payload.Model,
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
		if h.billing != nil && principal != nil {
			_ = h.billing.RecordUsage(r.Context(), billing.RecordInput{
				Principal:          principal,
				RequestID:          requestID,
				Endpoint:           "/v1/embeddings",
				Model:              payload.Model,
				OriginalModel:      payload.Model,
				UpstreamName:       "",
				StatusCode:         status,
				Stream:             false,
				ToolUsed:           false,
				CacheHit:           false,
				CompressionApplied: false,
				OriginalInputChars: len(body),
				FinalInputChars:    len(body),
				DurationMs:         time.Since(startedAt).Milliseconds(),
				ResponseBody:       nil,
				RequestKind:        "embedding",
				ErrorMessage:       err.Error(),
			})
		}
		gateway.WriteJSONError(w, status, "upstream_error", err.Error())
		return
	}
	defer response.Body.Close()

	gateway.CopyResponseHeaders(w.Header(), response.Header)
	w.WriteHeader(response.StatusCode)
	responseBody, _ := io.ReadAll(response.Body)
	_, _ = w.Write(responseBody)

	if h.billing != nil && principal != nil {
		if err := h.billing.RecordUsage(r.Context(), billing.RecordInput{
			Principal:          principal,
			RequestID:          requestID,
			Endpoint:           "/v1/embeddings",
			Model:              payload.Model,
			OriginalModel:      payload.Model,
			UpstreamName:       response.Upstream.Name,
			StatusCode:         response.StatusCode,
			Stream:             false,
			ToolUsed:           false,
			CacheHit:           false,
			CompressionApplied: false,
			OriginalInputChars: len(body),
			FinalInputChars:    len(body),
			DurationMs:         time.Since(startedAt).Milliseconds(),
			ResponseBody:       responseBody,
			RequestKind:        "embedding",
		}); err != nil && !errors.Is(err, store.ErrInsufficientBalance) {
			return
		}
	}
}
