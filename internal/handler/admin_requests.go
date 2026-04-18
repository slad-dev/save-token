package handler

import (
	"encoding/json"
	"net/http"
	"sort"
	"strings"
	"time"

	"agent-gateway/internal/auth"
	"agent-gateway/internal/gateway"
	"agent-gateway/internal/store"
)

func (h *AdminHandler) ListRequestObservations(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		gateway.WriteJSONError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		return
	}

	principal, ok := auth.PrincipalFromContext(r.Context())
	if !ok {
		gateway.WriteJSONError(w, http.StatusUnauthorized, "invalid_api_key", "authentication required")
		return
	}

	options := parseListOptions(r)
	rangeKey := normalizeCostRange(r.URL.Query().Get("range"))
	activeProject := strings.TrimSpace(r.URL.Query().Get("project"))
	activeUpstreamKind := strings.TrimSpace(r.URL.Query().Get("upstream_kind"))
	activeGovernance := strings.TrimSpace(r.URL.Query().Get("governance"))

	now := time.Now()
	location := now.Location()
	startOfToday := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, location)
	startTime, endTime := costRangeWindow(startOfToday, rangeKey)

	logs, err := h.store.ListUsageLogsByUserAndTimeRange(r.Context(), principal.User.ID, startTime, endTime)
	if err != nil {
		gateway.WriteJSONError(w, http.StatusInternalServerError, "server_error", "failed to query usage logs")
		return
	}

	traces, err := h.store.ListRouteTracesByUserAndTimeRange(r.Context(), principal.User.ID, startTime, endTime)
	if err != nil {
		gateway.WriteJSONError(w, http.StatusInternalServerError, "server_error", "failed to query route traces")
		return
	}

	projects, err := h.store.ListProjectsByUser(r.Context(), principal.User.ID)
	if err != nil {
		gateway.WriteJSONError(w, http.StatusInternalServerError, "server_error", "failed to query projects")
		return
	}

	traceByRequestID := make(map[string]store.RouteTrace, len(traces))
	for _, trace := range traces {
		if strings.TrimSpace(trace.RequestID) == "" {
			continue
		}
		traceByRequestID[trace.RequestID] = trace
	}

	projectNameByAPIKeyID := make(map[uint]string)
	apiKeyNameByID := make(map[uint]string)
	for _, project := range projects {
		for _, apiKey := range project.APIKeys {
			projectNameByAPIKeyID[apiKey.ID] = project.Name
			apiKeyNameByID[apiKey.ID] = apiKey.Name
		}
	}

	allProjects := make(map[string]struct{})
	allUpstreamKinds := make(map[string]struct{})
	allGovernance := make(map[string]struct{})
	data := make([]map[string]any, 0, len(logs))

	for _, log := range logs {
		trace := traceByRequestID[log.RequestID]
		projectName := firstNonEmpty(projectNameByAPIKeyID[log.APIKeyID], "Unassigned Project")
		upstreamKindValue := upstreamKind(log.UpstreamName, log.CacheHit)
		governanceAction := governanceBucketName(log, trace)

		allProjects[projectName] = struct{}{}
		allUpstreamKinds[upstreamKindValue] = struct{}{}
		allGovernance[governanceAction] = struct{}{}

		if activeProject != "" && activeProject != projectName {
			continue
		}
		if activeUpstreamKind != "" && activeUpstreamKind != upstreamKindValue {
			continue
		}
		if activeGovernance != "" && activeGovernance != governanceAction {
			continue
		}

		data = append(data, map[string]any{
			"request_id":             log.RequestID,
			"created_at":             log.CreatedAt,
			"project_name":           projectName,
			"api_key_name":           apiKeyNameByID[log.APIKeyID],
			"endpoint":               log.Endpoint,
			"original_model":         trace.OriginalModel,
			"final_model":            firstNonEmpty(trace.FinalModel, log.Model),
			"route_tier":             trace.RouteTier,
			"intent_class":           trace.IntentClass,
			"upstream_name":          log.UpstreamName,
			"upstream_kind":          upstreamKindValue,
			"governance_action":      governanceAction,
			"status_code":            log.StatusCode,
			"success":                log.Success,
			"cache_hit":              log.CacheHit,
			"search_applied":         trace.SearchApplied,
			"compression_applied":    trace.CompressionApplied,
			"duration_ms":            log.DurationMs,
			"saved_cost":             log.SavedCost,
			"saved_tokens":           log.SavedTokens,
			"decision_reason":        trace.DecisionReason,
			"intent_reasons":         splitAndTrim(trace.IntentReasons),
			"estimated_input_chars":  trace.EstimatedInputChars,
			"estimated_input_tokens": trace.EstimatedInputTokens,
		})
	}

	sort.Slice(data, func(i, j int) bool {
		left := data[i]["created_at"].(time.Time)
		right := data[j]["created_at"].(time.Time)
		return left.After(right)
	})

	limit := normalizeLimit(options.Limit)
	offset := normalizeOffset(options.Offset)
	total := len(data)
	if offset > total {
		offset = total
	}
	end := offset + limit
	if end > total {
		end = total
	}
	pagedData := data[offset:end]

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"data": pagedData,
		"filters": map[string]any{
			"active": map[string]any{
				"range":         rangeKey,
				"project":       activeProject,
				"upstream_kind": activeUpstreamKind,
				"governance":    activeGovernance,
			},
			"projects":           sortedKeys(allProjects),
			"upstream_kinds":     sortedKeys(allUpstreamKinds),
			"governance_actions": sortedKeys(allGovernance),
		},
		"time_range": map[string]any{
			"key":   rangeKey,
			"start": startTime.Format(time.RFC3339),
			"end":   endTime.Format(time.RFC3339),
		},
		"pagination": map[string]any{
			"total":  total,
			"limit":  limit,
			"offset": offset,
		},
	})
}

func upstreamKind(upstreamName string, cacheHit bool) string {
	if cacheHit {
		return "cache"
	}
	if strings.HasPrefix(upstreamName, "source:") {
		return "byok"
	}
	if strings.TrimSpace(upstreamName) == "" {
		return "unknown"
	}
	return "managed"
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func maxInt(left, right int) int {
	if left > right {
		return left
	}
	return right
}

func sortedKeys(items map[string]struct{}) []string {
	keys := make([]string, 0, len(items))
	for key := range items {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
