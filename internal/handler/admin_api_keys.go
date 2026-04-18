package handler

import (
	"encoding/json"
	"net/http"
	"strings"

	"agent-gateway/internal/auth"
	"agent-gateway/internal/gateway"
	"agent-gateway/internal/store"
)

type createAPIKeyRequest struct {
	ProjectID      uint   `json:"project_id"`
	Name           string `json:"name"`
	AllowWebSearch bool   `json:"allow_web_search"`
}

func (h *AdminHandler) CreateAPIKey(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		gateway.WriteJSONError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		return
	}

	principal, ok := auth.PrincipalFromContext(r.Context())
	if !ok {
		gateway.WriteJSONError(w, http.StatusUnauthorized, "invalid_api_key", "authentication required")
		return
	}

	var payload createAPIKeyRequest
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		gateway.WriteJSONError(w, http.StatusBadRequest, "invalid_request_error", "request body must be valid JSON")
		return
	}

	if payload.ProjectID == 0 || strings.TrimSpace(payload.Name) == "" {
		gateway.WriteJSONError(w, http.StatusBadRequest, "invalid_request_error", "project_id and name are required")
		return
	}

	if _, err := h.store.FindProjectByUser(r.Context(), principal.User.ID, payload.ProjectID); err != nil {
		gateway.WriteJSONError(w, http.StatusNotFound, "not_found", "project not found")
		return
	}

	apiKey, err := h.store.CreateAPIKey(r.Context(), store.CreateAPIKeyInput{
		UserID:         principal.User.ID,
		ProjectID:      payload.ProjectID,
		Name:           payload.Name,
		AllowWebSearch: payload.AllowWebSearch,
	})
	if err != nil {
		gateway.WriteJSONError(w, http.StatusInternalServerError, "server_error", "failed to create api key")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"id":               apiKey.ID,
		"name":             apiKey.Name,
		"key":              apiKey.Key,
		"key_preview":      maskAPIKey(apiKey.Key),
		"project_id":       apiKey.ProjectID,
		"allow_web_search": apiKey.AllowWebSearch,
		"is_active":        apiKey.IsActive,
		"created_at":       apiKey.CreatedAt,
		"updated_at":       apiKey.UpdatedAt,
	})
}
