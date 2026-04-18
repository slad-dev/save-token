package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
)

type Config struct {
	Server      ServerConfig      `json:"server"`
	Database    DatabaseConfig    `json:"database"`
	Intelligent IntelligentConfig `json:"intelligent"`
	Billing     BillingConfig     `json:"billing"`
	Cache       CacheConfig       `json:"cache"`
	RateLimit   RateLimitConfig   `json:"rate_limit"`
	Auth        AuthConfig        `json:"auth"`
	Local       LocalConfig       `json:"local"`
	Bootstrap   BootstrapConfig   `json:"bootstrap"`
}

func ResolvedPath() string {
	path := os.Getenv("CONFIG_PATH")
	if path == "" {
		path = "config.json"
	}
	return path
}

type ServerConfig struct {
	ListenAddr      string   `json:"listen_addr"`
	UpstreamTimeout Duration `json:"upstream_timeout"`
}

type DatabaseConfig struct {
	Path string `json:"path"`
}

type IntelligentConfig struct {
	WebSearch WebSearchConfig `json:"web_search"`
	RAG       RAGConfig       `json:"rag"`
	Routing   RoutingConfig   `json:"routing"`
}

type WebSearchConfig struct {
	Enabled    bool     `json:"enabled"`
	Provider   string   `json:"provider"`
	APIKey     string   `json:"api_key"`
	BaseURL    string   `json:"base_url"`
	MaxResults int      `json:"max_results"`
	Timeout    Duration `json:"timeout"`
}

type RAGConfig struct {
	Enabled                   bool `json:"enabled"`
	MaxInputCharacters        int  `json:"max_input_characters"`
	MaxEstimatedTokens        int  `json:"max_estimated_tokens"`
	CompressionThresholdChars int  `json:"compression_threshold_chars"`
	KeepRecentMessages        int  `json:"keep_recent_messages"`
	SummaryMaxChars           int  `json:"summary_max_chars"`
}

type RoutingConfig struct {
	Enabled bool          `json:"enabled"`
	Tiers   []RoutingTier `json:"tiers"`
	Rules   []RoutingRule `json:"rules"`
}

type RoutingTier struct {
	Name  string `json:"name"`
	Model string `json:"model"`
}

type RoutingRule struct {
	Name            string `json:"name"`
	IntentClass     string `json:"intent_class"`
	ModelPattern    string `json:"model_pattern"`
	MinInputChars   int    `json:"min_input_chars"`
	RouteTier       string `json:"route_tier"`
	EnableWebSearch *bool  `json:"enable_web_search"`
}

type BillingConfig struct {
	Enabled               bool         `json:"enabled"`
	DefaultChatPer1K      int64        `json:"default_chat_per_1k"`
	DefaultEmbeddingPer1K int64        `json:"default_embedding_per_1k"`
	MinimumRequestCharge  int64        `json:"minimum_request_charge"`
	WebSearchSurcharge    int64        `json:"web_search_surcharge"`
	ModelPricing          []ModelPrice `json:"model_pricing"`
}

type ModelPrice struct {
	ModelPattern string `json:"model_pattern"`
	Per1KTokens  int64  `json:"per_1k_tokens"`
	RequestKind  string `json:"request_kind"`
}

type CacheConfig struct {
	Enabled               bool                    `json:"enabled"`
	ExactChatTTL          Duration                `json:"exact_chat_ttl"`
	MaxBodyBytes          int                     `json:"max_body_bytes"`
	SemanticEnabled       bool                    `json:"semantic_enabled"`
	SemanticSimilarity    float64                 `json:"semantic_similarity"`
	SemanticMaxCandidates int                     `json:"semantic_max_candidates"`
	SemanticEmbedding     SemanticEmbeddingConfig `json:"semantic_embedding"`
}

type SemanticEmbeddingConfig struct {
	Provider string   `json:"provider"`
	BaseURL  string   `json:"base_url"`
	Model    string   `json:"model"`
	Timeout  Duration `json:"timeout"`
}

