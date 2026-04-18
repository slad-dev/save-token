package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"agent-gateway/internal/auth"
	"agent-gateway/internal/billing"
	"agent-gateway/internal/cache"
	"agent-gateway/internal/config"
	"agent-gateway/internal/gateway"
	"agent-gateway/internal/intelligent"
	"agent-gateway/internal/observe"
	"agent-gateway/internal/store"
)

type recordingSearcher struct {
	calls int32
}

func (s *recordingSearcher) Search(ctx context.Context, query string) ([]intelligent.SearchResult, error) {
	atomic.AddInt32(&s.calls, 1)
	return []intelligent.SearchResult{
		{
			Title:   "mock result",
			URL:     "https://example.invalid/result",
			Content: "mock content",
		},
	}, nil
}

func TestChatCompletionsStrictPrivacyBypassesSearchAndCache(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "gateway.db")

	var upstreamCalls int32
	upstreamBody := []byte(`{"id":"chatcmpl-mock","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"upstream-response"},"finish_reason":"stop"}],"usage":{"prompt_tokens":11,"completion_tokens":7,"total_tokens":18}}`)
	upstreamServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&upstreamCalls, 1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(upstreamBody)
	}))
	defer upstreamServer.Close()

	cfg := config.Config{
		Server: config.ServerConfig{
			ListenAddr:      ":0",
			UpstreamTimeout: config.Duration(5 * time.Second),
		},
		Database: config.DatabaseConfig{
			Path: dbPath,
		},
		Intelligent: config.IntelligentConfig{
			WebSearch: config.WebSearchConfig{
				Enabled:    true,
				Provider:   "tavily",
				APIKey:     "test-key",
				BaseURL:    "https://api.tavily.com",
				MaxResults: 5,
				Timeout:    config.Duration(5 * time.Second),
			},
			RAG: config.RAGConfig{
				Enabled:                   false,
				MaxInputCharacters:        128000,
				MaxEstimatedTokens:        32000,
				CompressionThresholdChars: 50000,
				KeepRecentMessages:        8,
				SummaryMaxChars:           2000,
			},
			Routing: config.RoutingConfig{
				Enabled: false,
			},
		},
		Billing: config.BillingConfig{
			Enabled:               true,
			DefaultChatPer1K:      100,
			DefaultEmbeddingPer1K: 20,
			MinimumRequestCharge:  1,
			WebSearchSurcharge:    5,
		},
		Cache: config.CacheConfig{
			Enabled:               true,
			ExactChatTTL:          config.Duration(10 * time.Minute),
			MaxBodyBytes:          1 << 20,
			SemanticEnabled:       true,
			SemanticSimilarity:    0.82,
			SemanticMaxCandidates: 25,
		},
		RateLimit: config.RateLimitConfig{
			Enabled:           false,
			RequestsPerMinute: 60,
			Burst:             20,
		},
		Bootstrap: config.BootstrapConfig{
			Upstreams: []config.UpstreamSeed{
				{
					Name:            "mock-upstream",
					BaseURL:         upstreamServer.URL,
					APIKey:          "upstream-key",
					Weight:          1,
					Enabled:         boolPtr(true),
					SupportedModels: []string{"deepseek-ai/DeepSeek-V3"},
				},
			},
			ModelRoutes: []config.ModelRouteSeed{
				{
					ModelPattern: "*",
					Strategy:     "round_robin",
					Upstreams:    []string{"mock-upstream"},
				},
			},
			Users: []config.UserSeed{
				{
					Name:     "demo-user",
					Balance:  100000,
					Plan:     "pro",
					IsActive: boolPtr(true),
				},
			},
			APIKeys: []config.APIKeySeed{
				{
					Name:           "demo-key",
					Key:            "sk-demo-local-key",
					UserName:       "demo-user",
					AllowWebSearch: boolPtr(true),
					IsActive:       boolPtr(true),
				},
			},
		},
	}

	st, err := store.Open(ctx, cfg.Database.Path)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	if err := st.SeedIfEmpty(ctx, &cfg); err != nil {
		t.Fatalf("seed store: %v", err)
	}

	registry := gateway.NewRegistry()
	if err := registry.Load(ctx, st); err != nil {
		t.Fatalf("load registry: %v", err)
	}

	proxy := gateway.NewProxy(registry, upstreamServer.Client(), st)
	billingService := billing.NewService(cfg.Billing, st)
	traceService := observe.NewRouteTraceService(st)
	cacheService := cache.NewExactCache(cfg.Cache, st)
	searcher := &recordingSearcher{}
	preprocessor := intelligent.NewPreprocessor(cfg.Intelligent, searcher)
	chatHandler := NewChatCompletionsHandler(proxy, preprocessor, billingService, traceService, cacheService)
	adminHandler := NewAdminHandler(st, registry, &cfg)

	adminPrincipal := mustFindPrincipal(t, st, ctx, "sk-demo-local-key")

	createProjectReq := httptest.NewRequest(http.MethodPost, "/admin/projects", bytes.NewBufferString(`{"name":"Strict Project","description":"private","mode":"hybrid","privacy_mode":"strict","web_search_enabled":true}`))
	createProjectReq = createProjectReq.WithContext(auth.WithPrincipal(createProjectReq.Context(), adminPrincipal))
	createProjectRec := httptest.NewRecorder()
	adminHandler.CreateProject(createProjectRec, createProjectReq)
	if createProjectRec.Code != http.StatusCreated {
		t.Fatalf("create project status = %d, body = %s", createProjectRec.Code, createProjectRec.Body.String())
	}

	var createdProject struct {
		ID               uint   `json:"id"`
		PrivacyMode      string `json:"privacy_mode"`
		WebSearchEnabled bool   `json:"web_search_enabled"`
	}
	if err := json.Unmarshal(createProjectRec.Body.Bytes(), &createdProject); err != nil {
		t.Fatalf("decode project response: %v", err)
	}
	if createdProject.PrivacyMode != store.PrivacyModeStrict {
		t.Fatalf("expected strict privacy mode, got %q", createdProject.PrivacyMode)
	}
	if createdProject.WebSearchEnabled {
		t.Fatalf("expected strict project to disable web search, body = %s", createProjectRec.Body.String())
	}

	createKeyReq := httptest.NewRequest(http.MethodPost, "/admin/api-keys", bytes.NewBufferString(fmt.Sprintf(`{"project_id":%d,"name":"Strict API Key","allow_web_search":true}`, createdProject.ID)))
	createKeyReq = createKeyReq.WithContext(auth.WithPrincipal(createKeyReq.Context(), adminPrincipal))
	createKeyRec := httptest.NewRecorder()
	adminHandler.CreateAPIKey(createKeyRec, createKeyReq)
	if createKeyRec.Code != http.StatusCreated {
		t.Fatalf("create api key status = %d, body = %s", createKeyRec.Code, createKeyRec.Body.String())
	}

	var createdKey struct {
		Key string `json:"key"`
	}
	if err := json.Unmarshal(createKeyRec.Body.Bytes(), &createdKey); err != nil {
		t.Fatalf("decode api key response: %v", err)
	}
	if createdKey.Key == "" {
		t.Fatalf("expected api key value to be returned")
	}

	principal := mustFindPrincipal(t, st, ctx, createdKey.Key)
	requestContext := auth.WithPrincipal(ctx, principal)

	chatBody := []byte(`{"model":"deepseek-ai/DeepSeek-V3","messages":[{"role":"user","content":"请帮我搜索一下今天北京天气怎么样"}]}`)
	prepareResult, err := preprocessor.PrepareChatRequest(requestContext, chatBody)
	if err != nil {
		t.Fatalf("prepare chat request: %v", err)
	}
	if searcher.calls != 0 {
		t.Fatalf("expected strict privacy to skip search, searcher calls = %d", searcher.calls)
	}
	if !strings.Contains(strings.ToLower(prepareResult.DecisionReason), "strict_privacy_mode") {
		t.Fatalf("expected decision reason to mention strict privacy, got %q", prepareResult.DecisionReason)
	}
	if prepareResult.UsedTooling {
		t.Fatalf("expected strict privacy to disable tooling")
	}

	requestHash, err := cacheService.BuildRequestHash(prepareResult.Body)
	if err != nil {
		t.Fatalf("build request hash: %v", err)
	}
	cachedBody := []byte(`{"id":"chatcmpl-cached","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"cached-response"},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":4,"total_tokens":7}}`)
	if err := st.UpsertCacheEntry(ctx, store.CacheEntry{
		UserID:       principal.User.ID,
		Endpoint:     "/v1/chat/completions",
		RequestHash:  requestHash,
		Model:        prepareResult.Model,
		QueryText:    prepareResult.QueryText,
		StatusCode:   http.StatusOK,
		ContentType:  "application/json",
		ResponseBody: append([]byte(nil), cachedBody...),
		ExpiresAt:    time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatalf("seed cache entry: %v", err)
	}

	chatReq := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(chatBody))
	chatReq = chatReq.WithContext(requestContext)
	chatRec := httptest.NewRecorder()
	chatHandler.ServeHTTP(chatRec, chatReq)
	if chatRec.Code != http.StatusOK {
		t.Fatalf("chat status = %d, body = %s", chatRec.Code, chatRec.Body.String())
	}
	if body := chatRec.Body.Bytes(); !bytes.Equal(body, upstreamBody) {
		t.Fatalf("expected upstream response body, got %s", string(body))
	}
	if got := atomic.LoadInt32(&upstreamCalls); got != 1 {
		t.Fatalf("expected one upstream call, got %d", got)
	}

	cacheEntry, err := st.FindValidCacheEntry(ctx, principal.User.ID, "/v1/chat/completions", requestHash)
	if err != nil {
		t.Fatalf("find cache entry: %v", err)
	}
	if cacheEntry == nil {
		t.Fatalf("expected cache entry to remain present")
	}
	if !bytes.Equal(cacheEntry.ResponseBody, cachedBody) {
		t.Fatalf("expected cache entry to remain unchanged, got %s", string(cacheEntry.ResponseBody))
	}

	traces, total, err := st.ListRouteTraces(ctx, store.RouteTraceFilter{
		ListOptions: store.ListOptions{Limit: 5},
		UserID:      principal.User.ID,
		Endpoint:    "/v1/chat/completions",
	})
	if err != nil {
		t.Fatalf("list route traces: %v", err)
	}
	if total == 0 || len(traces) == 0 {
		t.Fatalf("expected route trace to be recorded")
	}
	trace := traces[0]
	if trace.SearchApplied {
		t.Fatalf("expected search_applied=false in route trace")
	}
	if trace.CacheHit {
		t.Fatalf("expected cache_hit=false in route trace")
	}
	if !strings.Contains(strings.ToLower(trace.DecisionReason), "strict_privacy_mode") {
		t.Fatalf("expected route trace to record strict privacy mode, got %q", trace.DecisionReason)
	}

	logs, logTotal, err := st.ListUsageLogs(ctx, store.UsageLogFilter{
		ListOptions: store.ListOptions{Limit: 5},
		UserID:      principal.User.ID,
		Endpoint:    "/v1/chat/completions",
	})
	if err != nil {
		t.Fatalf("list usage logs: %v", err)
	}
	if logTotal == 0 || len(logs) == 0 {
		t.Fatalf("expected usage log to be recorded")
	}
	log := logs[0]
	if log.CacheHit {
		t.Fatalf("expected usage log cache_hit=false")
	}
	if log.ToolUsed {
		t.Fatalf("expected usage log tool_used=false")
	}
}

func mustFindPrincipal(t *testing.T, st *store.SQLiteStore, ctx context.Context, key string) *auth.Principal {
	t.Helper()

	apiKey, err := st.FindAPIKeyWithUser(ctx, key)
	if err != nil {
		t.Fatalf("find api key %q: %v", key, err)
	}

	return &auth.Principal{
		User:   apiKey.User,
		APIKey: *apiKey,
	}
}

func boolPtr(value bool) *bool {
	return &value
}
