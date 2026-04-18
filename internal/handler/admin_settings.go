package handler

import (
	"encoding/json"
	"net/http"
	"strings"

	"agent-gateway/internal/config"
	"agent-gateway/internal/gateway"
)

type adminSettingsResponse struct {
	Config       map[string]any    `json:"config"`
	SiteSettings map[string]string `json:"site_settings"`
}

type updateSiteSettingsRequest struct {
	SiteSettings map[string]string `json:"site_settings"`
}

type updateAdminSettingsRequest struct {
	FrontendBaseURL string `json:"frontend_base_url"`
	SecureCookie    bool   `json:"secure_cookie"`

	GitHubEnabled      bool   `json:"github_enabled"`
	GitHubClientID     string `json:"github_client_id"`
	GitHubClientSecret string `json:"github_client_secret"`
	GitHubRedirectURL  string `json:"github_redirect_url"`

	EmailCodeEnabled   bool   `json:"email_code_enabled"`
	EmailCodeDevMode   bool   `json:"email_code_dev_mode"`
	EmailCodeSubject   string `json:"email_code_subject"`
	EmailCodeFromName  string `json:"email_code_from_name"`
	AllowedDomainsText string `json:"allowed_domains_text"`
	SMTPHost           string `json:"smtp_host"`
	SMTPPort           int    `json:"smtp_port"`
	SMTPUsername       string `json:"smtp_username"`
	SMTPPassword       string `json:"smtp_password"`
	SMTPFromEmail      string `json:"smtp_from_email"`

	RateLimitEnabled    bool `json:"rate_limit_enabled"`
	RequestsPerMinute   int  `json:"requests_per_minute"`
	RateLimitBurst      int  `json:"rate_limit_burst"`
	CacheEnabled        bool `json:"cache_enabled"`
	SemanticCacheEnable bool `json:"semantic_cache_enabled"`
	WebSearchEnabled    bool `json:"web_search_enabled"`
	RoutingEnabled      bool `json:"routing_enabled"`
	RAGEnabled          bool `json:"rag_enabled"`

	SiteSettings map[string]string `json:"site_settings"`
}

func (h *AdminHandler) GetSettings(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		gateway.WriteJSONError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		return
	}
	if _, ok := h.requireAdmin(w, r); !ok {
		return
	}

	siteSettings, err := h.store.GetSiteSettings(r.Context())
	if err != nil {
		gateway.WriteJSONError(w, http.StatusInternalServerError, "server_error", "failed to load site settings")
		return
	}

	response := adminSettingsResponse{
		Config: map[string]any{
			"frontend_base_url":      h.cfg.Auth.FrontendBaseURL,
			"secure_cookie":          h.cfg.Auth.SecureCookie,
			"github_enabled":         h.cfg.Auth.GitHub.Enabled,
			"github_client_id":       h.cfg.Auth.GitHub.ClientID,
			"github_client_secret":   h.cfg.Auth.GitHub.ClientSecret,
			"github_redirect_url":    h.cfg.Auth.GitHub.RedirectURL,
			"email_code_enabled":     h.cfg.Auth.EmailCode.Enabled,
			"email_code_dev_mode":    h.cfg.Auth.EmailCode.DevMode,
			"email_code_subject":     h.cfg.Auth.EmailCode.Subject,
			"email_code_from_name":   h.cfg.Auth.EmailCode.FromName,
			"allowed_domains":        h.cfg.Auth.EmailCode.AllowedDomains,
			"smtp_host":              h.cfg.Auth.EmailCode.SMTP.Host,
			"smtp_port":              h.cfg.Auth.EmailCode.SMTP.Port,
			"smtp_username":          h.cfg.Auth.EmailCode.SMTP.Username,
			"smtp_password":          h.cfg.Auth.EmailCode.SMTP.Password,
			"smtp_from_email":        h.cfg.Auth.EmailCode.SMTP.FromEmail,
			"rate_limit_enabled":     h.cfg.RateLimit.Enabled,
			"requests_per_minute":    h.cfg.RateLimit.RequestsPerMinute,
			"rate_limit_burst":       h.cfg.RateLimit.Burst,
			"cache_enabled":          h.cfg.Cache.Enabled,
			"semantic_cache_enabled": h.cfg.Cache.SemanticEnabled,
			"web_search_enabled":     h.cfg.Intelligent.WebSearch.Enabled,
			"routing_enabled":        h.cfg.Intelligent.Routing.Enabled,
			"rag_enabled":            h.cfg.Intelligent.RAG.Enabled,
		},
		SiteSettings: mergeDefaultSiteSettings(siteSettings),
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(response)
}

