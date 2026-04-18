package handler

import (
	"encoding/json"
	"net/http"
	"time"

	"agent-gateway/internal/gateway"
	"agent-gateway/internal/store"
)

type adminSystemSummary struct {
	GeneratedAt string         `json:"generated_at"`
	Runtime     map[string]any `json:"runtime"`
	Auth        map[string]any `json:"auth"`
	Governance  map[string]any `json:"governance"`
	RateLimit   map[string]any `json:"rate_limit"`
	Resource    map[string]any `json:"resource"`
}

func (h *AdminHandler) SystemSummary(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		gateway.WriteJSONError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		return
	}
	if _, ok := h.requireAdmin(w, r); !ok {
		return
	}

	_, userTotal, err := h.store.ListUsers(r.Context(), store.ListOptions{Limit: 1})
	if err != nil {
		gateway.WriteJSONError(w, http.StatusInternalServerError, "server_error", "failed to load user summary")
		return
	}
	_, apiKeyTotal, err := h.store.ListAPIKeys(r.Context(), store.ListOptions{Limit: 1})
	if err != nil {
		gateway.WriteJSONError(w, http.StatusInternalServerError, "server_error", "failed to load key summary")
		return
	}
	models, err := h.store.ListManagedModels(r.Context())
	if err != nil {
		gateway.WriteJSONError(w, http.StatusInternalServerError, "server_error", "failed to load model summary")
		return
	}

	enabledModels := 0
	for _, model := range models {
		if model.Enabled {
			enabledModels++
		}
	}

	response := adminSystemSummary{
		GeneratedAt: time.Now().Format(time.RFC3339),
		Runtime: map[string]any{
			"listen_addr":       h.cfg.Server.ListenAddr,
			"frontend_base_url": h.cfg.Auth.FrontendBaseURL,
			"database_path":     h.cfg.Database.Path,
		},
		Auth: map[string]any{
			"cookie_name":           h.cfg.Auth.CookieName,
			"secure_cookie":         h.cfg.Auth.SecureCookie,
			"github_enabled":        h.cfg.Auth.GitHub.Enabled,
			"email_code_enabled":    h.cfg.Auth.EmailCode.Enabled,
			"email_code_dev_mode":   h.cfg.Auth.EmailCode.DevMode,
			"allowed_email_domains": h.cfg.Auth.EmailCode.AllowedDomains,
		},
		Governance: map[string]any{
			"cache_enabled":          h.cfg.Cache.Enabled,
			"semantic_cache_enabled": h.cfg.Cache.SemanticEnabled,
			"web_search_enabled":     h.cfg.Intelligent.WebSearch.Enabled,
			"web_search_provider":    h.cfg.Intelligent.WebSearch.Provider,
			"rag_enabled":            h.cfg.Intelligent.RAG.Enabled,
			"routing_enabled":        h.cfg.Intelligent.Routing.Enabled,
		},
		RateLimit: map[string]any{
			"enabled":             h.cfg.RateLimit.Enabled,
			"requests_per_minute": h.cfg.RateLimit.RequestsPerMinute,
			"burst":               h.cfg.RateLimit.Burst,
		},
		Resource: map[string]any{
			"total_users":    userTotal,
			"total_api_keys": apiKeyTotal,
			"total_models":   len(models),
			"enabled_models": enabledModels,
		},
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(response)
}
