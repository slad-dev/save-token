package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"agent-gateway/internal/auth"
	"agent-gateway/internal/gateway"
	"agent-gateway/internal/store"

	"gorm.io/gorm"
)

type updateContactStatusRequest struct {
	Status string `json:"status"`
}

type addContactNoteRequest struct {
	Body string `json:"body"`
}

type updateContactOwnerRequest struct {
	Owner string `json:"owner"`
}

func (h *AdminHandler) ListContactRequests(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		gateway.WriteJSONError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		return
	}

	if _, ok := auth.PrincipalFromContext(r.Context()); !ok {
		gateway.WriteJSONError(w, http.StatusUnauthorized, "invalid_api_key", "authentication required")
		return
	}

	options := parseListOptions(r)
	activeStatus := strings.TrimSpace(r.URL.Query().Get("status"))
	rows, total, err := h.store.ListContactRequests(r.Context(), options, activeStatus)
	if err != nil {
		gateway.WriteJSONError(w, http.StatusInternalServerError, "server_error", "failed to query contact requests")
		return
	}
	summary, err := h.store.ContactRequestStatusSummary(r.Context())
	if err != nil {
		gateway.WriteJSONError(w, http.StatusInternalServerError, "server_error", "failed to summarize contact requests")
		return
	}

	data := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		data = append(data, map[string]any{
			"id":         row.ID,
			"name":       row.Name,
			"email":      row.Email,
			"company":    row.Company,
			"role":       row.Role,
			"owner":      row.Owner,
			"message":    row.Message,
			"status":     row.Status,
			"source":     row.Source,
			"created_at": row.CreatedAt,
			"updated_at": row.UpdatedAt,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"data": data,
		"summary": map[string]any{
			"total":     summary[store.ContactStatusNew] + summary[store.ContactStatusContacted] + summary[store.ContactStatusQualified] + summary[store.ContactStatusClosed],
			"new":       summary[store.ContactStatusNew],
			"contacted": summary[store.ContactStatusContacted],
			"qualified": summary[store.ContactStatusQualified],
			"closed":    summary[store.ContactStatusClosed],
		},
		"filters": map[string]any{
			"active": map[string]any{
				"status": strings.ToLower(strings.TrimSpace(activeStatus)),
			},
			"statuses": []string{
				store.ContactStatusNew,
				store.ContactStatusContacted,
				store.ContactStatusQualified,
				store.ContactStatusClosed,
			},
		},
		"pagination": map[string]any{
			"total":  total,
			"limit":  normalizeLimit(options.Limit),
			"offset": normalizeOffset(options.Offset),
		},
	})
}

func (h *AdminHandler) UpdateContactRequestStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPatch {
		gateway.WriteJSONError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		return
	}

	if _, ok := auth.PrincipalFromContext(r.Context()); !ok {
		gateway.WriteJSONError(w, http.StatusUnauthorized, "invalid_api_key", "authentication required")
		return
	}

	requestID64, err := strconv.ParseUint(r.PathValue("requestID"), 10, 64)
	if err != nil || requestID64 == 0 {
		gateway.WriteJSONError(w, http.StatusBadRequest, "invalid_request_error", "request id is invalid")
		return
	}

	var payload updateContactStatusRequest
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		gateway.WriteJSONError(w, http.StatusBadRequest, "invalid_request_error", "request body must be valid JSON")
		return
	}

	record, err := h.store.UpdateContactRequestStatus(r.Context(), uint(requestID64), payload.Status)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			gateway.WriteJSONError(w, http.StatusNotFound, "not_found", "contact request not found")
			return
		}
		gateway.WriteJSONError(w, http.StatusInternalServerError, "server_error", "failed to update contact request")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"id":         record.ID,
		"status":     record.Status,
		"updated_at": record.UpdatedAt,
	})
}

func (h *AdminHandler) GetContactRequestDetail(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		gateway.WriteJSONError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		return
	}

	if _, ok := auth.PrincipalFromContext(r.Context()); !ok {
		gateway.WriteJSONError(w, http.StatusUnauthorized, "invalid_api_key", "authentication required")
		return
	}

	requestID64, err := strconv.ParseUint(r.PathValue("requestID"), 10, 64)
	if err != nil || requestID64 == 0 {
		gateway.WriteJSONError(w, http.StatusBadRequest, "invalid_request_error", "request id is invalid")
		return
	}

	record, err := h.store.FindContactRequestByID(r.Context(), uint(requestID64))
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			gateway.WriteJSONError(w, http.StatusNotFound, "not_found", "contact request not found")
			return
		}
		gateway.WriteJSONError(w, http.StatusInternalServerError, "server_error", "failed to query contact request")
		return
	}

	notes, err := h.store.ListContactNotesByRequestID(r.Context(), record.ID)
	if err != nil {
		gateway.WriteJSONError(w, http.StatusInternalServerError, "server_error", "failed to query contact notes")
		return
	}

	notesData := make([]map[string]any, 0, len(notes))
	for _, note := range notes {
		notesData = append(notesData, map[string]any{
			"id":         note.ID,
			"author":     note.Author,
			"body":       note.Body,
			"created_at": note.CreatedAt,
			"updated_at": note.UpdatedAt,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"id":         record.ID,
		"name":       record.Name,
		"email":      record.Email,
		"company":    record.Company,
		"role":       record.Role,
		"owner":      record.Owner,
		"message":    record.Message,
		"status":     record.Status,
		"source":     record.Source,
		"created_at": record.CreatedAt,
		"updated_at": record.UpdatedAt,
		"notes":      notesData,
	})
}

