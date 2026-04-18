package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"agent-gateway/internal/billing"
	"agent-gateway/internal/cache"
	"agent-gateway/internal/config"
	"agent-gateway/internal/gateway"
	"agent-gateway/internal/handler"
	"agent-gateway/internal/intelligent"
	"agent-gateway/internal/localapp"
	"agent-gateway/internal/middleware"
	"agent-gateway/internal/observe"
	"agent-gateway/internal/store"
)

const localBrowserOpenedKey = "local.browser_opened"

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	ctx := context.Background()
	st, err := store.Open(ctx, cfg.Database.Path)
	if err != nil {
		log.Fatalf("open store: %v", err)
	}
	defer st.Close()

	if err := st.SeedIfEmpty(ctx, cfg); err != nil {
		log.Fatalf("seed store: %v", err)
	}

	registry := gateway.NewRegistry()
	var localService *localapp.Service
	if cfg.Local.Enabled {
		cfg.Billing.Enabled = false
		localService = localapp.NewService(st, cfg)
		if err := localService.Bootstrap(ctx); err != nil {
			log.Fatalf("bootstrap local mode: %v", err)
		}
	}
	if err := registry.Load(ctx, st); err != nil {
		log.Fatalf("load registry: %v", err)
	}

	client := &http.Client{
		Timeout: time.Duration(cfg.Server.UpstreamTimeout),
	}

	proxy := gateway.NewProxy(registry, client, st)
	billingService := billing.NewService(cfg.Billing, st)
	cacheService := cache.NewExactCache(cfg.Cache, st)
	if embedder := cache.NewSemanticEmbedder(cfg.Cache.SemanticEmbedding); embedder != nil {
		cacheService.SetSemanticEmbedder(embedder)
	}
	traceService := observe.NewRouteTraceService(st)
	var searcher intelligent.WebSearcher
	if cfg.Intelligent.WebSearch.Enabled && cfg.Intelligent.WebSearch.Provider == "tavily" {
		searcher = intelligent.NewTavilyClient(cfg.Intelligent.WebSearch)
	}
	preprocessor := intelligent.NewPreprocessor(cfg.Intelligent, searcher)
	if localService != nil {
		preprocessor.SetRuntimeSettingsProvider(localService)
		cacheService.SetRuntimeSettingsProvider(localService)
	}

	chatHandler := handler.NewChatCompletionsHandler(proxy, preprocessor, billingService, traceService, cacheService)
	embeddingsHandler := handler.NewEmbeddingsHandler(proxy, billingService)
	modelsHandler := handler.NewModelsHandler(proxy, st)
	adminHandler := handler.NewAdminHandler(st, registry, cfg)
	consoleAuthHandler := handler.NewConsoleAuthHandler(st, &cfg.Auth)
	publicContactHandler := handler.NewPublicContactHandler(st)

	rateLimitMiddleware := middleware.NewRateLimitMiddleware(cfg.RateLimit)
	apiKeyAuthMiddleware := middleware.NewAuthMiddleware(st, cfg.Billing)
	consoleAuthMiddleware := middleware.NewConsoleAuthMiddleware(st, cfg.Auth)
	var localAuthMiddleware *middleware.LocalAuthMiddleware
	if localService != nil {
		localAuthMiddleware = middleware.NewLocalAuthMiddleware(localService)
	}

	protectedV1 := func(next http.Handler) http.Handler {
		if localAuthMiddleware != nil {
			return localAuthMiddleware.Wrap(rateLimitMiddleware.Wrap(next))
		}
		return apiKeyAuthMiddleware.Wrap(rateLimitMiddleware.Wrap(next))
	}
	protectedConsole := func(next http.Handler) http.Handler {
		return consoleAuthMiddleware.Wrap(rateLimitMiddleware.Wrap(next))
	}

	mux := http.NewServeMux()
	mux.Handle("GET /healthz", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}))

	if localService != nil {
		localAppHandler := handler.NewLocalAppHandler(localService, func(ctx context.Context) error {
			return registry.Load(ctx, st)
		})
		mux.Handle("GET /", http.HandlerFunc(localAppHandler.Page))
		mux.Handle("GET /local/api/config", http.HandlerFunc(localAppHandler.GetConfig))
		mux.Handle("POST /local/api/config", http.HandlerFunc(localAppHandler.SaveConfig))
	}

	mux.Handle("POST /v1/chat/completions", protectedV1(chatHandler))
	mux.Handle("POST /chat/completions", protectedV1(chatHandler))
	mux.Handle("POST /v1/embeddings", protectedV1(embeddingsHandler))
	mux.Handle("POST /embeddings", protectedV1(embeddingsHandler))
	mux.Handle("GET /v1/models", protectedV1(modelsHandler))
	mux.Handle("GET /models", protectedV1(modelsHandler))
	mux.Handle("POST /public/contact-requests", http.HandlerFunc(publicContactHandler.Create))
	mux.Handle("GET /public/site-settings", http.HandlerFunc(adminHandler.PublicSiteSettings))

	mux.Handle("POST /console/auth/register", http.HandlerFunc(consoleAuthHandler.Register))
	mux.Handle("POST /console/auth/login", http.HandlerFunc(consoleAuthHandler.Login))
	mux.Handle("POST /console/auth/email/request-code", http.HandlerFunc(consoleAuthHandler.RequestEmailCode))
	mux.Handle("POST /console/auth/email/verify-code", http.HandlerFunc(consoleAuthHandler.VerifyEmailCode))
	mux.Handle("GET /console/auth/me", protectedConsole(http.HandlerFunc(consoleAuthHandler.Me)))
	mux.Handle("POST /console/auth/logout", http.HandlerFunc(consoleAuthHandler.Logout))
	mux.Handle("GET /console/auth/github/start", http.HandlerFunc(consoleAuthHandler.GitHubStart))
	mux.Handle("GET /console/auth/github/callback", http.HandlerFunc(consoleAuthHandler.GitHubCallback))

	mux.Handle("GET /admin/users", protectedConsole(http.HandlerFunc(adminHandler.ListUsers)))
	mux.Handle("PATCH /admin/users/{userID}", protectedConsole(http.HandlerFunc(adminHandler.UpdateUser)))
	mux.Handle("GET /admin/projects", protectedConsole(http.HandlerFunc(adminHandler.ListProjects)))
	mux.Handle("POST /admin/projects", protectedConsole(http.HandlerFunc(adminHandler.CreateProject)))
	mux.Handle("GET /admin/projects/{projectID}", protectedConsole(http.HandlerFunc(adminHandler.GetProjectDetail)))
	mux.Handle("PATCH /admin/projects/{projectID}", protectedConsole(http.HandlerFunc(adminHandler.UpdateProjectSettings)))
	mux.Handle("GET /admin/models", protectedConsole(http.HandlerFunc(adminHandler.ListManagedModels)))
	mux.Handle("POST /admin/models", protectedConsole(http.HandlerFunc(adminHandler.CreateManagedModel)))
	mux.Handle("PATCH /admin/models/{modelID}", protectedConsole(http.HandlerFunc(adminHandler.UpdateManagedModel)))
	mux.Handle("PATCH /admin/models/{modelID}/status", protectedConsole(http.HandlerFunc(adminHandler.UpdateManagedModelStatus)))
	mux.Handle("DELETE /admin/models/{modelID}", protectedConsole(http.HandlerFunc(adminHandler.DeleteManagedModel)))
	mux.Handle("GET /admin/sources", protectedConsole(http.HandlerFunc(adminHandler.ListSources)))
	mux.Handle("POST /admin/sources", protectedConsole(http.HandlerFunc(adminHandler.CreateSource)))
	mux.Handle("GET /admin/api-keys", protectedConsole(http.HandlerFunc(adminHandler.ListAPIKeys)))
	mux.Handle("POST /admin/api-keys", protectedConsole(http.HandlerFunc(adminHandler.CreateAPIKey)))
	mux.Handle("GET /admin/usage-logs", protectedConsole(http.HandlerFunc(adminHandler.ListUsageLogs)))
	mux.Handle("GET /admin/route-traces", protectedConsole(http.HandlerFunc(adminHandler.ListRouteTraces)))
	mux.Handle("GET /admin/request-observations", protectedConsole(http.HandlerFunc(adminHandler.ListRequestObservations)))
	mux.Handle("GET /admin/cost-analysis", protectedConsole(http.HandlerFunc(adminHandler.CostAnalysis)))
	mux.Handle("GET /admin/overview", protectedConsole(http.HandlerFunc(adminHandler.Overview)))
	mux.Handle("GET /admin/system", protectedConsole(http.HandlerFunc(adminHandler.SystemSummary)))
	mux.Handle("GET /admin/settings", protectedConsole(http.HandlerFunc(adminHandler.GetSettings)))
	mux.Handle("PATCH /admin/settings", protectedConsole(http.HandlerFunc(adminHandler.UpdateSettings)))
	mux.Handle("PATCH /admin/site-settings", protectedConsole(http.HandlerFunc(adminHandler.UpdateSiteSettings)))
	mux.Handle("GET /admin/contact-requests", protectedConsole(http.HandlerFunc(adminHandler.ListContactRequests)))
	mux.Handle("PATCH /admin/contact-requests/{requestID}", protectedConsole(http.HandlerFunc(adminHandler.UpdateContactRequestStatus)))
	mux.Handle("GET /admin/contact-requests/{requestID}", protectedConsole(http.HandlerFunc(adminHandler.GetContactRequestDetail)))
	mux.Handle("POST /admin/contact-requests/{requestID}/notes", protectedConsole(http.HandlerFunc(adminHandler.AddContactRequestNote)))
	mux.Handle("PATCH /admin/contact-requests/{requestID}/owner", protectedConsole(http.HandlerFunc(adminHandler.UpdateContactRequestOwner)))
	mux.Handle("PATCH /admin/contact-notes/{noteID}", protectedConsole(http.HandlerFunc(adminHandler.UpdateContactNote)))
	mux.Handle("DELETE /admin/contact-notes/{noteID}", protectedConsole(http.HandlerFunc(adminHandler.DeleteContactNote)))

	server := &http.Server{
		Addr:              cfg.Server.ListenAddr,
		Handler:           loggingMiddleware(mux),
		ReadHeaderTimeout: 10 * time.Second,
	}

	log.Printf("agent gateway listening on %s", cfg.Server.ListenAddr)
	if cfg.Local.Enabled && cfg.Local.AutoOpenBrowser {
		autoOpenLocalUI(ctx, st, cfg)
	}
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("listen server: %v", err)
	}
}

