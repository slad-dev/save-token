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

type adminOverviewResponse struct {
	GeneratedAt string                 `json:"generated_at"`
	TimeRange   adminOverviewTimeRange `json:"time_range"`
	Overview    adminOverviewMetrics   `json:"overview"`
	Trend       []adminTrendPoint      `json:"trend"`
	RouteMix    []adminRouteMixItem    `json:"route_mix"`
}

type adminOverviewTimeRange struct {
	Start string `json:"start"`
	End   string `json:"end"`
}

type adminOverviewMetrics struct {
	TotalBalance          int64   `json:"total_balance"`
	TodayCost             int64   `json:"today_cost"`
	TodaySavedCost        int64   `json:"today_saved_cost"`
	SavedTokens           int64   `json:"saved_tokens"`
	CacheHitRate          float64 `json:"cache_hit_rate"`
	RouteOptimizationRate float64 `json:"route_optimization_rate"`
	AverageLatencyMs      int64   `json:"average_latency_ms"`
	RequestsToday         int64   `json:"requests_today"`
	SuccessRate           float64 `json:"success_rate"`
	ActiveKeys            int64   `json:"active_keys"`
}

type adminTrendPoint struct {
	Time    string `json:"time"`
	Cost    int64  `json:"cost"`
	Saved   int64  `json:"saved"`
	Latency int64  `json:"latency"`
}

type adminRouteMixItem struct {
	Label string  `json:"label"`
	Value float64 `json:"value"`
	Tone  string  `json:"tone"`
}

type routeBucket struct {
	Key   string
	Label string
	Tone  string
	Count int64
}

func (h *AdminHandler) Overview(w http.ResponseWriter, r *http.Request) {
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
	startOfDay := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, location)
	endOfDay := startOfDay.Add(24 * time.Hour)

	user, err := h.store.FindUserByID(r.Context(), principal.User.ID)
	if err != nil {
		gateway.WriteJSONError(w, http.StatusInternalServerError, "server_error", "failed to load user summary")
		return
	}
	activeKeys, err := h.store.CountActiveAPIKeysByUser(r.Context(), principal.User.ID)
	if err != nil {
		gateway.WriteJSONError(w, http.StatusInternalServerError, "server_error", "failed to load api key summary")
		return
	}
	logs, err := h.store.ListUsageLogsByUserAndTimeRange(r.Context(), principal.User.ID, startOfDay, endOfDay)
	if err != nil {
		gateway.WriteJSONError(w, http.StatusInternalServerError, "server_error", "failed to load usage summary")
		return
	}
	traces, err := h.store.ListRouteTracesByUserAndTimeRange(r.Context(), principal.User.ID, startOfDay, endOfDay)
	if err != nil {
		gateway.WriteJSONError(w, http.StatusInternalServerError, "server_error", "failed to load route trace summary")
		return
	}

	response := buildAdminOverviewResponse(now, startOfDay, endOfDay, user, activeKeys, logs, traces)

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(response); err != nil {
		gateway.WriteJSONError(w, http.StatusInternalServerError, "server_error", "failed to encode overview response")
		return
	}
}

