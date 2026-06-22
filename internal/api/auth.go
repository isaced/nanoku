package api

import (
	"context"
	"net/http"
)

type ctxKey int

const (
	ctxKeyUser ctxKey = iota
)

// UserFromContext returns the authenticated user attached to the request by
// SessionAuth, or nil if the request is unauthenticated.
func UserFromContext(ctx context.Context) *AuthedUser {
	u, _ := ctx.Value(ctxKeyUser).(*AuthedUser)
	return u
}

func contextWithUser(ctx context.Context, u *AuthedUser) context.Context {
	return context.WithValue(ctx, ctxKeyUser, u)
}

// SessionAuth validates the session cookie on every request and injects
// the AuthedUser into the request context. It does NOT redirect: the API
// contract is JSON, and the SPA frontend reacts to 401 by clearing state.
func SessionAuth(store *SessionStore) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			c, err := r.Cookie(sessionCookieName)
			if err != nil {
				writeErr(w, http.StatusUnauthorized, ErrInvalidCredentials)
				return
			}
			u, err := store.Lookup(r.Context(), c.Value)
			if err != nil {
				// stale cookie: clear it so the client knows to drop it
				clearSessionCookie(w, r)
				writeErr(w, http.StatusUnauthorized, ErrInvalidCredentials)
				return
			}
			au := &AuthedUser{ID: u.ID, Username: u.Username, Role: u.Role}
			next.ServeHTTP(w, r.WithContext(contextWithUser(r.Context(), au)))
		})
	}
}

// CORS keeps the existing cross-origin headers used by the UI and trigger endpoint.
func CORS(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		h.ServeHTTP(w, r)
	})
}

// setSessionCookie writes the session cookie. Secure depends on whether the
// request hit an https connection (r.TLS != nil). For dev on plain http it stays off.
func setSessionCookie(w http.ResponseWriter, r *http.Request, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		Secure:   r.TLS != nil,
		MaxAge:   int(sessionTTL.Seconds()),
	})
}

func clearSessionCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		Secure:   r.TLS != nil,
		MaxAge:   -1,
	})
}