func autoOpenLocalUI(ctx context.Context, st *store.SQLiteStore, cfg *config.Config) {
	settings, err := st.GetSiteSettings(ctx)
	if err != nil {
		log.Printf("load site settings for local browser auto-open: %v", err)
		return
	}
	if strings.EqualFold(strings.TrimSpace(settings[localBrowserOpenedKey]), "true") {
		return
	}

	localURL := localModeBaseURL(cfg.Server.ListenAddr)
	go func() {
		if err := waitForLocalServer(localURL); err != nil {
			log.Printf("wait for local server before opening browser: %v", err)
		}
		if err := openBrowser(localURL); err != nil {
			log.Printf("open local browser: %v", err)
			return
		}
		if err := st.UpsertSiteSettings(context.Background(), map[string]string{
			localBrowserOpenedKey: "true",
		}); err != nil {
			log.Printf("mark local browser opened: %v", err)
		}
	}()
}

func waitForLocalServer(baseURL string) error {
	client := &http.Client{Timeout: 500 * time.Millisecond}
	healthURL := strings.TrimRight(baseURL, "/") + "/healthz"

	var lastErr error
	for attempt := 0; attempt < 20; attempt++ {
		resp, err := client.Get(healthURL)
		if err == nil {
			_ = resp.Body.Close()
			return nil
		}
		lastErr = err
		time.Sleep(250 * time.Millisecond)
	}
	return lastErr
}

func localModeBaseURL(listenAddr string) string {
	addr := strings.TrimSpace(listenAddr)
	if addr == "" {
		return "http://127.0.0.1:8080"
	}
	if strings.HasPrefix(addr, "http://") || strings.HasPrefix(addr, "https://") {
		return addr
	}
	if strings.HasPrefix(addr, ":") {
		return "http://127.0.0.1" + addr
	}

	host, port, err := net.SplitHostPort(addr)
	if err == nil {
		host = strings.TrimSpace(host)
		switch host {
		case "", "0.0.0.0", "::":
			host = "127.0.0.1"
		}
		return fmt.Sprintf("http://%s:%s", host, port)
	}

	return "http://" + addr
}

func openBrowser(target string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", target)
	case "darwin":
		cmd = exec.Command("open", target)
	default:
		cmd = exec.Command("xdg-open", target)
	}
	return cmd.Start()
}

func loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		log.Printf("%s %s %s", r.Method, r.URL.Path, time.Since(start))
	})
}
