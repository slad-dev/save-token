package localapp

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync"

	"agent-gateway/internal/auth"
	"agent-gateway/internal/config"
	"agent-gateway/internal/store"
)

const (
	StrategyConservative = "conservative"
	StrategyBalanced     = "balanced"
	StrategyAggressive   = "aggressive"

	localSettingsBaseURLKey  = "local.base_url"
	localSettingsAPIKeyKey   = "local.api_key"
	localSettingsStrategyKey = "local.strategy"

	localUserName      = "Local User"
	localProjectName   = "Local Gateway"
	localAPIKeyName    = "Local Gateway Key"
	localAPIKeyValue   = "sk-local-gateway"
	localUpstreamName  = "local-primary"
	localModelPattern  = "*"
	localRouteStrategy = "fixed"
	defaultBaseURL     = "https://your-openai-compatible-host.example/v1"
	recentRequestLimit = 12
)

type AppConfig struct {
	BaseURL    string `json:"base_url"`
	APIKey     string `json:"api_key"`
	Strategy   string `json:"strategy"`
	Configured bool   `json:"configured"`
}

type StrategyDetails struct {
	Key         string `json:"key"`
	Label       string `json:"label"`
	Description string `json:"description"`
}

type Overview struct {
	Requests        int64   `json:"requests"`
	SavedTokens     int64   `json:"saved_tokens"`
	CacheHits       int64   `json:"cache_hits"`
	CompressionHits int64   `json:"compression_hits"`
	LastRequestAt   *string `json:"last_request_at,omitempty"`
}

type RequestRecord struct {
	RequestID          string `json:"request_id"`
	CreatedAt          string `json:"created_at"`
	Endpoint           string `json:"endpoint"`
	OriginalModel      string `json:"original_model"`
	FinalModel         string `json:"final_model"`
	UpstreamName       string `json:"upstream_name"`
	UpstreamKind       string `json:"upstream_kind"`
	StatusCode         int    `json:"status_code"`
	Success            bool   `json:"success"`
	CacheHit           bool   `json:"cache_hit"`
	CompressionApplied bool   `json:"compression_applied"`
	SearchApplied      bool   `json:"search_applied"`
	SavedTokens        int64  `json:"saved_tokens"`
	TotalTokens        int64  `json:"total_tokens"`
	DurationMs         int64  `json:"duration_ms"`
	DecisionReason     string `json:"decision_reason,omitempty"`
	ErrorMessage       string `json:"error_message,omitempty"`
}

type Snapshot struct {
	Config         AppConfig         `json:"config"`
	ProxyBaseURL   string            `json:"proxy_base_url"`
	Strategies     []StrategyDetails `json:"strategies"`
	Overview       Overview          `json:"overview"`
	RecentRequests []RequestRecord   `json:"recent_requests"`
	ExampleAPIKey  string            `json:"example_api_key"`
	RequiresAPIKey bool              `json:"requires_api_key"`
}

type Service struct {
	store           *store.SQLiteStore
	baseAddr        string
	defaultBaseURL  string
	defaultStrategy string

	mu        sync.RWMutex
	config    AppConfig
	principal *auth.Principal
}

func NewService(st *store.SQLiteStore, cfg *config.Config) *Service {
	service := &Service{
		store:           st,
		baseAddr:        strings.TrimSpace(cfg.Server.ListenAddr),
		defaultBaseURL:  firstNonEmpty(strings.TrimSpace(cfg.Local.DefaultBaseURL), defaultBaseURL),
		defaultStrategy: normalizeStrategy(cfg.Local.DefaultStrategy),
		config: AppConfig{
			Strategy: StrategyBalanced,
		},
	}
	if service.defaultStrategy == "" {
		service.defaultStrategy = StrategyBalanced
	}
	service.config.Strategy = normalizeStrategy(service.config.Strategy)
	return service
}

func (s *Service) Bootstrap(ctx context.Context) error {
	cfg, err := s.loadConfig(ctx)
	if err != nil {
		return err
	}
	principal, err := s.ensurePrincipal(ctx, cfg)
	if err != nil {
		return err
	}
	if err := s.applyGatewayConfig(ctx, cfg); err != nil {
		return err
	}
	s.setState(cfg, principal)
	return nil
}

func (s *Service) Snapshot(ctx context.Context, proxyBaseURL string) (Snapshot, error) {
	cfg := s.Config()
	principal, err := s.cachedPrincipal(ctx, cfg)
	if err != nil {
		return Snapshot{}, err
	}
	summary, err := s.store.SummarizeUsage(ctx, principal.User.ID)
	if err != nil {
		return Snapshot{}, err
	}
	recentRequests, err := s.recentRequests(ctx, principal.User.ID)
	if err != nil {
		return Snapshot{}, err
	}

	overview := Overview{
		Requests:        summary.Requests,
		SavedTokens:     summary.SavedTokens,
		CacheHits:       summary.CacheHits,
		CompressionHits: summary.CompressionHits,
	}
	if summary.LastRequestAt != nil {
		value := summary.LastRequestAt.Format("2006-01-02 15:04:05")
		overview.LastRequestAt = &value
	}

	return Snapshot{
		Config:         cfg,
		ProxyBaseURL:   firstNonEmpty(strings.TrimSpace(proxyBaseURL), s.defaultProxyBaseURL()),
		Strategies:     SupportedStrategies(),
		Overview:       overview,
		RecentRequests: recentRequests,
		ExampleAPIKey:  "local-not-used",
		RequiresAPIKey: false,
	}, nil
}

