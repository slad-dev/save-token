package middleware

import (
	"net/http"

	"agent-gateway/internal/auth"
	"agent-gateway/internal/gateway"
	"agent-gateway/internal/localapp"
)

type LocalAuthMiddleware struct {
	service *localapp.Service
}

func NewLocalAuthMiddleware(service *localapp.Service) *LocalAuthMiddleware {
	return &LocalAuthMiddleware{service: service}
}

func (m *LocalAuthMiddleware) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		principal, err := m.service.Principal(r.Context())
		if err != nil {
			gateway.WriteJSONError(w, http.StatusInternalServerError, "local_mode_error", err.Error())
			return
		}
		next.ServeHTTP(w, r.WithContext(auth.WithPrincipal(r.Context(), principal)))
	})
}