type RateLimitConfig struct {
	Enabled           bool `json:"enabled"`
	RequestsPerMinute int  `json:"requests_per_minute"`
	Burst             int  `json:"burst"`
}

type AuthConfig struct {
	CookieName      string           `json:"cookie_name"`
	SessionTTL      Duration         `json:"session_ttl"`
	SecureCookie    bool             `json:"secure_cookie"`
	FrontendBaseURL string           `json:"frontend_base_url"`
	EmailCode       EmailCodeConfig  `json:"email_code"`
	GitHub          GitHubAuthConfig `json:"github"`
}

type GitHubAuthConfig struct {
	Enabled      bool     `json:"enabled"`
	ClientID     string   `json:"client_id"`
	ClientSecret string   `json:"client_secret"`
	RedirectURL  string   `json:"redirect_url"`
	HTTPTimeout  Duration `json:"http_timeout"`
}

type EmailCodeConfig struct {
	Enabled        bool           `json:"enabled"`
	DevMode        bool           `json:"dev_mode"`
	OTPTTL         Duration       `json:"otp_ttl"`
	CodeLength     int            `json:"code_length"`
	Subject        string         `json:"subject"`
	FromName       string         `json:"from_name"`
	SMTP           SMTPAuthConfig `json:"smtp"`
	AllowedDomains []string       `json:"allowed_domains"`
}

type LocalConfig struct {
	Enabled         bool   `json:"enabled"`
	DefaultBaseURL  string `json:"default_base_url"`
	DefaultStrategy string `json:"default_strategy"`
	AutoOpenBrowser bool   `json:"auto_open_browser"`
}

type SMTPAuthConfig struct {
	Host      string `json:"host"`
	Port      int    `json:"port"`
	Username  string `json:"username"`
	Password  string `json:"password"`
	FromEmail string `json:"from_email"`
}

type BootstrapConfig struct {
	Upstreams   []UpstreamSeed   `json:"upstreams"`
	ModelRoutes []ModelRouteSeed `json:"model_routes"`
	Users       []UserSeed       `json:"users"`
	APIKeys     []APIKeySeed     `json:"api_keys"`
}

type UpstreamSeed struct {
	Name            string   `json:"name"`
	BaseURL         string   `json:"base_url"`
	APIKey          string   `json:"api_key"`
	Weight          int      `json:"weight"`
	Enabled         *bool    `json:"enabled"`
	SupportedModels []string `json:"supported_models"`
}

type ModelRouteSeed struct {
	ModelPattern string   `json:"model_pattern"`
	Strategy     string   `json:"strategy"`
	Upstreams    []string `json:"upstreams"`
}

type UserSeed struct {
	Name     string `json:"name"`
	Balance  int64  `json:"balance"`
	Plan     string `json:"plan"`
	IsActive *bool  `json:"is_active"`
}

type APIKeySeed struct {
	Name           string `json:"name"`
	Key            string `json:"key"`
	UserName       string `json:"user_name"`
	AllowWebSearch *bool  `json:"allow_web_search"`
	IsActive       *bool  `json:"is_active"`
}

func Load() (*Config, error) {
	path := ResolvedPath()

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}

	cfg := defaultConfig()
	if err := json.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}

	if err := cfg.validate(); err != nil {
		return nil, err
	}

	return cfg, nil
}

func Save(cfg *Config) error {
	if cfg == nil {
		return errors.New("config is nil")
	}
	if err := cfg.validate(); err != nil {
		return err
	}

	path := ResolvedPath()
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal %s: %w", path, err)
	}
	data = append(data, '\n')

	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

