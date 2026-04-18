package handler

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"agent-gateway/internal/gateway"
	"agent-gateway/internal/store"
)

type createManagedModelRequest struct {
	GroupName       string `json:"group_name"`
	DisplayName     string `json:"display_name"`
	Description     string `json:"description"`
	ModelIdentifier string `json:"model_identifier"`
	Provider        string `json:"provider"`
	Icon            string `json:"icon"`
	SourceType      string `json:"source_type"`
	BaseURL         string `json:"base_url"`
	APIKey          string `json:"api_key"`
	RouteStrategy   string `json:"route_strategy"`
	SortOrder       int    `json:"sort_order"`
}

type updateManagedModelStatusRequest struct {
	Enabled bool `json:"enabled"`
}

type updateManagedModelRequest struct {
	GroupName       string `json:"group_name"`
	DisplayName     string `json:"display_name"`
	Description     string `json:"description"`
	ModelIdentifier string `json:"model_identifier"`
	Provider        string `json:"provider"`
	Icon            string `json:"icon"`
	SourceType      string `json:"source_type"`
	BaseURL         string `json:"base_url"`
	APIKey          string `json:"api_key"`
	RouteStrategy   string `json:"route_strategy"`
	SortOrder       int    `json:"sort_order"`
}

func (h *AdminHandler) ListManagedModels(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		gateway.WriteJSONError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		return
	}

	models, err := h.store.ListManagedModels(r.Context())
	if err != nil {
		gateway.WriteJSONError(w, http.StatusInternalServerError, "server_error", "failed to query managed models")
		return
	}

	data := make([]map[string]any, 0, len(models))
	for _, model := range models {
		data = append(data, map[string]any{
			"id":               model.ID,
			"group_name":       model.GroupName,
			"display_name":     model.DisplayName,
			"description":      model.Description,
			"model_identifier": model.ModelIdentifier,
			"provider":         model.Provider,
			"icon":             model.Icon,
			"source_type":      model.SourceType,
			"base_url":         model.BaseURL,
			"key_preview":      maskAPIKey(model.APIKey),
			"route_strategy":   model.RouteStrategy,
			"sort_order":       model.SortOrder,
			"enabled":          model.Enabled,
			"created_at":       model.CreatedAt,
			"updated_at":       model.UpdatedAt,
		})
	}

	writeListResponse(w, data, int64(len(data)), store.ListOptions{Limit: len(data), Offset: 0})
}

func (h *AdminHandler) CreateManagedModel(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		gateway.WriteJSONError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		return
	}

	var payload createManagedModelRequest
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		gateway.WriteJSONError(w, http.StatusBadRequest, "invalid_request_error", "request body must be valid JSON")
		return
	}

	if strings.TrimSpace(payload.DisplayName) == "" || strings.TrimSpace(payload.ModelIdentifier) == "" || strings.TrimSpace(payload.BaseURL) == "" || strings.TrimSpace(payload.APIKey) == "" {
		gateway.WriteJSONError(w, http.StatusBadRequest, "invalid_request_error", "display_name, model_identifier, base_url and api_key are required")
		return
	}

	model, err := h.store.CreateManagedModel(r.Context(), store.CreateManagedModelInput{
		GroupName:       payload.GroupName,
		DisplayName:     payload.DisplayName,
		Description:     payload.Description,
		ModelIdentifier: payload.ModelIdentifier,
		Provider:        payload.Provider,
		Icon:            payload.Icon,
		SourceType:      payload.SourceType,
		BaseURL:         payload.BaseURL,
		APIKey:          payload.APIKey,
		RouteStrategy:   payload.RouteStrategy,
		SortOrder:       payload.SortOrder,
	})
	if err != nil {
		gateway.WriteJSONError(w, http.StatusInternalServerError, "server_error", "failed to create managed model")
		return
	}
	if h.registry != nil {
		_ = h.registry.Load(r.Context(), h.store)
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"id":               model.ID,
		"group_name":       model.GroupName,
		"display_name":     model.DisplayName,
		"description":      model.Description,
		"model_identifier": model.ModelIdentifier,
		"provider":         model.Provider,
		"icon":             model.Icon,
		"source_type":      model.SourceType,
		"base_url":         model.BaseURL,
		"key_preview":      maskAPIKey(model.APIKey),
		"route_strategy":   model.RouteStrategy,
		"sort_order":       model.SortOrder,
		"enabled":          model.Enabled,
		"created_at":       model.CreatedAt,
		"updated_at":       model.UpdatedAt,
	})
}

