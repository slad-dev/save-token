package middleware

import (
	"errors"
	"net/http"
	"strings"

	"agent-gateway/internal/auth"
	"agent-gateway/internal/config"
	"agent-gateway/internal/gateway"
	"agent-gateway/internal/store"

	"gorm.io/gorm"
)

type AuthMiddleware struct {
	store   *store.SQLiteStore
	billing config.BillingConfig
}

func NewAuthMiddleware(store *store.SQLiteStore, billing config.BillingConfig) *AuthMiddleware {
	return &AuthMiddleware{
		store:   store,
		billing: billing,
	}
}

func (m *AuthMiddleware) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := extractBearerToken(r.Header.Get("Authorization"))
		if token == "" || !strings.HasPrefix(token, "sk-") {
			gateway.WriteJSONError(w, http.StatusUnauthorized, "invalid_api_key", "missing or invalid Authorization bearer token")
			return
		}

		apiKey, err := m.store.FindAPIKeyWithUser(r.Context(), token)
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				gateway.WriteJSONError(w, http.StatusUnauthorized, "invalid_api_key", "api key not found")
				return
			}
			gateway.WriteJSONError(w, http.StatusInternalServerError, "server_error", "failed to validate api key")
			return
		}

		if !apiKey.IsActive || !apiKey.User.IsActive {
			gateway.WriteJSONError(w, http.StatusUnauthorized, "invalid_api_key", "api key or user is inactive")
			return
		}

		if apiKey.User.Balance < m.billing.MinimumRequestCharge {
			gateway.WriteJSONError(w, http.StatusPaymentRequired, "insufficient_balance", "user balance is insufficient")
			return
		}

		principal := &auth.Principal{
			User:   apiKey.User,
			APIKey: *apiKey,
		}
		next.ServeHTTP(w, r.WithContext(auth.WithPrincipal(r.Context(), principal)))
	})
}

func extractBearerToken(header string) string {
	header = strings.TrimSpace(header)
	if header == "" {
		return ""
	}
	parts := strings.SplitN(header, " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return ""
	}
	return strings.TrimSpace(parts[1])
}