func defaultConfig() *Config {
	return &Config{
		Server: ServerConfig{
			ListenAddr:      ":8080",
			UpstreamTimeout: Duration(120 * time.Second),
		},
		Database: DatabaseConfig{
			Path: "data/gateway.db",
		},
		Intelligent: IntelligentConfig{
			WebSearch: WebSearchConfig{
				Enabled:    false,
				Provider:   "tavily",
				BaseURL:    "https://api.tavily.com",
				MaxResults: 5,
				Timeout:    Duration(15 * time.Second),
			},
			RAG: RAGConfig{
				Enabled:                   false,
				MaxInputCharacters:        128000,
				MaxEstimatedTokens:        32000,
				CompressionThresholdChars: 50000,
				KeepRecentMessages:        8,
				SummaryMaxChars:           2000,
			},
			Routing: RoutingConfig{
				Enabled: false,
			},
		},
		Billing: BillingConfig{
			Enabled:               true,
			DefaultChatPer1K:      100,
			DefaultEmbeddingPer1K: 20,
			MinimumRequestCharge:  1,
			WebSearchSurcharge:    5,
		},
		Cache: CacheConfig{
			Enabled:               true,
			ExactChatTTL:          Duration(10 * time.Minute),
			MaxBodyBytes:          1 << 20,
			SemanticEnabled:       true,
			SemanticSimilarity:    0.90,
			SemanticMaxCandidates: 25,
			SemanticEmbedding: SemanticEmbeddingConfig{
				Provider: "ollama",
				BaseURL:  "http://127.0.0.1:11434",
				Model:    "nomic-embed-text",
				Timeout:  Duration(15 * time.Second),
			},
		},
		RateLimit: RateLimitConfig{
			Enabled:           true,
			RequestsPerMinute: 60,
			Burst:             20,
		},
		Auth: AuthConfig{
			CookieName:      "ag_console_session",
			SessionTTL:      Duration(7 * 24 * time.Hour),
			SecureCookie:    false,
			FrontendBaseURL: "http://localhost:3000",
			EmailCode: EmailCodeConfig{
				Enabled:    false,
				DevMode:    false,
				OTPTTL:     Duration(10 * time.Minute),
				CodeLength: 6,
				Subject:    "Agent Gateway 登录验证码",
				FromName:   "Agent Gateway",
				SMTP: SMTPAuthConfig{
					Port: 465,
				},
			},
			GitHub: GitHubAuthConfig{
				Enabled:     false,
				HTTPTimeout: Duration(15 * time.Second),
			},
		},
		Local: LocalConfig{
			Enabled:         false,
			DefaultStrategy: "balanced",
			AutoOpenBrowser: true,
		},
	}
}