func (h *AdminHandler) UpdateSettings(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPatch {
		gateway.WriteJSONError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		return
	}
	if _, ok := h.requireAdmin(w, r); !ok {
		return
	}

	var payload updateAdminSettingsRequest
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		gateway.WriteJSONError(w, http.StatusBadRequest, "invalid_request_error", "request body must be valid JSON")
		return
	}

	h.cfg.Auth.FrontendBaseURL = strings.TrimSpace(payload.FrontendBaseURL)
	h.cfg.Auth.SecureCookie = payload.SecureCookie
	h.cfg.Auth.GitHub.Enabled = payload.GitHubEnabled
	h.cfg.Auth.GitHub.ClientID = strings.TrimSpace(payload.GitHubClientID)
	h.cfg.Auth.GitHub.ClientSecret = strings.TrimSpace(payload.GitHubClientSecret)
	h.cfg.Auth.GitHub.RedirectURL = strings.TrimSpace(payload.GitHubRedirectURL)

	h.cfg.Auth.EmailCode.Enabled = payload.EmailCodeEnabled
	h.cfg.Auth.EmailCode.DevMode = payload.EmailCodeDevMode
	h.cfg.Auth.EmailCode.Subject = strings.TrimSpace(payload.EmailCodeSubject)
	h.cfg.Auth.EmailCode.FromName = strings.TrimSpace(payload.EmailCodeFromName)
	h.cfg.Auth.EmailCode.AllowedDomains = splitCSV(payload.AllowedDomainsText)
	h.cfg.Auth.EmailCode.SMTP.Host = strings.TrimSpace(payload.SMTPHost)
	h.cfg.Auth.EmailCode.SMTP.Port = payload.SMTPPort
	h.cfg.Auth.EmailCode.SMTP.Username = strings.TrimSpace(payload.SMTPUsername)
	if strings.TrimSpace(payload.SMTPPassword) != "" {
		h.cfg.Auth.EmailCode.SMTP.Password = strings.TrimSpace(payload.SMTPPassword)
	}
	h.cfg.Auth.EmailCode.SMTP.FromEmail = strings.TrimSpace(payload.SMTPFromEmail)

	h.cfg.RateLimit.Enabled = payload.RateLimitEnabled
	h.cfg.RateLimit.RequestsPerMinute = payload.RequestsPerMinute
	h.cfg.RateLimit.Burst = payload.RateLimitBurst
	h.cfg.Cache.Enabled = payload.CacheEnabled
	h.cfg.Cache.SemanticEnabled = payload.SemanticCacheEnable
	h.cfg.Intelligent.WebSearch.Enabled = payload.WebSearchEnabled
	h.cfg.Intelligent.Routing.Enabled = payload.RoutingEnabled
	h.cfg.Intelligent.RAG.Enabled = payload.RAGEnabled

	if err := config.Save(h.cfg); err != nil {
		gateway.WriteJSONError(w, http.StatusBadRequest, "invalid_request_error", err.Error())
		return
	}

	if err := h.store.UpsertSiteSettings(r.Context(), mergeDefaultSiteSettings(payload.SiteSettings)); err != nil {
		gateway.WriteJSONError(w, http.StatusInternalServerError, "server_error", "failed to save site settings")
		return
	}

	h.GetSettings(w, r)
}

func (h *AdminHandler) UpdateSiteSettings(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPatch {
		gateway.WriteJSONError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		return
	}
	if _, ok := h.requireAdmin(w, r); !ok {
		return
	}

	var payload updateSiteSettingsRequest
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		gateway.WriteJSONError(w, http.StatusBadRequest, "invalid_request_error", "request body must be valid JSON")
		return
	}

	current, err := h.store.GetSiteSettings(r.Context())
	if err != nil {
		gateway.WriteJSONError(w, http.StatusInternalServerError, "server_error", "failed to load current site settings")
		return
	}
	merged := mergeDefaultSiteSettings(current)
	for key, value := range payload.SiteSettings {
		merged[key] = strings.TrimSpace(value)
	}

	if err := h.store.UpsertSiteSettings(r.Context(), merged); err != nil {
		gateway.WriteJSONError(w, http.StatusInternalServerError, "server_error", "failed to save site settings")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(merged)
}

