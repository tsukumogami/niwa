package web

import (
	"crypto/subtle"
	"net/http"
	"strings"
)

// BearerMiddleware returns an http middleware that admits only requests
// whose Authorization header carries `Bearer <token>` with a constant-
// time match against the configured token. Mismatched or missing headers
// receive 401 with a `WWW-Authenticate: Bearer` challenge.
//
// Token-from-cookie and token-from-query are explicitly rejected: only
// the Authorization request header is consulted. This prevents two
// classes of attack: third-party iframe abuse (cookies are sent
// automatically by the browser, the Authorization header is not) and
// referrer-leak of a query-string token to upstream proxies/log
// aggregators.
//
// F5 wires this middleware around a route group that contains zero
// routes. The contract is locked here so F10's mutation API composes on
// it without inventing a new auth surface. The tests for #6 confirm
// 401/200 against a stubbed route to keep the middleware's behaviour
// regression-tested even when no real route exercises it.
func BearerMiddleware(token string) func(http.Handler) http.Handler {
	tokenBytes := []byte(token)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Token sources we reject by design: cookies and query
			// parameters. If a caller stuffed a "token" cookie or query
			// param expecting it to authenticate, that should not work.
			// We do not affirmatively scan for these — we simply ignore
			// them. The auth surface is the Authorization header only.
			h := r.Header.Get("Authorization")
			if !strings.HasPrefix(h, "Bearer ") {
				w.Header().Set("WWW-Authenticate", `Bearer realm="niwa-surface"`)
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			provided := []byte(strings.TrimPrefix(h, "Bearer "))
			// subtle.ConstantTimeCompare returns 0 on length mismatch.
			if subtle.ConstantTimeCompare(provided, tokenBytes) != 1 {
				w.Header().Set("WWW-Authenticate", `Bearer realm="niwa-surface"`)
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