func (c *Config) validate() error {
	if c.Server.ListenAddr == "" {
		return errors.New("server.listen_addr is required")
	}

	if time.Duration(c.Server.UpstreamTimeout) <= 0 {
		return errors.New("server.upstream_timeout must be greater than 0")
	}

	if c.Database.Path == "" {
		return errors.New("database.path is required")
	}

	c.Local.DefaultBaseURL = strings.TrimSpace(c.Local.DefaultBaseURL)
	c.Local.DefaultStrategy = strings.ToLower(strings.TrimSpace(c.Local.DefaultStrategy))
	if c.Local.DefaultStrategy == "" {
		c.Local.DefaultStrategy = "balanced"
	}

	if c.Intelligent.WebSearch.Enabled {
		if c.Intelligent.WebSearch.Provider == "" {
			c.Intelligent.WebSearch.Provider = "tavily"
		}
		if c.Intelligent.WebSearch.BaseURL == "" {
			c.Intelligent.WebSearch.BaseURL = "https://api.tavily.com"
		}
		if c.Intelligent.WebSearch.MaxResults <= 0 {
			c.Intelligent.WebSearch.MaxResults = 5
		}
		if time.Duration(c.Intelligent.WebSearch.Timeout) <= 0 {
			c.Intelligent.WebSearch.Timeout = Duration(15 * time.Second)
		}
		if c.Intelligent.WebSearch.APIKey == "" {
			return errors.New("intelligent.web_search.api_key is required when web_search is enabled")
		}
	}

	if c.Intelligent.RAG.MaxInputCharacters <= 0 {
		c.Intelligent.RAG.MaxInputCharacters = 128000
	}
	if c.Intelligent.RAG.MaxEstimatedTokens <= 0 {
		c.Intelligent.RAG.MaxEstimatedTokens = 32000
	}
	if c.Intelligent.RAG.CompressionThresholdChars <= 0 {
		c.Intelligent.RAG.CompressionThresholdChars = 50000
	}
	if c.Intelligent.RAG.KeepRecentMessages <= 0 {
		c.Intelligent.RAG.KeepRecentMessages = 8
	}
	if c.Intelligent.RAG.SummaryMaxChars <= 0 {
		c.Intelligent.RAG.SummaryMaxChars = 2000
	}

	for i, tier := range c.Intelligent.Routing.Tiers {
		if tier.Name == "" {
			return fmt.Errorf("intelligent.routing.tiers[%d].name is required", i)
		}
		if tier.Model == "" {
			return fmt.Errorf("intelligent.routing.tiers[%d].model is required", i)
		}
	}
	for i, rule := range c.Intelligent.Routing.Rules {
		if rule.Name == "" {
			return fmt.Errorf("intelligent.routing.rules[%d].name is required", i)
		}
		if rule.IntentClass == "" {
			rule.IntentClass = "*"
			c.Intelligent.Routing.Rules[i] = rule
		}
		if rule.ModelPattern == "" {
			rule.ModelPattern = "*"
			c.Intelligent.Routing.Rules[i] = rule
		}
	}

	if c.Billing.DefaultChatPer1K <= 0 {
		c.Billing.DefaultChatPer1K = 100
	}
	if c.Billing.DefaultEmbeddingPer1K <= 0 {
		c.Billing.DefaultEmbeddingPer1K = 20
	}
	if c.Billing.MinimumRequestCharge <= 0 {
		c.Billing.MinimumRequestCharge = 1
	}
	if c.Billing.WebSearchSurcharge < 0 {
		c.Billing.WebSearchSurcharge = 0
	}
	for i, pricing := range c.Billing.ModelPricing {
		if pricing.ModelPattern == "" {
			return fmt.Errorf("billing.model_pricing[%d].model_pattern is required", i)
		}
		if pricing.Per1KTokens <= 0 {
			return fmt.Errorf("billing.model_pricing[%d].per_1k_tokens must be greater than 0", i)
		}
		if pricing.RequestKind == "" {
			pricing.RequestKind = "chat"
			c.Billing.ModelPricing[i] = pricing
		}
	}

	if c.RateLimit.RequestsPerMinute <= 0 {
		c.RateLimit.RequestsPerMinute = 60
	}
	if c.RateLimit.Burst <= 0 {
		c.RateLimit.Burst = 20
	}
	if time.Duration(c.Cache.ExactChatTTL) <= 0 {
		c.Cache.ExactChatTTL = Duration(10 * time.Minute)
	}
	if c.Cache.MaxBodyBytes <= 0 {
		c.Cache.MaxBodyBytes = 1 << 20
	}
	if c.Cache.SemanticSimilarity <= 0 || c.Cache.SemanticSimilarity > 1 {
		c.Cache.SemanticSimilarity = 0.90
	}
	if c.Cache.SemanticMaxCandidates <= 0 {
		c.Cache.SemanticMaxCandidates = 25
	}
	c.Cache.SemanticEmbedding.Provider = normalizeLower(c.Cache.SemanticEmbedding.Provider)
	if c.Cache.SemanticEmbedding.Provider == "" {
		c.Cache.SemanticEmbedding.Provider = "ollama"
	}
	c.Cache.SemanticEmbedding.BaseURL = strings.TrimSpace(c.Cache.SemanticEmbedding.BaseURL)
	if c.Cache.SemanticEmbedding.BaseURL == "" {
		c.Cache.SemanticEmbedding.BaseURL = "http://127.0.0.1:11434"
	}
	c.Cache.SemanticEmbedding.Model = strings.TrimSpace(c.Cache.SemanticEmbedding.Model)
	if c.Cache.SemanticEmbedding.Model == "" {
		c.Cache.SemanticEmbedding.Model = "nomic-embed-text"
	}
	if time.Duration(c.Cache.SemanticEmbedding.Timeout) <= 0 {
		c.Cache.SemanticEmbedding.Timeout = Duration(15 * time.Second)
	}
	if c.Auth.CookieName == "" {
		c.Auth.CookieName = "ag_console_session"
	}
	if time.Duration(c.Auth.SessionTTL) <= 0 {
		c.Auth.SessionTTL = Duration(7 * 24 * time.Hour)
	}
	if c.Auth.FrontendBaseURL == "" {
		c.Auth.FrontendBaseURL = "http://localhost:3000"
	}
	if time.Duration(c.Auth.EmailCode.OTPTTL) <= 0 {
		c.Auth.EmailCode.OTPTTL = Duration(10 * time.Minute)
	}
	if c.Auth.EmailCode.CodeLength < 4 || c.Auth.EmailCode.CodeLength > 8 {
		c.Auth.EmailCode.CodeLength = 6
	}
	if c.Auth.EmailCode.Subject == "" {
		c.Auth.EmailCode.Subject = "Agent Gateway 登录验证码"
	}
	if c.Auth.EmailCode.FromName == "" {
		c.Auth.EmailCode.FromName = "Agent Gateway"
	}
	if c.Auth.EmailCode.SMTP.Port <= 0 {
		c.Auth.EmailCode.SMTP.Port = 465
	}
	for i := range c.Auth.EmailCode.AllowedDomains {
		c.Auth.EmailCode.AllowedDomains[i] = normalizeLower(c.Auth.EmailCode.AllowedDomains[i])
	}
	if c.Auth.EmailCode.Enabled {
		if c.Auth.EmailCode.SMTP.Host == "" {
			return errors.New("auth.email_code.smtp.host is required when email code login is enabled")
		}
		if c.Auth.EmailCode.SMTP.Username == "" {
			return errors.New("auth.email_code.smtp.username is required when email code login is enabled")
		}
		if c.Auth.EmailCode.SMTP.Password == "" {
			return errors.New("auth.email_code.smtp.password is required when email code login is enabled")
		}
		if c.Auth.EmailCode.SMTP.FromEmail == "" {
			return errors.New("auth.email_code.smtp.from_email is required when email code login is enabled")
		}
	}
	if time.Duration(c.Auth.GitHub.HTTPTimeout) <= 0 {
		c.Auth.GitHub.HTTPTimeout = Duration(15 * time.Second)
	}
	if c.Auth.GitHub.Enabled {
		if c.Auth.GitHub.ClientID == "" {
			return errors.New("auth.github.client_id is required when github auth is enabled")
		}
		if c.Auth.GitHub.ClientSecret == "" {
			return errors.New("auth.github.client_secret is required when github auth is enabled")
		}
		if c.Auth.GitHub.RedirectURL == "" {
			return errors.New("auth.github.redirect_url is required when github auth is enabled")
		}
	}

	if len(c.Bootstrap.Upstreams) == 0 && !c.Local.Enabled {
		return errors.New("bootstrap.upstreams requires at least one upstream")
	}

	upstreamNames := make(map[string]struct{}, len(c.Bootstrap.Upstreams))
	for i, upstream := range c.Bootstrap.Upstreams {
		if upstream.Name == "" {
			return fmt.Errorf("bootstrap.upstreams[%d].name is required", i)
		}
		if upstream.BaseURL == "" {
			return fmt.Errorf("bootstrap.upstreams[%d].base_url is required", i)
		}
		if upstream.APIKey == "" {
			return fmt.Errorf("bootstrap.upstreams[%d].api_key is required", i)
		}
		if upstream.Weight <= 0 {
			upstream.Weight = 1
			c.Bootstrap.Upstreams[i] = upstream
		}
		if upstream.Enabled == nil {
			enabled := true
			upstream.Enabled = &enabled
			c.Bootstrap.Upstreams[i] = upstream
		}
		if _, exists := upstreamNames[upstream.Name]; exists {
			return fmt.Errorf("duplicate upstream name: %s", upstream.Name)
		}
		upstreamNames[upstream.Name] = struct{}{}
	}

	for i, route := range c.Bootstrap.ModelRoutes {
		if route.ModelPattern == "" {
			return fmt.Errorf("bootstrap.model_routes[%d].model_pattern is required", i)
		}
		if route.Strategy == "" {
			route.Strategy = "round_robin"
			c.Bootstrap.ModelRoutes[i] = route
		}
		if len(route.Upstreams) == 0 {
			return fmt.Errorf("bootstrap.model_routes[%d].upstreams is required", i)
		}
		for _, upstreamName := range route.Upstreams {
			if _, ok := upstreamNames[upstreamName]; !ok {
				return fmt.Errorf("bootstrap.model_routes[%d] references unknown upstream %q", i, upstreamName)
			}
		}
	}

	userNames := make(map[string]struct{}, len(c.Bootstrap.Users))
	for i, user := range c.Bootstrap.Users {
		if user.Name == "" {
			return fmt.Errorf("bootstrap.users[%d].name is required", i)
		}
		if user.Plan == "" {
			user.Plan = "free"
			c.Bootstrap.Users[i] = user
		}
		if user.IsActive == nil {
			active := true
			user.IsActive = &active
			c.Bootstrap.Users[i] = user
		}
		if _, exists := userNames[user.Name]; exists {
			return fmt.Errorf("duplicate user name: %s", user.Name)
		}
		userNames[user.Name] = struct{}{}
	}

	apiKeys := make(map[string]struct{}, len(c.Bootstrap.APIKeys))
	for i, apiKey := range c.Bootstrap.APIKeys {
		if apiKey.Name == "" {
			return fmt.Errorf("bootstrap.api_keys[%d].name is required", i)
		}
		if apiKey.Key == "" {
			return fmt.Errorf("bootstrap.api_keys[%d].key is required", i)
		}
		if apiKey.UserName == "" {
			return fmt.Errorf("bootstrap.api_keys[%d].user_name is required", i)
		}
		if _, ok := userNames[apiKey.UserName]; !ok && len(c.Bootstrap.Users) > 0 {
			return fmt.Errorf("bootstrap.api_keys[%d] references unknown user %q", i, apiKey.UserName)
		}
		if apiKey.AllowWebSearch == nil {
			allowed := false
			apiKey.AllowWebSearch = &allowed
			c.Bootstrap.APIKeys[i] = apiKey
		}
		if apiKey.IsActive == nil {
			active := true
			apiKey.IsActive = &active
			c.Bootstrap.APIKeys[i] = apiKey
		}
		if _, exists := apiKeys[apiKey.Key]; exists {
			return fmt.Errorf("duplicate api key: %s", apiKey.Name)
		}
		apiKeys[apiKey.Key] = struct{}{}
	}

	return nil
}

func normalizeLower(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

type Duration time.Duration

func (d Duration) MarshalJSON() ([]byte, error) {
	return json.Marshal(time.Duration(d).String())
}

func (d *Duration) UnmarshalJSON(data []byte) error {
	var asString string
	if err := json.Unmarshal(data, &asString); err == nil {
		parsed, parseErr := time.ParseDuration(asString)
		if parseErr != nil {
			return parseErr
		}
		*d = Duration(parsed)
		return nil
	}

	var asNumber int64
	if err := json.Unmarshal(data, &asNumber); err == nil {
		*d = Duration(time.Duration(asNumber))
		return nil
	}

	return errors.New("duration must be a string like \"120s\" or an integer nanosecond value")
}
