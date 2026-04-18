package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"agent-gateway/internal/auth"
	"agent-gateway/internal/gateway"
	"agent-gateway/internal/store"

	"gorm.io/gorm"
)

type createSourceRequest struct {
	ProjectID       uint   `json:"project_id"`
	Name            string `json:"name"`
	Provider        string `json:"provider"`
	BaseURL         string `json:"base_url"`
	APIKey          string `json:"api_key"`
	SupportedModels string `json:"supported_models"`
}

func (h *AdminHandler) ListSources(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		gateway.WriteJSONError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		return
	}

	principal, ok := auth.PrincipalFromContext(r.Context())
	if !ok {
		gateway.WriteJSONError(w, http.StatusUnauthorized, "invalid_api_key", "authentication required")
		return
	}

	records, err := h.store.LoadUpstreams(r.Context())
	if err != nil {
		gateway.WriteJSONError(w, http.StatusInternalServerError, "server_error", "failed to query managed sources")
		return
	}

	customSources, err := h.store.ListSourcesByUser(r.Context(), principal.User.ID)
	if err != nil {
		gateway.WriteJSONError(w, http.StatusInternalServerError, "server_error", "failed to query user sources")
		return
	}
	managedModels, _ := h.store.ListManagedModelsMap(r.Context())

	data := make([]map[string]any, 0, len(records)+len(customSources))
	for _, record := range records {
		managedModelItems := make([]map[string]any, 0, len(record.SupportedModels))
		for _, identifier := range record.SupportedModels {
			if model, ok := managedModels[identifier]; ok {
				managedModelItems = append(managedModelItems, map[string]any{
					"id":               model.ID,
					"display_name":     model.DisplayName,
					"model_identifier": model.ModelIdentifier,
					"icon":             model.Icon,
					"provider":         model.Provider,
				})
			}
		}
		data = append(data, map[string]any{
			"id":               "managed:" + record.Name,
			"name":             record.Name,
			"kind":             "managed",
			"provider":         "platform",
			"project_id":       nil,
			"project_name":     "Platform Shared",
			"base_url":         record.BaseURL,
			"key_preview":      maskAPIKey(record.APIKey),
			"supported_models": record.SupportedModels,
			"managed_models":   managedModelItems,
			"is_active":        record.Enabled,
		})
	}

	for _, source := range customSources {
		data = append(data, map[string]any{
			"id":               source.ID,
			"name":             source.Name,
			"kind":             source.Kind,
			"provider":         source.Provider,
			"project_id":       source.ProjectID,
			"project_name":     source.Project.Name,
			"base_url":         source.BaseURL,
			"key_preview":      maskAPIKey(source.APIKey),
			"supported_models": parseSupportedModels(source.SupportedModels),
			"managed_models":   []map[string]any{},
			"is_active":        source.IsActive,
			"created_at":       source.CreatedAt,
			"updated_at":       source.UpdatedAt,
		})
	}

	writeListResponse(w, data, int64(len(data)), store.ListOptions{Limit: len(data), Offset: 0})
}

func (h *AdminHandler) CreateSource(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		gateway.WriteJSONError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		return
	}

	principal, ok := auth.PrincipalFromContext(r.Context())
	if !ok {
		gateway.WriteJSONError(w, http.StatusUnauthorized, "invalid_api_key", "authentication required")
		return
	}

	var payload createSourceRequest
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		gateway.WriteJSONError(w, http.StatusBadRequest, "invalid_request_error", "request body must be valid JSON")
		return
	}

	if strings.TrimSpace(payload.Name) == "" || strings.TrimSpace(payload.BaseURL) == "" || strings.TrimSpace(payload.APIKey) == "" || payload.ProjectID == 0 {
		gateway.WriteJSONError(w, http.StatusBadRequest, "invalid_request_error", "project_id, name, base_url and api_key are required")
		return
	}

	if _, err := h.store.FindProjectByUser(r.Context(), principal.User.ID, payload.ProjectID); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			gateway.WriteJSONError(w, http.StatusBadRequest, "invalid_request_error", "project not found")
			return
		}
		gateway.WriteJSONError(w, http.StatusInternalServerError, "server_error", "failed to validate project")
		return
	}

	source, err := h.store.CreateSource(r.Context(), store.CreateSourceInput{
		UserID:          principal.User.ID,
		ProjectID:       payload.ProjectID,
		Name:            payload.Name,
		Provider:        payload.Provider,
		BaseURL:         payload.BaseURL,
		APIKey:          payload.APIKey,
		SupportedModels: splitAndTrim(payload.SupportedModels),
	})
	if err != nil {
		gateway.WriteJSONError(w, http.StatusInternalServerError, "server_error", "failed to create source")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"id":               source.ID,
		"name":             source.Name,
		"kind":             source.Kind,
		"provider":         source.Provider,
		"project_id":       source.ProjectID,
		"base_url":         source.BaseURL,
		"key_preview":      maskAPIKey(source.APIKey),
		"supported_models": parseSupportedModels(source.SupportedModels),
		"is_active":        source.IsActive,
		"created_at":       source.CreatedAt,
		"updated_at":       source.UpdatedAt,
	})
}

func splitAndTrim(value string) []string {
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

func parseSupportedModels(value string) []string {
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
