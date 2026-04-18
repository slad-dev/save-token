package handler

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"agent-gateway/internal/auth"
	"agent-gateway/internal/config"
	"agent-gateway/internal/gateway"
	"agent-gateway/internal/store"
)

type AdminHandler struct {
	store    *store.SQLiteStore
	registry *gateway.Registry
	cfg      *config.Config
}

func NewAdminHandler(st *store.SQLiteStore, registry *gateway.Registry, cfg *config.Config) *AdminHandler {
	return &AdminHandler{store: st, registry: registry, cfg: cfg}
}

func (h *AdminHandler) ListUsers(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		gateway.WriteJSONError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		return
	}
	if _, ok := h.requireAdmin(w, r); !ok {
		return
	}

	options := parseListOptions(r)
	users, total, err := h.store.ListUsers(r.Context(), options)
	if err != nil {
		gateway.WriteJSONError(w, http.StatusInternalServerError, "server_error", "failed to query users")
		return
	}

	data := make([]map[string]any, 0, len(users))
	for _, user := range users {
		data = append(data, map[string]any{
			"id":         user.ID,
			"name":       user.Name,
			"email":      stringValue(user.Email),
			"role":       user.Role,
			"balance":    user.Balance,
			"plan":       user.Plan,
			"is_active":  user.IsActive,
			"created_at": user.CreatedAt,
			"updated_at": user.UpdatedAt,
		})
	}

	writeListResponse(w, data, total, options)
}

func (h *AdminHandler) ListAPIKeys(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		gateway.WriteJSONError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		return
	}

	options := parseListOptions(r)
	apiKeys, total, err := h.store.ListAPIKeys(r.Context(), options)
	if err != nil {
		gateway.WriteJSONError(w, http.StatusInternalServerError, "server_error", "failed to query api keys")
		return
	}

	data := make([]map[string]any, 0, len(apiKeys))
	for _, apiKey := range apiKeys {
		data = append(data, map[string]any{
			"id":                   apiKey.ID,
			"name":                 apiKey.Name,
			"key_preview":          maskAPIKey(apiKey.Key),
			"user_id":              apiKey.UserID,
			"user_name":            apiKey.User.Name,
			"project_id":           apiKey.ProjectID,
			"project_name":         projectName(apiKey.Project),
			"project_privacy_mode": projectPrivacyMode(apiKey.Project),
			"allow_web_search":     apiKey.AllowWebSearch,
			"is_active":            apiKey.IsActive,
			"created_at":           apiKey.CreatedAt,
			"updated_at":           apiKey.UpdatedAt,
		})
	}

	writeListResponse(w, data, total, options)
}

func (h *AdminHandler) ListUsageLogs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		gateway.WriteJSONError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		return
	}

	options := parseListOptions(r)
	filter := store.UsageLogFilter{
		ListOptions: options,
		Endpoint:    r.URL.Query().Get("endpoint"),
	}
	if userID, err := parseUintQuery(r, "user_id"); err == nil {
		filter.UserID = uint(userID)
	}
	if apiKeyID, err := parseUintQuery(r, "api_key_id"); err == nil {
		filter.APIKeyID = uint(apiKeyID)
	}

	logs, total, err := h.store.ListUsageLogs(r.Context(), filter)
	if err != nil {
		gateway.WriteJSONError(w, http.StatusInternalServerError, "server_error", "failed to query usage logs")
		return
	}

	data := make([]map[string]any, 0, len(logs))
	for _, log := range logs {
		data = append(data, map[string]any{
			"id":                log.ID,
			"user_id":           log.UserID,
			"api_key_id":        log.APIKeyID,
			"endpoint":          log.Endpoint,
			"model":             log.Model,
			"upstream_name":     log.UpstreamName,
			"status_code":       log.StatusCode,
			"prompt_tokens":     log.PromptTokens,
			"completion_tokens": log.CompletionTokens,
			"total_tokens":      log.TotalTokens,
			"baseline_tokens":   log.BaselineTokens,
			"saved_tokens":      log.SavedTokens,
			"credits_charged":   log.CreditsCharged,
			"baseline_cost":     log.BaselineCost,
			"saved_cost":        log.SavedCost,
			"request_kind":      log.RequestKind,
			"stream":            log.Stream,
			"tool_used":         log.ToolUsed,
			"cache_hit":         log.CacheHit,
			"duration_ms":       log.DurationMs,
			"success":           log.Success,
			"error_message":     log.ErrorMessage,
			"created_at":        log.CreatedAt,
		})
	}

	writeListResponse(w, data, total, options)
}