func (h *AdminHandler) PublicSiteSettings(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		gateway.WriteJSONError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		return
	}

	siteSettings, err := h.store.GetSiteSettings(r.Context())
	if err != nil {
		gateway.WriteJSONError(w, http.StatusInternalServerError, "server_error", "failed to load site settings")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(mergeDefaultSiteSettings(siteSettings))
}

func splitCSV(value string) []string {
	items := strings.Split(value, ",")
	result := make([]string, 0, len(items))
	for _, item := range items {
		trimmed := strings.TrimSpace(item)
		if trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}

func mergeDefaultSiteSettings(values map[string]string) map[string]string {
	result := map[string]string{
		"site_name":             "Agent Gateway",
		"hero_badge":            "AI 中转与治理网关",
		"hero_title":            "让企业更省钱、更稳定地使用 AI API",
		"hero_description":      "兼容主流模型接口，统一接入、统一治理、统一统计，让用户更容易理解你的价值，也更愿意留下来继续使用。",
		"primary_button_text":   "获取 API 密钥",
		"secondary_button_text": "查看接入文档",
		"announcement":          "所有请求统一经过网关，便于治理、统计和后续优化。",
		"support_email":         "",
		"docs_url":              "/docs",
		"logo_url":              "",
		"favicon_url":           "",
		"feature_1_title":       "统一接入所有模型",
		"feature_1_desc":        "无论用户使用平台模型，还是接入其它平台 API，都可以通过统一入口完成调用。",
		"feature_2_title":       "所有请求统一走治理网关",
		"feature_2_desc":        "这样才能统一做统计、限流、缓存、治理和后续优化，也更方便给客户展示你的价值。",
		"feature_3_title":       "节省成本看得见",
		"feature_3_desc":        "把调用成本、节省金额、治理命中率直接展示出来，让客户知道为什么要长期使用你。",
		"feature_4_title":       "稳定、安全、可管理",
		"feature_4_desc":        "你可以在后台直接管理模型、用户、密钥和网站配置，逐步形成完整的运营体系。",
		"saving_1_title":        "上下文压缩",
		"saving_1_desc":         "对于长上下文请求，网关会在合适场景下降低无效 Token 消耗。",
		"saving_2_title":        "缓存复用",
		"saving_2_desc":         "对高重复请求尽量复用结果，减少重复调用和重复花费。",
		"saving_3_title":        "智能路由",
		"saving_3_desc":         "根据请求类型和治理策略，分配更合适的模型与路径。",
		"flow_1":                "客户提交请求",
		"flow_2":                "请求先进入你的治理网关",
		"flow_3":                "网关完成鉴权、统计与治理判断",
		"flow_4":                "再路由到对应的模型来源",
		"flow_5":                "返回结果给客户并沉淀成本数据",
		"flow_6":                "后台持续展示节省效果与运行状态",
		"faq_1_q":               "为什么客户自己的 API 也要走网关？",
		"faq_1_a":               "因为只有统一经过网关，才能做治理、统计、缓存、路由和节省效果分析，这也是你的核心产品价值。",
		"faq_2_q":               "打开网站为什么不强制登录？",
		"faq_2_a":               "先让用户自由浏览，理解你的网站是做什么的；只有在点击获取 API 密钥时，再弹出登录或注册，更有利于转化。",
		"faq_3_q":               "后面还能继续细化治理吗？",
		"faq_3_a":               "可以。当前先把网站与后台跑通，后续再继续加强意图判断、隐私治理、路由策略和运营能力。",
		"provider_icon_map":     "{\"openai\":\"sparkles\",\"anthropic\":\"brain\",\"gemini\":\"gem\",\"deepseek\":\"bot\",\"siliconflow\":\"orbit\",\"openrouter\":\"route\"}",
	}
	for key, value := range values {
		result[key] = strings.TrimSpace(value)
	}
	return result
}
