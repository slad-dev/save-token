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

type ConsoleAuthMiddleware struct {
	store      *store.SQLiteStore
	cookieName string
}

func NewConsoleAuthMiddleware(st *store.SQLiteStore, cfg config.AuthConfig) *ConsoleAuthMiddleware {
	cookieName := strings.TrimSpace(cfg.CookieName)
	if cookieName == "" {
		cookieName = "ag_console_session"
	}
	return &ConsoleAuthMiddleware{store: st, cookieName: cookieName}
}

func (m *ConsoleAuthMiddleware) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(m.cookieName)
		if err != nil || strings.TrimSpace(cookie.Value) == "" {
			gateway.WriteJSONError(w, http.StatusUnauthorized, "unauthorized", "login required")
			return
		}

		session, err := m.store.FindConsoleSessionByToken(r.Context(), cookie.Value)
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				gateway.WriteJSONError(w, http.StatusUnauthorized, "unauthorized", "session expired or invalid")
				return
			}
			gateway.WriteJSONError(w, http.StatusInternalServerError, "server_error", "failed to validate session")
			return
		}

		if !session.User.IsActive {
			gateway.WriteJSONError(w, http.StatusUnauthorized, "unauthorized", "user is inactive")
			return
		}

		_ = m.store.TouchConsoleSession(r.Context(), session.ID)

		principal := &auth.Principal{User: session.User}
		next.ServeHTTP(w, r.WithContext(auth.WithPrincipal(r.Context(), principal)))
	})
}
