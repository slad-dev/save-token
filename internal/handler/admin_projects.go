package handler

import (
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"agent-gateway/internal/auth"
	"agent-gateway/internal/gateway"
	"agent-gateway/internal/store"
)

type createProjectRequest struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Mode        string `json:"mode"`
	PrivacyMode string `json:"privacy_mode"`
}

type updateProjectSettingsRequest struct {
	Mode                  string `json:"mode"`
	PrivacyMode           string `json:"privacy_mode"`
	WebSearchEnabled      bool   `json:"web_search_enabled"`
	AggressiveCompression bool   `json:"aggressive_compression"`
}

func (h *AdminHandler) ListProjects(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		gateway.WriteJSONError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		return
	}

	principal, ok := auth.PrincipalFromContext(r.Context())
	if !ok {
		gateway.WriteJSONError(w, http.StatusUnauthorized, "invalid_api_key", "authentication required")
		return
	}

	projects, err := h.store.ListProjectsByUser(r.Context(), principal.User.ID)
	if err != nil {
		gateway.WriteJSONError(w, http.StatusInternalServerError, "server_error", "failed to query projects")
		return
	}

	now := time.Now()
	startOfDay := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	endOfDay := startOfDay.Add(24 * time.Hour)
	logs, err := h.store.ListUsageLogsByUserAndTimeRange(r.Context(), principal.User.ID, startOfDay, endOfDay)
	if err != nil {
		gateway.WriteJSONError(w, http.StatusInternalServerError, "server_error", "failed to query project metrics")
		return
	}

	data := buildProjectListResponse(projects, logs)
	writeListResponse(w, data, int64(len(data)), store.ListOptions{Limit: len(data), Offset: 0})
}

func (h *AdminHandler) CreateProject(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		gateway.WriteJSONError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		return
	}

	principal, ok := auth.PrincipalFromContext(r.Context())
	if !ok {
		gateway.WriteJSONError(w, http.StatusUnauthorized, "invalid_api_key", "authentication required")
		return
	}

	var payload createProjectRequest
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		gateway.WriteJSONError(w, http.StatusBadRequest, "invalid_request_error", "request body must be valid JSON")
		return
	}

	if strings.TrimSpace(payload.Name) == "" {
		gateway.WriteJSONError(w, http.StatusBadRequest, "invalid_request_error", "project name is required")
		return
	}
	webSearchEnabled := true
	if strings.EqualFold(strings.TrimSpace(payload.PrivacyMode), store.PrivacyModeStrict) {
		webSearchEnabled = false
	}

	project, err := h.store.CreateProject(r.Context(), store.CreateProjectInput{
		UserID:                principal.User.ID,
		Name:                  payload.Name,
		Description:           payload.Description,
		Mode:                  payload.Mode,
		PrivacyMode:           payload.PrivacyMode,
		WebSearchEnabled:      webSearchEnabled,
		AggressiveCompression: false,
	})
	if err != nil {
		gateway.WriteJSONError(w, http.StatusInternalServerError, "server_error", "failed to create project")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"id":                     project.ID,
		"name":                   project.Name,
		"description":            project.Description,
		"mode":                   project.Mode,
		"privacy_mode":           project.PrivacyMode,
		"web_search_enabled":     project.WebSearchEnabled,
		"aggressive_compression": project.AggressiveCompression,
		"is_active":              project.IsActive,
		"created_at":             project.CreatedAt,
		"updated_at":             project.UpdatedAt,
	})
}

func (h *AdminHandler) UpdateProjectSettings(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPatch {
		gateway.WriteJSONError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		return
	}

	principal, ok := auth.PrincipalFromContext(r.Context())
	if !ok {
		gateway.WriteJSONError(w, http.StatusUnauthorized, "invalid_api_key", "authentication required")
		return
	}

	projectIDValue := r.PathValue("projectID")
	projectID64, err := strconv.ParseUint(projectIDValue, 10, 64)
	if err != nil || projectID64 == 0 {
		gateway.WriteJSONError(w, http.StatusBadRequest, "invalid_request_error", "project id is invalid")
		return
	}

	var payload updateProjectSettingsRequest
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		gateway.WriteJSONError(w, http.StatusBadRequest, "invalid_request_error", "request body must be valid JSON")
		return
	}
	webSearchEnabled := payload.WebSearchEnabled
	if strings.EqualFold(strings.TrimSpace(payload.PrivacyMode), store.PrivacyModeStrict) {
		webSearchEnabled = false
	}

	project, err := h.store.UpdateProjectSettings(r.Context(), store.UpdateProjectSettingsInput{
		ProjectID:             uint(projectID64),
		UserID:                principal.User.ID,
		Mode:                  payload.Mode,
		PrivacyMode:           payload.PrivacyMode,
		WebSearchEnabled:      webSearchEnabled,
		AggressiveCompression: payload.AggressiveCompression,
	})
	if err != nil {
		gateway.WriteJSONError(w, http.StatusNotFound, "not_found", "project not found")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"id":                     project.ID,
		"mode":                   project.Mode,
		"privacy_mode":           project.PrivacyMode,
		"web_search_enabled":     project.WebSearchEnabled,
		"aggressive_compression": project.AggressiveCompression,
		"updated_at":             project.UpdatedAt,
	})
}

