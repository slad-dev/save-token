package handler

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"agent-gateway/internal/auth"
	"agent-gateway/internal/gateway"
	"agent-gateway/internal/store"
)

type ModelsHandler struct {
	proxy *gateway.Proxy
	store *store.SQLiteStore
}

func NewModelsHandler(proxy *gateway.Proxy, st *store.SQLiteStore) *ModelsHandler {
	return &ModelsHandler{proxy: proxy, store: st}
}

func (h *ModelsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		gateway.WriteJSONError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		return
	}

	models := h.proxy.Models(r.Context())
	if shouldProxyModels(models) {
		response, err := h.proxy.Forward(r.Context(), gateway.ForwardRequest{
			Path:   "/v1/models",
			Model:  "",
			Method: http.MethodGet,
			Header: r.Header.Clone(),
		})
		if err == nil {
			defer response.Body.Close()
			gateway.CopyResponseHeaders(w.Header(), response.Header)
			w.WriteHeader(response.StatusCode)
			body, _ := io.ReadAll(response.Body)
			_, _ = w.Write(body)
			return
		}
		if !errors.Is(err, gateway.ErrNoUpstreamAvailable) {
			gateway.WriteJSONError(w, http.StatusBadGateway, "upstream_error", err.Error())
			return
		}
	}
	data := make([]map[string]any, 0, len(models))
	now := time.Now().Unix()
	managedModels := map[string]store.ManagedModel{}
	if h.store != nil {
		if items, err := h.store.ListManagedModelsMap(r.Context()); err == nil {
			managedModels = items
		}
	}
	owner := "agent-gateway"
	if principal, ok := auth.PrincipalFromContext(r.Context()); ok && principal.APIKey.Project != nil {
		owner = principal.APIKey.Project.Name
	}
	for _, model := range models {
		displayName := model
		icon := "cpu"
		provider := "custom"
		sourceType := "proxy"
		if managed, ok := managedModels[model]; ok {
			displayName = managed.DisplayName
			icon = managed.Icon
			provider = managed.Provider
			sourceType = managed.SourceType
		}
		data = append(data, map[string]any{
			"id":           model,
			"object":       "model",
			"created":      now,
			"owned_by":     owner,
			"display_name": displayName,
			"icon":         icon,
			"provider":     provider,
			"source_type":  sourceType,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"object": "list",
		"data":   data,
	})
}

func shouldProxyModels(models []string) bool {
	if len(models) == 0 {
		return true
	}
	for _, model := range models {
		if model == "*" {
			return true
		}
	}
	return false
}
