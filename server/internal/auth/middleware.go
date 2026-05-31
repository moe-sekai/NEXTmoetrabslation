package auth

import (
	"context"
	"net/http"
	"strings"
)

type ctxKey int

const claimsKey ctxKey = 0

// FromContext returns the authenticated claims attached by RequireAuth.
func FromContext(ctx context.Context) (*Claims, bool) {
	c, ok := ctx.Value(claimsKey).(*Claims)
	return c, ok
}

// bearerToken extracts a token from the Authorization header, or—for SSE, where
// EventSource cannot set headers—from the "token" query parameter.
func bearerToken(r *http.Request) string {
	if h := r.Header.Get("Authorization"); h != "" {
		return strings.TrimPrefix(h, "Bearer ")
	}
	return r.URL.Query().Get("token")
}

// RequireAuth wraps a handler, rejecting requests without a valid JWT and
// attaching the claims to the request context.
func (a *Auth) RequireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		claims, err := a.VerifyToken(bearerToken(r))
		if err != nil {
			writeJSONError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		ctx := context.WithValue(r.Context(), claimsKey, claims)
		next(w, r.WithContext(ctx))
	}
}

// RequireAdmin wraps a handler, additionally requiring the admin role.
func (a *Auth) RequireAdmin(next http.HandlerFunc) http.HandlerFunc {
	return a.RequireAuth(func(w http.ResponseWriter, r *http.Request) {
		claims, _ := FromContext(r.Context())
		if claims == nil || claims.Role != RoleAdmin {
			writeJSONError(w, http.StatusForbidden, "admin role required")
			return
		}
		next(w, r)
	})
}

func writeJSONError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	// msg is a fixed internal string, safe to inline.
	w.Write([]byte(`{"error":"` + msg + `"}`))
}