func (h *AdminHandler) GetProjectDetail(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		gateway.WriteJSONError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		return
	}

	principal, ok := auth.PrincipalFromContext(r.Context())
	if !ok {
		gateway.WriteJSONError(w, http.StatusUnauthorized, "invalid_api_key", "authentication required")
		return
	}

	projectIDValue := r.PathValue("projectID")
	projectID64, err := strconv.ParseUint(projectIDValue, 10, 64)
	if err != nil || projectID64 == 0 {
		gateway.WriteJSONError(w, http.StatusBadRequest, "invalid_request_error", "project id is invalid")
		return
	}
	projectID := uint(projectID64)
	rangeKey := normalizeCostRange(r.URL.Query().Get("range"))

	project, err := h.store.FindProjectDetailByUser(r.Context(), principal.User.ID, projectID)
	if err != nil {
		gateway.WriteJSONError(w, http.StatusNotFound, "not_found", "project not found")
		return
	}

	now := time.Now()
	startOfDay := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	startTime, endTime := costRangeWindow(startOfDay, rangeKey)
	logs, err := h.store.ListUsageLogsByUserAndTimeRange(r.Context(), principal.User.ID, startTime, endTime)
	if err != nil {
		gateway.WriteJSONError(w, http.StatusInternalServerError, "server_error", "failed to query project usage")
		return
	}
	traces, err := h.store.ListRouteTracesByUserAndTimeRange(r.Context(), principal.User.ID, startTime, endTime)
	if err != nil {
		gateway.WriteJSONError(w, http.StatusInternalServerError, "server_error", "failed to query project traces")
		return
	}

	apiKeyIDs := make(map[uint]store.APIKey, len(project.APIKeys))
	activeKeys := 0
	for _, apiKey := range project.APIKeys {
		apiKeyIDs[apiKey.ID] = apiKey
		if apiKey.IsActive {
			activeKeys++
		}
	}

	traceByRequestID := make(map[string]store.RouteTrace, len(traces))
	for _, trace := range traces {
		if strings.TrimSpace(trace.RequestID) == "" {
			continue
		}
		traceByRequestID[trace.RequestID] = trace
	}

	overview := map[string]any{
		"range_cost":        int64(0),
		"range_saved":       int64(0),
		"saved_tokens":      int64(0),
		"requests_in_range": int64(0),
		"average_latency":   int64(0),
		"active_keys":       activeKeys,
		"total_keys":        len(project.APIKeys),
		"active_sources":    0,
		"total_sources":     len(project.Sources),
		"cache_hits":        int64(0),
		"search_applied":    int64(0),
		"compression_hits":  int64(0),
	}
	trend := make([]map[string]any, 0)
	totalBuckets := int(endTime.Sub(startTime).Hours() / 24)
	if totalBuckets < 1 {
		totalBuckets = 1
	}
	for i := 0; i < totalBuckets; i++ {
		bucketTime := startTime.Add(time.Duration(i) * 24 * time.Hour)
		trend = append(trend, map[string]any{
			"time":    bucketTime.Format("01/02"),
			"cost":    int64(0),
			"saved":   int64(0),
			"latency": int64(0),
		})
	}

	sourcesData := make([]map[string]any, 0, len(project.Sources))
	activeSources := 0
	for _, source := range project.Sources {
		if source.IsActive {
			activeSources++
		}
		sourcesData = append(sourcesData, map[string]any{
			"id":               source.ID,
			"name":             source.Name,
			"kind":             source.Kind,
			"provider":         source.Provider,
			"base_url":         source.BaseURL,
			"is_active":        source.IsActive,
			"supported_models": splitAndTrim(source.SupportedModels),
			"created_at":       source.CreatedAt,
			"updated_at":       source.UpdatedAt,
		})
	}
	overview["active_sources"] = activeSources

	keysData := make([]map[string]any, 0, len(project.APIKeys))
	for _, apiKey := range project.APIKeys {
		keysData = append(keysData, map[string]any{
			"id":               apiKey.ID,
			"name":             apiKey.Name,
			"key_preview":      maskAPIKey(apiKey.Key),
			"allow_web_search": apiKey.AllowWebSearch,
			"is_active":        apiKey.IsActive,
			"created_at":       apiKey.CreatedAt,
			"updated_at":       apiKey.UpdatedAt,
		})
	}

	recentRequests := make([]map[string]any, 0)
	var latencyTotal int64
	for _, log := range logs {
		apiKey, ok := apiKeyIDs[log.APIKeyID]
		if !ok {
			continue
		}
		trace := traceByRequestID[log.RequestID]
		overview["range_cost"] = overview["range_cost"].(int64) + log.CreditsCharged
		overview["range_saved"] = overview["range_saved"].(int64) + log.SavedCost
		overview["saved_tokens"] = overview["saved_tokens"].(int64) + log.SavedTokens
		overview["requests_in_range"] = overview["requests_in_range"].(int64) + 1
		latencyTotal += log.DurationMs
		if log.CacheHit {
			overview["cache_hits"] = overview["cache_hits"].(int64) + 1
		}
		if trace.SearchApplied {
			overview["search_applied"] = overview["search_applied"].(int64) + 1
		}
		if trace.CompressionApplied {
			overview["compression_hits"] = overview["compression_hits"].(int64) + 1
		}
		bucketIndex := int(log.CreatedAt.In(startTime.Location()).Sub(startTime).Hours() / 24)
		if bucketIndex < 0 {
			bucketIndex = 0
		}
		if bucketIndex >= len(trend) {
			bucketIndex = len(trend) - 1
		}
		trend[bucketIndex]["cost"] = trend[bucketIndex]["cost"].(int64) + log.CreditsCharged
		trend[bucketIndex]["saved"] = trend[bucketIndex]["saved"].(int64) + log.SavedCost
		trend[bucketIndex]["latency"] = trend[bucketIndex]["latency"].(int64) + log.DurationMs

		recentRequests = append(recentRequests, map[string]any{
			"request_id":          log.RequestID,
			"created_at":          log.CreatedAt,
			"api_key_name":        apiKey.Name,
			"endpoint":            log.Endpoint,
			"final_model":         firstNonEmpty(trace.FinalModel, log.Model),
			"upstream_name":       log.UpstreamName,
			"upstream_kind":       upstreamKind(log.UpstreamName, log.CacheHit),
			"governance_action":   governanceBucketName(log, trace),
			"duration_ms":         log.DurationMs,
			"saved_cost":          log.SavedCost,
			"success":             log.Success,
			"status_code":         log.StatusCode,
			"decision_reason":     trace.DecisionReason,
			"search_applied":      trace.SearchApplied,
			"compression_applied": trace.CompressionApplied,
			"cache_hit":           log.CacheHit,
		})
	}

	if overview["requests_in_range"].(int64) > 0 {
		overview["average_latency"] = latencyTotal / overview["requests_in_range"].(int64)
	}

	sort.Slice(recentRequests, func(i, j int) bool {
		return recentRequests[i]["created_at"].(time.Time).After(recentRequests[j]["created_at"].(time.Time))
	})
	if len(recentRequests) > 8 {
		recentRequests = recentRequests[:8]
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"id":                     project.ID,
		"name":                   project.Name,
		"description":            project.Description,
		"mode":                   project.Mode,
		"privacy_mode":           project.PrivacyMode,
		"web_search_enabled":     project.WebSearchEnabled,
		"aggressive_compression": project.AggressiveCompression,
		"is_active":              project.IsActive,
		"created_at":             project.CreatedAt,
		"updated_at":             project.UpdatedAt,
		"time_range": map[string]any{
			"key":   rangeKey,
			"start": startTime.Format(time.RFC3339),
			"end":   endTime.Format(time.RFC3339),
		},
		"overview": overview,
		"trend":    trend,
		"sources":  sourcesData,
		"keys":     keysData,
		"requests": recentRequests,
	})
}