func (h *AdminHandler) ListRouteTraces(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		gateway.WriteJSONError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		return
	}

	options := parseListOptions(r)
	filter := store.RouteTraceFilter{
		ListOptions: options,
		Endpoint:    r.URL.Query().Get("endpoint"),
		IntentClass: r.URL.Query().Get("intent_class"),
		RouteTier:   r.URL.Query().Get("route_tier"),
	}
	if userID, err := parseUintQuery(r, "user_id"); err == nil {
		filter.UserID = uint(userID)
	}
	if apiKeyID, err := parseUintQuery(r, "api_key_id"); err == nil {
		filter.APIKeyID = uint(apiKeyID)
	}

	traces, total, err := h.store.ListRouteTraces(r.Context(), filter)
	if err != nil {
		gateway.WriteJSONError(w, http.StatusInternalServerError, "server_error", "failed to query route traces")
		return
	}

	data := make([]map[string]any, 0, len(traces))
	for _, trace := range traces {
		data = append(data, map[string]any{
			"id":                     trace.ID,
			"request_id":             trace.RequestID,
			"user_id":                trace.UserID,
			"api_key_id":             trace.APIKeyID,
			"endpoint":               trace.Endpoint,
			"original_model":         trace.OriginalModel,
			"final_model":            trace.FinalModel,
			"route_tier":             trace.RouteTier,
			"intent_class":           trace.IntentClass,
			"intent_confidence":      trace.IntentConfidence,
			"estimated_input_chars":  trace.EstimatedInputChars,
			"estimated_input_tokens": trace.EstimatedInputTokens,
			"search_applied":         trace.SearchApplied,
			"compression_applied":    trace.CompressionApplied,
			"cache_hit":              trace.CacheHit,
			"decision_reason":        trace.DecisionReason,
			"intent_reasons":         trace.IntentReasons,
			"created_at":             trace.CreatedAt,
		})
	}

	writeListResponse(w, data, total, options)
}

func writeListResponse(w http.ResponseWriter, data []map[string]any, total int64, options store.ListOptions) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"data": data,
		"pagination": map[string]any{
			"total":  total,
			"limit":  normalizeLimit(options.Limit),
			"offset": normalizeOffset(options.Offset),
		},
	})
}

func parseListOptions(r *http.Request) store.ListOptions {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	return store.ListOptions{
		Limit:  limit,
		Offset: offset,
	}
}

func parseUintQuery(r *http.Request, name string) (uint64, error) {
	value := r.URL.Query().Get(name)
	if value == "" {
		return 0, strconv.ErrSyntax
	}
	return strconv.ParseUint(value, 10, 64)
}

func maskAPIKey(key string) string {
	if len(key) <= 8 {
		return key
	}
	return key[:6] + "..." + key[len(key)-4:]
}

func normalizeLimit(limit int) int {
	if limit <= 0 {
		return 20
	}
	if limit > 100 {
		return 100
	}
	return limit
}

func normalizeOffset(offset int) int {
	if offset < 0 {
		return 0
	}
	return offset
}

func projectName(project *store.Project) string {
	if project == nil {
		return ""
	}
	return project.Name
}

func projectPrivacyMode(project *store.Project) string {
	if project == nil {
		return store.PrivacyModeStandard
	}
	mode := strings.TrimSpace(project.PrivacyMode)
	if mode == "" {
		return store.PrivacyModeStandard
	}
	return mode
}

func (h *AdminHandler) requireAdmin(w http.ResponseWriter, r *http.Request) (*auth.Principal, bool) {
	principal, ok := auth.PrincipalFromContext(r.Context())
	if !ok {
		gateway.WriteJSONError(w, http.StatusUnauthorized, "unauthorized", "login required")
		return nil, false
	}
	if principal.User.Role != store.RoleAdmin {
		gateway.WriteJSONError(w, http.StatusForbidden, "forbidden", "admin access required")
		return principal, false
	}
	return principal, true
}

func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(*value)
}
