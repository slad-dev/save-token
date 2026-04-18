package handler

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"agent-gateway/internal/gateway"
	"agent-gateway/internal/store"
)

type updateUserRequest struct {
	Name     string `json:"name"`
	Plan     string `json:"plan"`
	Role     string `json:"role"`
	Balance  int64  `json:"balance"`
	IsActive bool   `json:"is_active"`
}

func (h *AdminHandler) UpdateUser(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPatch {
		gateway.WriteJSONError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		return
	}
	if _, ok := h.requireAdmin(w, r); !ok {
		return
	}

	userID, err := strconv.ParseUint(strings.TrimSpace(r.PathValue("userID")), 10, 64)
	if err != nil || userID == 0 {
		gateway.WriteJSONError(w, http.StatusBadRequest, "invalid_request_error", "invalid user id")
		return
	}

	var payload updateUserRequest
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		gateway.WriteJSONError(w, http.StatusBadRequest, "invalid_request_error", "request body must be valid JSON")
		return
	}

	user, err := h.store.UpdateUserAdmin(r.Context(), store.UpdateUserAdminInput{
		UserID:   uint(userID),
		Name:     payload.Name,
		Plan:     payload.Plan,
		Role:     payload.Role,
		Balance:  payload.Balance,
		IsActive: payload.IsActive,
	})
	if err != nil {
		gateway.WriteJSONError(w, http.StatusInternalServerError, "server_error", "failed to update user")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"id":         user.ID,
		"name":       user.Name,
		"email":      stringValue(user.Email),
		"role":       user.Role,
		"plan":       user.Plan,
		"balance":    user.Balance,
		"is_active":  user.IsActive,
		"updated_at": user.UpdatedAt,
	})
}