func buildProjectListResponse(projects []store.Project, logs []store.UsageLog) []map[string]any {
	projectByAPIKey := make(map[uint]uint)
	for _, project := range projects {
		for _, apiKey := range project.APIKeys {
			projectByAPIKey[apiKey.ID] = project.ID
		}
	}

	type dailySummary struct {
		cost     int64
		saved    int64
		requests int64
	}
	summaryByProject := make(map[uint]*dailySummary)
	for _, log := range logs {
		projectID, ok := projectByAPIKey[log.APIKeyID]
		if !ok {
			continue
		}
		summary := summaryByProject[projectID]
		if summary == nil {
			summary = &dailySummary{}
			summaryByProject[projectID] = summary
		}
		summary.cost += log.CreditsCharged
		summary.saved += log.SavedCost
		summary.requests++
	}

	data := make([]map[string]any, 0, len(projects))
	for _, project := range projects {
		activeKeys := 0
		for _, apiKey := range project.APIKeys {
			if apiKey.IsActive {
				activeKeys++
			}
		}

		summary := summaryByProject[project.ID]
		todayCost := int64(0)
		todaySaved := int64(0)
		requestsToday := int64(0)
		if summary != nil {
			todayCost = summary.cost
			todaySaved = summary.saved
			requestsToday = summary.requests
		}

		data = append(data, map[string]any{
			"id":                     project.ID,
			"name":                   project.Name,
			"description":            project.Description,
			"mode":                   project.Mode,
			"privacy_mode":           project.PrivacyMode,
			"web_search_enabled":     project.WebSearchEnabled,
			"aggressive_compression": project.AggressiveCompression,
			"is_active":              project.IsActive,
			"active_keys":            activeKeys,
			"total_keys":             len(project.APIKeys),
			"today_cost":             todayCost,
			"today_saved":            todaySaved,
			"requests_today":         requestsToday,
			"created_at":             project.CreatedAt,
			"updated_at":             project.UpdatedAt,
		})
	}

	return data
}
