package api

import (
	"encoding/json"
	"errors"
	"net"
	"net/http"

	sessionpkg "github.com/isaced/nanoku/internal/db/session"
	userpkg "github.com/isaced/nanoku/internal/db/user"
)

// LoginRequest is the body of POST /api/login.
type LoginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// LoginResponse is the success payload for POST /api/login.
type LoginResponse struct {
	User     string `json:"user"`
	Role     string `json:"role"`
	Username string `json:"username"`
}

func (h *Handlers) Login(w http.ResponseWriter, r *http.Request) {
	ip := clientIP(r)
	if !h.Sessions.AllowLogin(ip) {
		writeErr(w, http.StatusTooManyRequests, errors.New("too many login attempts, slow down"))
		return
	}

	var in LoginRequest
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeErr(w, http.StatusBadRequest, errors.New("invalid json body"))
		return
	}
	if in.Username == "" || in.Password == "" {
		writeErr(w, http.StatusBadRequest, errors.New("username and password are required"))
		return
	}

	u, err := Authenticate(r.Context(), h.DB, in.Username, in.Password)
	if err != nil {
		writeErr(w, http.StatusUnauthorized, ErrInvalidCredentials)
		return
	}

	token, err := h.Sessions.Create(r.Context(), u)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, errors.New("failed to create session"))
		return
	}
	setSessionCookie(w, r, token)
	writeJSON(w, http.StatusOK, LoginResponse{
		User:     u.Username,
		Username: u.Username,
		Role:     u.Role,
	})
}

func (h *Handlers) Logout(w http.ResponseWriter, r *http.Request) {
	c, err := r.Cookie(sessionCookieName)
	if err == nil {
		_ = h.Sessions.Delete(r.Context(), c.Value)
	}
	clearSessionCookie(w, r)
	w.WriteHeader(http.StatusNoContent)
}

// Me reports the currently authenticated user. Always 200 with a user payload,
// or 401 if not authenticated (the SPA uses this as the heartbeat check).
func (h *Handlers) Me(w http.ResponseWriter, r *http.Request) {
	u := UserFromContext(r.Context())
	if u == nil {
		writeErr(w, http.StatusUnauthorized, ErrInvalidCredentials)
		return
	}
	writeJSON(w, http.StatusOK, LoginResponse{
		User:     u.Username,
		Username: u.Username,
		Role:     u.Role,
	})
}

// ChangePassword lets the authenticated user rotate their own password.
func (h *Handlers) ChangePassword(w http.ResponseWriter, r *http.Request) {
	u := UserFromContext(r.Context())
	if u == nil {
		writeErr(w, http.StatusUnauthorized, ErrInvalidCredentials)
		return
	}
	var in struct {
		OldPassword string `json:"oldPassword"`
		NewPassword string `json:"newPassword"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeErr(w, http.StatusBadRequest, errors.New("invalid json body"))
		return
	}
	if len(in.NewPassword) < 8 {
		writeErr(w, http.StatusBadRequest, errors.New("new password must be at least 8 characters"))
		return
	}
	row, err := h.DB.User.Get(r.Context(), u.ID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	if _, err := Authenticate(r.Context(), h.DB, row.Username, in.OldPassword); err != nil {
		writeErr(w, http.StatusUnauthorized, errors.New("old password is incorrect"))
		return
	}
	hash, err := HashPassword(in.NewPassword)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	if _, err := h.DB.User.UpdateOneID(u.ID).SetPasswordHash(hash).Save(r.Context()); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	// drop all other sessions for this user (force re-login on other devices)
	if _, err := h.DB.Session.Delete().Where(sessionpkg.HasUserWith(userpkg.IDEQ(u.ID))).Exec(r.Context()); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// clientIP returns the best-effort client IP. Honors X-Forwarded-For first
// hop when present (nanoku typically sits behind Caddy); falls back to
// RemoteAddr host.
func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		for i := 0; i < len(xff); i++ {
			if xff[i] == ',' {
				return trimSpace(xff[:i])
			}
		}
		return trimSpace(xff)
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func trimSpace(s string) string {
	start, end := 0, len(s)
	for start < end && (s[start] == ' ' || s[start] == '\t') {
		start++
	}
	for end > start && (s[end-1] == ' ' || s[end-1] == '\t') {
		end--
	}
	return s[start:end]
}