func buildAdminOverviewResponse(now, startOfDay, endOfDay time.Time, user *store.User, activeKeys int64, logs []store.UsageLog, traces []store.RouteTrace) adminOverviewResponse {
	overview := adminOverviewMetrics{
		TotalBalance: user.Balance,
		ActiveKeys:   activeKeys,
	}

	latencyTotal := int64(0)
	successCount := int64(0)
	cacheHitCount := int64(0)
	optimizedCount := int64(0)

	trendBuckets := make(map[int]*adminTrendPoint, 24)
	for hour := 0; hour < 24; hour++ {
		trendBuckets[hour] = &adminTrendPoint{
			Time:    startOfDay.Add(time.Duration(hour) * time.Hour).Format("15:04"),
			Cost:    0,
			Saved:   0,
			Latency: 0,
		}
	}
	trendLatencyTotals := make(map[int]int64, 24)
	trendLatencyCounts := make(map[int]int64, 24)

	for _, log := range logs {
		overview.TodayCost += log.CreditsCharged
		overview.TodaySavedCost += log.SavedCost
		overview.SavedTokens += log.SavedTokens
		overview.RequestsToday++
		latencyTotal += log.DurationMs

		if log.Success {
			successCount++
		}
		if log.CacheHit {
			cacheHitCount++
		}

		if hour := log.CreatedAt.In(startOfDay.Location()).Hour(); hour >= 0 && hour < 24 {
			bucket := trendBuckets[hour]
			bucket.Cost += log.CreditsCharged
			bucket.Saved += log.SavedCost
			trendLatencyTotals[hour] += log.DurationMs
			trendLatencyCounts[hour]++
		}
	}

	if overview.RequestsToday > 0 {
		overview.AverageLatencyMs = latencyTotal / overview.RequestsToday
		overview.SuccessRate = roundPercentage(float64(successCount), float64(overview.RequestsToday))
		overview.CacheHitRate = roundPercentage(float64(cacheHitCount), float64(overview.RequestsToday))
	}

	routeBuckets := map[string]*routeBucket{
		"cache_reuse":        {Key: "cache_reuse", Label: "Cache Reuse", Tone: "bg-amber-500"},
		"realtime_search":    {Key: "realtime_search", Label: "Realtime + Search", Tone: "bg-emerald-500"},
		"compressed":         {Key: "compressed", Label: "Compressed Context", Tone: "bg-violet-500"},
		"original":           {Key: "original", Label: "Original Model", Tone: "bg-slate-400"},
		"route_tier_cheap":   {Key: "route_tier_cheap", Label: "Cheap Tier", Tone: "bg-blue-500"},
		"route_tier_premium": {Key: "route_tier_premium", Label: "Premium Tier", Tone: "bg-slate-900"},
	}

	for _, trace := range traces {
		if isOptimizedRouteTrace(trace) {
			optimizedCount++
		}

		bucketKey, bucketLabel, bucketTone := classifyRouteBucket(trace)
		bucket, ok := routeBuckets[bucketKey]
		if !ok {
			bucket = &routeBucket{
				Key:   bucketKey,
				Label: bucketLabel,
				Tone:  bucketTone,
			}
			routeBuckets[bucketKey] = bucket
		}
		bucket.Count++
	}

	if len(traces) > 0 {
		overview.RouteOptimizationRate = roundPercentage(float64(optimizedCount), float64(len(traces)))
	}

	trend := make([]adminTrendPoint, 0, 24)
	for hour := 0; hour < 24; hour++ {
		bucket := trendBuckets[hour]
		if count := trendLatencyCounts[hour]; count > 0 {
			bucket.Latency = trendLatencyTotals[hour] / count
		}
		trend = append(trend, *bucket)
	}

	routeMix := make([]adminRouteMixItem, 0, len(routeBuckets))
	if len(traces) > 0 {
		for _, bucket := range routeBuckets {
			if bucket.Count == 0 {
				continue
			}
			routeMix = append(routeMix, adminRouteMixItem{
				Label: bucket.Label,
				Value: roundPercentage(float64(bucket.Count), float64(len(traces))),
				Tone:  bucket.Tone,
			})
		}
		sort.Slice(routeMix, func(i, j int) bool {
			if routeMix[i].Value == routeMix[j].Value {
				return routeMix[i].Label < routeMix[j].Label
			}
			return routeMix[i].Value > routeMix[j].Value
		})
	}

	return adminOverviewResponse{
		GeneratedAt: now.Format(time.RFC3339),
		TimeRange: adminOverviewTimeRange{
			Start: startOfDay.Format(time.RFC3339),
			End:   endOfDay.Format(time.RFC3339),
		},
		Overview: overview,
		Trend:    trend,
		RouteMix: routeMix,
	}
}

func isOptimizedRouteTrace(trace store.RouteTrace) bool {
	return trace.CacheHit ||
		trace.SearchApplied ||
		trace.CompressionApplied ||
		(strings.TrimSpace(trace.RouteTier) != "" && trace.FinalModel != trace.OriginalModel)
}

func classifyRouteBucket(trace store.RouteTrace) (string, string, string) {
	switch {
	case trace.CacheHit:
		return "cache_reuse", "Cache Reuse", "bg-amber-500"
	case trace.SearchApplied:
		return "realtime_search", "Realtime + Search", "bg-emerald-500"
	case strings.EqualFold(strings.TrimSpace(trace.RouteTier), "cheap"):
		return "route_tier_cheap", "Cheap Tier", "bg-blue-500"
	case strings.EqualFold(strings.TrimSpace(trace.RouteTier), "premium"):
		return "route_tier_premium", "Premium Tier", "bg-slate-900"
	case strings.TrimSpace(trace.RouteTier) != "":
		label := strings.TrimSpace(trace.RouteTier)
		return "route_tier_" + strings.ToLower(label), strings.Title(label) + " Tier", "bg-sky-500"
	case trace.CompressionApplied:
		return "compressed", "Compressed Context", "bg-violet-500"
	default:
		return "original", "Original Model", "bg-slate-400"
	}
}

func roundPercentage(numerator, denominator float64) float64 {
	if denominator <= 0 {
		return 0
	}
	percentage := (numerator / denominator) * 100
	return float64(int(percentage*10+0.5)) / 10
}
