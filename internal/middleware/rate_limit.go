package middleware

import (
	"net/http"
	"strconv"
	"sync"
	"time"

	"agent-gateway/internal/auth"
	"agent-gateway/internal/config"
	"agent-gateway/internal/gateway"

	"golang.org/x/time/rate"
)

type RateLimitMiddleware struct {
	cfg      config.RateLimitConfig
	limiters sync.Map
}

func NewRateLimitMiddleware(cfg config.RateLimitConfig) *RateLimitMiddleware {
	return &RateLimitMiddleware{cfg: cfg}
}

func (m *RateLimitMiddleware) Wrap(next http.Handler) http.Handler {
	if !m.cfg.Enabled {
		return next
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		principal, ok := auth.PrincipalFromContext(r.Context())
		if !ok {
			gateway.WriteJSONError(w, http.StatusUnauthorized, "invalid_api_key", "authentication required before rate limiting")
			return
		}

		limiter := m.getLimiter(strconv.FormatUint(uint64(principal.APIKey.ID), 10))
		if !limiter.Allow() {
			w.Header().Set("Retry-After", "60")
			gateway.WriteJSONError(w, http.StatusTooManyRequests, "rate_limit_exceeded", "rate limit exceeded for this api key")
			return
		}

		next.ServeHTTP(w, r)
	})
}

func (m *RateLimitMiddleware) getLimiter(key string) *rate.Limiter {
	value, _ := m.limiters.LoadOrStore(key, rate.NewLimiter(rate.Every(time.Minute/time.Duration(m.cfg.RequestsPerMinute)), m.cfg.Burst))
	return value.(*rate.Limiter)
}