func (h *AdminHandler) AddContactRequestNote(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		gateway.WriteJSONError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		return
	}

	principal, ok := auth.PrincipalFromContext(r.Context())
	if !ok {
		gateway.WriteJSONError(w, http.StatusUnauthorized, "invalid_api_key", "authentication required")
		return
	}

	requestID64, err := strconv.ParseUint(r.PathValue("requestID"), 10, 64)
	if err != nil || requestID64 == 0 {
		gateway.WriteJSONError(w, http.StatusBadRequest, "invalid_request_error", "request id is invalid")
		return
	}

	var payload addContactNoteRequest
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		gateway.WriteJSONError(w, http.StatusBadRequest, "invalid_request_error", "request body must be valid JSON")
		return
	}
	if strings.TrimSpace(payload.Body) == "" {
		gateway.WriteJSONError(w, http.StatusBadRequest, "invalid_request_error", "note body is required")
		return
	}

	if _, err := h.store.FindContactRequestByID(r.Context(), uint(requestID64)); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			gateway.WriteJSONError(w, http.StatusNotFound, "not_found", "contact request not found")
			return
		}
		gateway.WriteJSONError(w, http.StatusInternalServerError, "server_error", "failed to validate contact request")
		return
	}

	note, err := h.store.CreateContactNote(r.Context(), store.CreateContactNoteInput{
		ContactRequestID: uint(requestID64),
		Author:           principal.User.Name,
		Body:             payload.Body,
	})
	if err != nil {
		gateway.WriteJSONError(w, http.StatusInternalServerError, "server_error", "failed to create note")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"id":         note.ID,
		"author":     note.Author,
		"body":       note.Body,
		"created_at": note.CreatedAt,
		"updated_at": note.UpdatedAt,
	})
}

func (h *AdminHandler) UpdateContactRequestOwner(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPatch {
		gateway.WriteJSONError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		return
	}

	if _, ok := auth.PrincipalFromContext(r.Context()); !ok {
		gateway.WriteJSONError(w, http.StatusUnauthorized, "invalid_api_key", "authentication required")
		return
	}

	requestID64, err := strconv.ParseUint(r.PathValue("requestID"), 10, 64)
	if err != nil || requestID64 == 0 {
		gateway.WriteJSONError(w, http.StatusBadRequest, "invalid_request_error", "request id is invalid")
		return
	}

	var payload updateContactOwnerRequest
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		gateway.WriteJSONError(w, http.StatusBadRequest, "invalid_request_error", "request body must be valid JSON")
		return
	}

	record, err := h.store.UpdateContactRequestOwner(r.Context(), uint(requestID64), payload.Owner)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			gateway.WriteJSONError(w, http.StatusNotFound, "not_found", "contact request not found")
			return
		}
		gateway.WriteJSONError(w, http.StatusInternalServerError, "server_error", "failed to update owner")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"id":         record.ID,
		"owner":      record.Owner,
		"updated_at": record.UpdatedAt,
	})
}

func (h *AdminHandler) UpdateContactNote(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPatch {
		gateway.WriteJSONError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		return
	}

	if _, ok := auth.PrincipalFromContext(r.Context()); !ok {
		gateway.WriteJSONError(w, http.StatusUnauthorized, "invalid_api_key", "authentication required")
		return
	}

	noteID64, err := strconv.ParseUint(r.PathValue("noteID"), 10, 64)
	if err != nil || noteID64 == 0 {
		gateway.WriteJSONError(w, http.StatusBadRequest, "invalid_request_error", "note id is invalid")
		return
	}

	var payload addContactNoteRequest
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		gateway.WriteJSONError(w, http.StatusBadRequest, "invalid_request_error", "request body must be valid JSON")
		return
	}
	if strings.TrimSpace(payload.Body) == "" {
		gateway.WriteJSONError(w, http.StatusBadRequest, "invalid_request_error", "note body is required")
		return
	}

	note, err := h.store.UpdateContactNote(r.Context(), uint(noteID64), payload.Body)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			gateway.WriteJSONError(w, http.StatusNotFound, "not_found", "note not found")
			return
		}
		gateway.WriteJSONError(w, http.StatusInternalServerError, "server_error", "failed to update note")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"id":         note.ID,
		"author":     note.Author,
		"body":       note.Body,
		"created_at": note.CreatedAt,
		"updated_at": note.UpdatedAt,
	})
}

func (h *AdminHandler) DeleteContactNote(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		gateway.WriteJSONError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		return
	}

	if _, ok := auth.PrincipalFromContext(r.Context()); !ok {
		gateway.WriteJSONError(w, http.StatusUnauthorized, "invalid_api_key", "authentication required")
		return
	}

	noteID64, err := strconv.ParseUint(r.PathValue("noteID"), 10, 64)
	if err != nil || noteID64 == 0 {
		gateway.WriteJSONError(w, http.StatusBadRequest, "invalid_request_error", "note id is invalid")
		return
	}

	if err := h.store.DeleteContactNote(r.Context(), uint(noteID64)); err != nil {
		gateway.WriteJSONError(w, http.StatusInternalServerError, "server_error", "failed to delete note")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
}
