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

func (h *AdminHandler) CostAnalysis(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		gateway.WriteJSONError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		return
	}

	principal, ok := auth.PrincipalFromContext(r.Context())
	if !ok {
		gateway.WriteJSONError(w, http.StatusUnauthorized, "invalid_api_key", "authentication required")
		return
	}

	now := time.Now()
	location := now.Location()
	rangeKey := normalizeCostRange(r.URL.Query().Get("range"))
	startOfDay := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, location)
	startTime, endOfDay := costRangeWindow(startOfDay, rangeKey)

	logs, err := h.store.ListUsageLogsByUserAndTimeRange(r.Context(), principal.User.ID, startTime, endOfDay)
	if err != nil {
		gateway.WriteJSONError(w, http.StatusInternalServerError, "server_error", "failed to query usage logs")
		return
	}

	traces, err := h.store.ListRouteTracesByUserAndTimeRange(r.Context(), principal.User.ID, startTime, endOfDay)
	if err != nil {
		gateway.WriteJSONError(w, http.StatusInternalServerError, "server_error", "failed to query route traces")
		return
	}

	projects, err := h.store.ListProjectsByUser(r.Context(), principal.User.ID)
	if err != nil {
		gateway.WriteJSONError(w, http.StatusInternalServerError, "server_error", "failed to query projects")
		return
	}

	projectNameByAPIKeyID := make(map[uint]string)
	for _, project := range projects {
		for _, apiKey := range project.APIKeys {
			projectNameByAPIKeyID[apiKey.ID] = project.Name
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
		"actual_cost":   int64(0),
		"baseline_cost": int64(0),
		"saved_cost":    int64(0),
		"saved_tokens":  int64(0),
		"requests":      int64(0),
	}

	projectAgg := make(map[string]map[string]any)
	sourceAgg := make(map[string]map[string]any)
	governanceAgg := make(map[string]map[string]any)
	trendAgg := make(map[int]map[string]any)
	totalDays := int(endOfDay.Sub(startTime) / (24 * time.Hour))
	for day := 0; day < totalDays; day++ {
		bucketTime := startTime.AddDate(0, 0, day)
		trendAgg[day] = map[string]any{
			"time":          bucketTime.Format("01/02"),
			"actual_cost":   int64(0),
			"baseline_cost": int64(0),
			"saved_cost":    int64(0),
		}
	}

	for _, log := range logs {
		trace := traceByRequestID[log.RequestID]

		overview["actual_cost"] = overview["actual_cost"].(int64) + log.CreditsCharged
		overview["baseline_cost"] = overview["baseline_cost"].(int64) + log.BaselineCost
		overview["saved_cost"] = overview["saved_cost"].(int64) + log.SavedCost
		overview["saved_tokens"] = overview["saved_tokens"].(int64) + log.SavedTokens
		overview["requests"] = overview["requests"].(int64) + 1

		projectName := projectNameByAPIKeyID[log.APIKeyID]
		if strings.TrimSpace(projectName) == "" {
			projectName = "Unassigned Project"
		}
		projectItem := ensureCostBucket(projectAgg, projectName)
		projectItem["actual_cost"] = projectItem["actual_cost"].(int64) + log.CreditsCharged
		projectItem["baseline_cost"] = projectItem["baseline_cost"].(int64) + log.BaselineCost
		projectItem["saved_cost"] = projectItem["saved_cost"].(int64) + log.SavedCost
		projectItem["saved_tokens"] = projectItem["saved_tokens"].(int64) + log.SavedTokens
		projectItem["requests"] = projectItem["requests"].(int64) + 1

		sourceName := sourceBucketName(log.UpstreamName, log.CacheHit)
		sourceItem := ensureCostBucket(sourceAgg, sourceName)
		sourceItem["actual_cost"] = sourceItem["actual_cost"].(int64) + log.CreditsCharged
		sourceItem["baseline_cost"] = sourceItem["baseline_cost"].(int64) + log.BaselineCost
		sourceItem["saved_cost"] = sourceItem["saved_cost"].(int64) + log.SavedCost
		sourceItem["saved_tokens"] = sourceItem["saved_tokens"].(int64) + log.SavedTokens
		sourceItem["requests"] = sourceItem["requests"].(int64) + 1

		governanceName := governanceBucketName(log, trace)
		governanceItem := ensureCostBucket(governanceAgg, governanceName)
		governanceItem["actual_cost"] = governanceItem["actual_cost"].(int64) + log.CreditsCharged
		governanceItem["baseline_cost"] = governanceItem["baseline_cost"].(int64) + log.BaselineCost
		governanceItem["saved_cost"] = governanceItem["saved_cost"].(int64) + log.SavedCost
		governanceItem["saved_tokens"] = governanceItem["saved_tokens"].(int64) + log.SavedTokens
		governanceItem["requests"] = governanceItem["requests"].(int64) + 1

		day := int(log.CreatedAt.In(location).Sub(startTime) / (24 * time.Hour))
		trendItem := trendAgg[day]
		if trendItem == nil {
			continue
		}
		trendItem["actual_cost"] = trendItem["actual_cost"].(int64) + log.CreditsCharged
		trendItem["baseline_cost"] = trendItem["baseline_cost"].(int64) + log.BaselineCost
		trendItem["saved_cost"] = trendItem["saved_cost"].(int64) + log.SavedCost
	}

	projectsData := flattenCostBuckets(projectAgg)
	sourcesData := flattenCostBuckets(sourceAgg)
	governanceData := flattenCostBuckets(governanceAgg)
	sortCostBuckets(projectsData)
	sortCostBuckets(sourcesData)
	sortCostBuckets(governanceData)

	trend := make([]map[string]any, 0, totalDays)
	for day := 0; day < totalDays; day++ {
		trend = append(trend, trendAgg[day])
	}

	w.Header().Set("Content-Type", "application/json")
	writeJSON := map[string]any{
		"generated_at": now.Format(time.RFC3339),
		"time_range": map[string]any{
			"key":   rangeKey,
			"start": startTime.Format(time.RFC3339),
			"end":   endOfDay.Format(time.RFC3339),
		},
		"overview": overview,
		"projects": projectsData,
		"sources":  sourcesData,
		"governance": map[string]any{
			"attribution_model": "primary",
			"notes":             "Savings are attributed to a single dominant governance action per request to avoid double counting.",
			"breakdown":         governanceData,
		},
		"trend": trend,
	}
	_ = json.NewEncoder(w).Encode(writeJSON)
}