func (s *Service) Save(ctx context.Context, cfg AppConfig) (AppConfig, error) {
	cfg.BaseURL = strings.TrimSpace(cfg.BaseURL)
	cfg.APIKey = strings.TrimSpace(cfg.APIKey)
	cfg.Strategy = normalizeStrategy(cfg.Strategy)
	cfg.Configured = cfg.BaseURL != "" && cfg.APIKey != ""

	if cfg.BaseURL == "" {
		return AppConfig{}, errors.New("base_url is required")
	}
	if cfg.APIKey == "" {
		return AppConfig{}, errors.New("api_key is required")
	}
	if _, err := url.ParseRequestURI(cfg.BaseURL); err != nil {
		return AppConfig{}, fmt.Errorf("base_url is invalid: %w", err)
	}

	if err := s.store.UpsertSiteSettings(ctx, map[string]string{
		localSettingsBaseURLKey:  cfg.BaseURL,
		localSettingsAPIKeyKey:   cfg.APIKey,
		localSettingsStrategyKey: cfg.Strategy,
	}); err != nil {
		return AppConfig{}, err
	}
	if err := s.applyGatewayConfig(ctx, cfg); err != nil {
		return AppConfig{}, err
	}
	principal, err := s.ensurePrincipal(ctx, cfg)
	if err != nil {
		return AppConfig{}, err
	}
	s.setState(cfg, principal)
	return cfg, nil
}

func (s *Service) Config() AppConfig {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.config
}

func (s *Service) CompressionEnabled() bool {
	cfg := s.Config()
	switch normalizeStrategy(cfg.Strategy) {
	case StrategyConservative:
		return false
	case StrategyAggressive:
		return true
	default:
		return true
	}
}

func (s *Service) AggressiveCompressionEnabled() bool {
	cfg := s.Config()
	return normalizeStrategy(cfg.Strategy) == StrategyAggressive
}

func (s *Service) SemanticCacheEnabled() bool {
	cfg := s.Config()
	return normalizeStrategy(cfg.Strategy) != StrategyConservative
}

func (s *Service) BasicSlimmingEnabled() bool {
	cfg := s.Config()
	return normalizeStrategy(cfg.Strategy) != StrategyConservative
}

func (s *Service) AggressiveSlimmingEnabled() bool {
	cfg := s.Config()
	return normalizeStrategy(cfg.Strategy) == StrategyAggressive
}

func (s *Service) OutputConstraintEnabled() bool {
	cfg := s.Config()
	return normalizeStrategy(cfg.Strategy) == StrategyAggressive
}

func (s *Service) Principal(ctx context.Context) (*auth.Principal, error) {
	return s.cachedPrincipal(ctx, s.Config())
}

func SupportedStrategies() []StrategyDetails {
	return []StrategyDetails{
		{
			Key:         StrategyConservative,
			Label:       "保守",
			Description: "仅启用精确缓存与稳定前缀排版，尽量不改写请求内容，适合先稳定接入并观察效果。",
		},
		{
			Key:         StrategyBalanced,
			Label:       "平衡",
			Description: "包含保守模式能力，并开启 embedding 语义缓存、基础输入瘦身与滑窗裁剪，适合大多数日常调用。",
		},
		{
			Key:         StrategyAggressive,
			Label:       "激进",
			Description: "在平衡模式基础上增加更强的 JSON/代码压缩与简洁输出约束，优先追求更高的 token 节省率。",
		},
	}
}

func (s *Service) applyGatewayConfig(ctx context.Context, cfg AppConfig) error {
	return s.store.UpsertManagedUpstream(ctx, localUpstreamName, cfg.BaseURL, cfg.APIKey, nil, localModelPattern, localRouteStrategy)
}

func (s *Service) ensurePrincipal(ctx context.Context, cfg AppConfig) (*auth.Principal, error) {
	apiKey, err := s.store.EnsureLocalIdentity(ctx, store.EnsureLocalIdentityInput{
		UserName:              localUserName,
		ProjectName:           localProjectName,
		APIKeyName:            localAPIKeyName,
		APIKeyValue:           localAPIKeyValue,
		AggressiveCompression: s.strategyAggressive(cfg.Strategy),
	})
	if err != nil {
		return nil, err
	}
	return &auth.Principal{
		User:   apiKey.User,
		APIKey: *apiKey,
	}, nil
}

