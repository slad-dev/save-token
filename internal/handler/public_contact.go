package handler

import (
	"encoding/json"
	"net/http"
	"strings"

	"agent-gateway/internal/gateway"
	"agent-gateway/internal/store"
)

type PublicContactHandler struct {
	store *store.SQLiteStore
}

type createContactRequest struct {
	Name    string `json:"name"`
	Email   string `json:"email"`
	Company string `json:"company"`
	Role    string `json:"role"`
	Message string `json:"message"`
	Source  string `json:"source"`
}

func NewPublicContactHandler(st *store.SQLiteStore) *PublicContactHandler {
	return &PublicContactHandler{store: st}
}

func (h *PublicContactHandler) Create(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		gateway.WriteJSONError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		return
	}

	var payload createContactRequest
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		gateway.WriteJSONError(w, http.StatusBadRequest, "invalid_request_error", "request body must be valid JSON")
		return
	}

	if strings.TrimSpace(payload.Name) == "" || strings.TrimSpace(payload.Email) == "" || strings.TrimSpace(payload.Message) == "" {
		gateway.WriteJSONError(w, http.StatusBadRequest, "invalid_request_error", "name, email and message are required")
		return
	}

	if _, err := h.store.CreateContactRequest(r.Context(), store.CreateContactRequestInput{
		Name:    payload.Name,
		Email:   payload.Email,
		Company: payload.Company,
		Role:    payload.Role,
		Message: payload.Message,
		Source:  payload.Source,
	}); err != nil {
		gateway.WriteJSONError(w, http.StatusInternalServerError, "server_error", "failed to submit contact request")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"ok": true,
	})
}