func (h *AdminHandler) UpdateManagedModel(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPatch {
		gateway.WriteJSONError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		return
	}

	modelIDValue := r.PathValue("modelID")
	modelID64, err := strconv.ParseUint(modelIDValue, 10, 64)
	if err != nil || modelID64 == 0 {
		gateway.WriteJSONError(w, http.StatusBadRequest, "invalid_request_error", "model id is invalid")
		return
	}

	var payload updateManagedModelRequest
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		gateway.WriteJSONError(w, http.StatusBadRequest, "invalid_request_error", "request body must be valid JSON")
		return
	}
	if strings.TrimSpace(payload.DisplayName) == "" || strings.TrimSpace(payload.ModelIdentifier) == "" || strings.TrimSpace(payload.BaseURL) == "" {
		gateway.WriteJSONError(w, http.StatusBadRequest, "invalid_request_error", "display_name, model_identifier and base_url are required")
		return
	}

	model, err := h.store.UpdateManagedModel(r.Context(), store.UpdateManagedModelInput{
		ID:              uint(modelID64),
		GroupName:       payload.GroupName,
		DisplayName:     payload.DisplayName,
		Description:     payload.Description,
		ModelIdentifier: payload.ModelIdentifier,
		Provider:        payload.Provider,
		Icon:            payload.Icon,
		SourceType:      payload.SourceType,
		BaseURL:         payload.BaseURL,
		APIKey:          payload.APIKey,
		RouteStrategy:   payload.RouteStrategy,
		SortOrder:       payload.SortOrder,
	})
	if err != nil {
		gateway.WriteJSONError(w, http.StatusNotFound, "not_found", "managed model not found")
		return
	}
	if h.registry != nil {
		_ = h.registry.Load(r.Context(), h.store)
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"id": model.ID,
	})
}

func (h *AdminHandler) UpdateManagedModelStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPatch {
		gateway.WriteJSONError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		return
	}

	modelIDValue := r.PathValue("modelID")
	modelID64, err := strconv.ParseUint(modelIDValue, 10, 64)
	if err != nil || modelID64 == 0 {
		gateway.WriteJSONError(w, http.StatusBadRequest, "invalid_request_error", "model id is invalid")
		return
	}

	var payload updateManagedModelStatusRequest
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		gateway.WriteJSONError(w, http.StatusBadRequest, "invalid_request_error", "request body must be valid JSON")
		return
	}

	model, err := h.store.SetManagedModelEnabled(r.Context(), uint(modelID64), payload.Enabled)
	if err != nil {
		gateway.WriteJSONError(w, http.StatusNotFound, "not_found", "managed model not found")
		return
	}
	if h.registry != nil {
		_ = h.registry.Load(r.Context(), h.store)
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"id":      model.ID,
		"enabled": model.Enabled,
	})
}

func (h *AdminHandler) DeleteManagedModel(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		gateway.WriteJSONError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		return
	}

	modelIDValue := r.PathValue("modelID")
	modelID64, err := strconv.ParseUint(modelIDValue, 10, 64)
	if err != nil || modelID64 == 0 {
		gateway.WriteJSONError(w, http.StatusBadRequest, "invalid_request_error", "model id is invalid")
		return
	}

	if err := h.store.DeleteManagedModel(r.Context(), uint(modelID64)); err != nil {
		gateway.WriteJSONError(w, http.StatusNotFound, "not_found", "managed model not found")
		return
	}
	if h.registry != nil {
		_ = h.registry.Load(r.Context(), h.store)
	}

	w.WriteHeader(http.StatusNoContent)
}