func ensureCostBucket(target map[string]map[string]any, name string) map[string]any {
	item := target[name]
	if item != nil {
		return item
	}
	item = map[string]any{
		"name":          name,
		"actual_cost":   int64(0),
		"baseline_cost": int64(0),
		"saved_cost":    int64(0),
		"saved_tokens":  int64(0),
		"requests":      int64(0),
	}
	target[name] = item
	return item
}

func flattenCostBuckets(target map[string]map[string]any) []map[string]any {
	items := make([]map[string]any, 0, len(target))
	for _, item := range target {
		items = append(items, item)
	}
	return items
}

func sortCostBuckets(items []map[string]any) {
	sort.Slice(items, func(i, j int) bool {
		left := items[i]["actual_cost"].(int64)
		right := items[j]["actual_cost"].(int64)
		if left == right {
			return items[i]["name"].(string) < items[j]["name"].(string)
		}
		return left > right
	})
}

func sourceBucketName(upstreamName string, cacheHit bool) string {
	if cacheHit {
		return "Cache Reuse"
	}
	if strings.HasPrefix(upstreamName, "source:") {
		return "BYOK Sources"
	}
	if strings.TrimSpace(upstreamName) == "" {
		return "Unknown"
	}
	return "Managed Sources"
}

func governanceBucketName(log store.UsageLog, trace store.RouteTrace) string {
	if log.CacheHit {
		return "Cache Reuse"
	}
	if trace.CompressionApplied {
		return "Context Compression"
	}
	if trace.SearchApplied {
		return "Search Enhancement"
	}
	if routeOptimizationApplied(trace) {
		return "Routing Optimization"
	}
	return "Standard Path"
}

func routeOptimizationApplied(trace store.RouteTrace) bool {
	if strings.TrimSpace(trace.RouteTier) != "" {
		return true
	}
	originalModel := strings.TrimSpace(trace.OriginalModel)
	finalModel := strings.TrimSpace(trace.FinalModel)
	if originalModel != "" && finalModel != "" && originalModel != finalModel {
		return true
	}
	return false
}

func normalizeCostRange(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "7d":
		return "7d"
	case "30d":
		return "30d"
	default:
		return "today"
	}
}

func costRangeWindow(startOfToday time.Time, rangeKey string) (time.Time, time.Time) {
	switch rangeKey {
	case "7d":
		return startOfToday.AddDate(0, 0, -6), startOfToday.Add(24 * time.Hour)
	case "30d":
		return startOfToday.AddDate(0, 0, -29), startOfToday.Add(24 * time.Hour)
	default:
		return startOfToday, startOfToday.Add(24 * time.Hour)
	}
}