func (s *Service) loadConfig(ctx context.Context) (AppConfig, error) {
	settings, err := s.store.GetSiteSettings(ctx)
	if err != nil {
		return AppConfig{}, err
	}
	cfg := AppConfig{
		BaseURL:  strings.TrimSpace(settings[localSettingsBaseURLKey]),
		APIKey:   strings.TrimSpace(settings[localSettingsAPIKeyKey]),
		Strategy: normalizeStrategy(settings[localSettingsStrategyKey]),
	}
	if cfg.BaseURL == "" {
		cfg.BaseURL = s.defaultBaseURL
	}
	if cfg.Strategy == "" {
		cfg.Strategy = s.defaultStrategy
	}
	cfg.Configured = cfg.BaseURL != "" && cfg.APIKey != ""
	return cfg, nil
}

func (s *Service) setConfig(cfg AppConfig) {
	s.setState(cfg, s.currentPrincipal())
}

func (s *Service) setState(cfg AppConfig, principal *auth.Principal) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cfg.Strategy = normalizeStrategy(cfg.Strategy)
	cfg.Configured = cfg.BaseURL != "" && cfg.APIKey != ""
	s.config = cfg
	s.principal = principal
}

func (s *Service) currentPrincipal() *auth.Principal {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.principal
}

func (s *Service) cachedPrincipal(ctx context.Context, cfg AppConfig) (*auth.Principal, error) {
	s.mu.RLock()
	principal := s.principal
	s.mu.RUnlock()
	if principal != nil {
		return principal, nil
	}

	principal, err := s.ensurePrincipal(ctx, cfg)
	if err != nil {
		return nil, err
	}

	s.mu.Lock()
	s.principal = principal
	s.mu.Unlock()
	return principal, nil
}

func (s *Service) defaultProxyBaseURL() string {
	if strings.TrimSpace(s.baseAddr) == "" {
		return "http://127.0.0.1:8080"
	}
	addr := strings.TrimSpace(s.baseAddr)
	if strings.HasPrefix(addr, ":") {
		return "http://127.0.0.1" + addr
	}
	if strings.HasPrefix(addr, "http://") || strings.HasPrefix(addr, "https://") {
		return addr
	}
	return "http://" + addr
}

func (s *Service) recentRequests(ctx context.Context, userID uint) ([]RequestRecord, error) {
	logs, _, err := s.store.ListUsageLogs(ctx, store.UsageLogFilter{
		UserID: userID,
		ListOptions: store.ListOptions{
			Limit:  recentRequestLimit,
			Offset: 0,
		},
	})
	if err != nil {
		return nil, err
	}
	traces, _, err := s.store.ListRouteTraces(ctx, store.RouteTraceFilter{
		UserID: userID,
		ListOptions: store.ListOptions{
			Limit:  recentRequestLimit * 4,
			Offset: 0,
		},
	})
	if err != nil {
		return nil, err
	}

	traceByRequestID := make(map[string]store.RouteTrace, len(traces))
	for _, trace := range traces {
		if strings.TrimSpace(trace.RequestID) == "" {
			continue
		}
		if _, exists := traceByRequestID[trace.RequestID]; exists {
			continue
		}
		traceByRequestID[trace.RequestID] = trace
	}

	items := make([]RequestRecord, 0, len(logs))
	for _, log := range logs {
		trace := traceByRequestID[log.RequestID]
		items = append(items, RequestRecord{
			RequestID:          log.RequestID,
			CreatedAt:          log.CreatedAt.Format("2006-01-02 15:04:05"),
			Endpoint:           log.Endpoint,
			OriginalModel:      firstNonEmpty(trace.OriginalModel, log.Model),
			FinalModel:         firstNonEmpty(trace.FinalModel, log.Model),
			UpstreamName:       log.UpstreamName,
			UpstreamKind:       localUpstreamKind(log.UpstreamName, log.CacheHit),
			StatusCode:         log.StatusCode,
			Success:            log.Success,
			CacheHit:           log.CacheHit,
			CompressionApplied: trace.CompressionApplied,
			SearchApplied:      trace.SearchApplied,
			SavedTokens:        log.SavedTokens,
			TotalTokens:        log.TotalTokens,
			DurationMs:         log.DurationMs,
			DecisionReason:     summarizeDecision(trace.DecisionReason),
			ErrorMessage:       strings.TrimSpace(log.ErrorMessage),
		})
	}
	return items, nil
}

func localUpstreamKind(upstreamName string, cacheHit bool) string {
	if cacheHit {
		return "cache"
	}
	if strings.HasPrefix(strings.TrimSpace(upstreamName), "source:") {
		return "byok"
	}
	if strings.TrimSpace(upstreamName) == "" {
		return "unknown"
	}
	return "managed"
}

func summarizeDecision(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	value = strings.ReplaceAll(value, "\n", " ")
	if len(value) <= 96 {
		return value
	}
	if len(value) <= 180 {
		return value
	}
	return strings.TrimSpace(value[:180]) + "..."
}

func (s *Service) strategyAggressive(strategy string) bool {
	return normalizeStrategy(strategy) == StrategyAggressive
}

func normalizeStrategy(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case StrategyConservative:
		return StrategyConservative
	case StrategyAggressive:
		return StrategyAggressive
	default:
		return StrategyBalanced
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
