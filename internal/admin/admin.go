package admin

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"sync"
	"time"

	"mailservice/internal/auth"
	"mailservice/internal/store"
)

const (
	cookieName      = "ms_admin_session"
	sessionTTL      = 12 * time.Hour
	maxLoginFails   = 5
	loginFailWindow = 15 * time.Minute
	MinPasswordLen  = 12
)

// Store is the subset of the store the admin API needs.
type Store interface {
	CreateKey(ctx context.Context, in store.KeyInput, createdIP string) (*store.APIKey, string, error)
	ListKeys(ctx context.Context) ([]*store.APIKey, error)
	UpdateKey(ctx context.Context, id int64, in store.KeyInput) (*store.APIKey, error)
	SetKeyActive(ctx context.Context, id int64, active bool) error
	DeleteKey(ctx context.Context, id int64) error
	RevealKey(ctx context.Context, id int64) (string, error)
	ListLogs(ctx context.Context, keyID int64, limit int) ([]store.RequestLog, error)
	ListKeyRequests(ctx context.Context) ([]*store.KeyRequest, error)
	ApproveKeyRequest(ctx context.Context, id int64, in store.KeyInput, adminIP string) (*store.APIKey, string, error)
	RejectKeyRequest(ctx context.Context, id int64) error
}

type Config struct {
	Username     string
	Password     string
	SecureCookie bool
	TrustProxy   bool
}

type Handler struct {
	store    Store
	cfg      Config
	userHash [32]byte
	passHash [32]byte

	mu       sync.Mutex
	sessions map[string]time.Time
	fails    map[string][]time.Time
}

func New(s Store, cfg Config) (*Handler, error) {
	if cfg.Username == "" {
		return nil, errors.New("SUPERUSER_USERNAME is not set")
	}
	if len(cfg.Password) < MinPasswordLen {
		return nil, fmt.Errorf("SUPERUSER_PASSWORD must be at least %d characters", MinPasswordLen)
	}
	return &Handler{
		store:    s,
		cfg:      cfg,
		userHash: sha256.Sum256([]byte(cfg.Username)),
		passHash: sha256.Sum256([]byte(cfg.Password)),
		sessions: make(map[string]time.Time),
		fails:    make(map[string][]time.Time),
	}, nil
}

// Register mounts the admin API under /admin/api/.
func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("POST /admin/api/login", h.login)
	mux.HandleFunc("POST /admin/api/logout", h.logout)
	mux.HandleFunc("GET /admin/api/me", h.requireSession(h.me))
	mux.HandleFunc("GET /admin/api/keys", h.requireSession(h.listKeys))
	mux.HandleFunc("POST /admin/api/keys", h.requireSession(h.createKey))
	mux.HandleFunc("PUT /admin/api/keys/{id}", h.requireSession(h.updateKey))
	mux.HandleFunc("GET /admin/api/keys/{id}/secret", h.requireSession(h.revealKey))
	mux.HandleFunc("POST /admin/api/keys/{id}/enable", h.requireSession(h.setActive(true)))
	mux.HandleFunc("POST /admin/api/keys/{id}/disable", h.requireSession(h.setActive(false)))
	mux.HandleFunc("DELETE /admin/api/keys/{id}", h.requireSession(h.deleteKey))
	mux.HandleFunc("GET /admin/api/logs", h.requireSession(h.listLogs))
	mux.HandleFunc("GET /admin/api/requests", h.requireSession(h.listRequests))
	mux.HandleFunc("POST /admin/api/requests/{id}/approve", h.requireSession(h.approveRequest))
	mux.HandleFunc("POST /admin/api/requests/{id}/reject", h.requireSession(h.rejectRequest))
}

func (h *Handler) login(w http.ResponseWriter, r *http.Request) {
	ip := auth.ClientIP(r, h.cfg.TrustProxy)
	if h.tooManyFails(ip) {
		auth.WriteJSON(w, http.StatusTooManyRequests, errBody("too many failed login attempts, try again in 15 minutes"))
		return
	}

	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body); err != nil {
		auth.WriteJSON(w, http.StatusBadRequest, errBody("invalid JSON"))
		return
	}

	u := sha256.Sum256([]byte(body.Username))
	p := sha256.Sum256([]byte(body.Password))
	userOK := subtle.ConstantTimeCompare(u[:], h.userHash[:])
	passOK := subtle.ConstantTimeCompare(p[:], h.passHash[:])
	if userOK&passOK != 1 {
		h.recordFail(ip)
		log.Printf("admin: failed login from %s", ip)
		auth.WriteJSON(w, http.StatusUnauthorized, errBody("invalid username or password"))
		return
	}

	token, err := randomToken()
	if err != nil {
		auth.WriteJSON(w, http.StatusInternalServerError, errBody("could not create session"))
		return
	}
	h.mu.Lock()
	delete(h.fails, ip)
	h.sessions[token] = time.Now().Add(sessionTTL)
	h.mu.Unlock()

	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    token,
		Path:     "/admin",
		MaxAge:   int(sessionTTL.Seconds()),
		HttpOnly: true,
		Secure:   h.cfg.SecureCookie,
		SameSite: http.SameSiteStrictMode,
	})
	log.Printf("admin: login from %s", ip)
	auth.WriteJSON(w, http.StatusOK, map[string]string{"username": h.cfg.Username})
}

func (h *Handler) logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(cookieName); err == nil {
		h.mu.Lock()
		delete(h.sessions, c.Value)
		h.mu.Unlock()
	}
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: "", Path: "/admin", MaxAge: -1, HttpOnly: true,
		Secure: h.cfg.SecureCookie, SameSite: http.SameSiteStrictMode})
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) me(w http.ResponseWriter, r *http.Request) {
	auth.WriteJSON(w, http.StatusOK, map[string]string{"username": h.cfg.Username})
}

func (h *Handler) requireSession(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(cookieName)
		if err != nil || !h.validSession(c.Value) {
			auth.WriteJSON(w, http.StatusUnauthorized, errBody("login required"))
			return
		}
		next(w, r)
	}
}

func (h *Handler) validSession(token string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	expires, ok := h.sessions[token]
	if !ok {
		return false
	}
	if time.Now().After(expires) {
		delete(h.sessions, token)
		return false
	}
	return true
}

func (h *Handler) tooManyFails(ip string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	cutoff := time.Now().Add(-loginFailWindow)
	recent := h.fails[ip][:0]
	for _, t := range h.fails[ip] {
		if t.After(cutoff) {
			recent = append(recent, t)
		}
	}
	h.fails[ip] = recent
	return len(recent) >= maxLoginFails
}

func (h *Handler) recordFail(ip string) {
	h.mu.Lock()
	h.fails[ip] = append(h.fails[ip], time.Now())
	h.mu.Unlock()
}

func (h *Handler) listKeys(w http.ResponseWriter, r *http.Request) {
	keys, err := h.store.ListKeys(r.Context())
	if err != nil {
		serverError(w, err)
		return
	}
	auth.WriteJSON(w, http.StatusOK, keys)
}

func (h *Handler) createKey(w http.ResponseWriter, r *http.Request) {
	var in store.KeyInput
	if !decode(w, r, &in) {
		return
	}
	key, raw, err := h.store.CreateKey(r.Context(), in, auth.ClientIP(r, h.cfg.TrustProxy))
	if err != nil {
		if isValidation(err) {
			auth.WriteJSON(w, http.StatusBadRequest, errBody(err.Error()))
			return
		}
		serverError(w, err)
		return
	}
	log.Printf("admin: created API key %d (%s)", key.ID, key.Name)
	auth.WriteJSON(w, http.StatusCreated, map[string]any{"key": key, "api_key": raw})
}

func (h *Handler) updateKey(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var in store.KeyInput
	if !decode(w, r, &in) {
		return
	}
	key, err := h.store.UpdateKey(r.Context(), id, in)
	switch {
	case errors.Is(err, store.ErrNotFound):
		auth.WriteJSON(w, http.StatusNotFound, errBody("API key not found"))
	case err != nil && isValidation(err):
		auth.WriteJSON(w, http.StatusBadRequest, errBody(err.Error()))
	case err != nil:
		serverError(w, err)
	default:
		log.Printf("admin: updated API key %d", id)
		auth.WriteJSON(w, http.StatusOK, key)
	}
}

func (h *Handler) revealKey(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	raw, err := h.store.RevealKey(r.Context(), id)
	switch {
	case errors.Is(err, store.ErrNotFound):
		auth.WriteJSON(w, http.StatusNotFound, errBody("API key not found"))
	case errors.Is(err, store.ErrNotRecoverable):
		auth.WriteJSON(w, http.StatusGone, errBody(err.Error()))
	case err != nil:
		serverError(w, err)
	default:
		log.Printf("admin: revealed API key %d to %s", id, auth.ClientIP(r, h.cfg.TrustProxy))
		w.Header().Set("Cache-Control", "no-store")
		auth.WriteJSON(w, http.StatusOK, map[string]string{"api_key": raw})
	}
}

func (h *Handler) setActive(active bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := pathID(w, r)
		if !ok {
			return
		}
		h.writeMutation(w, h.store.SetKeyActive(r.Context(), id, active))
	}
}

func (h *Handler) deleteKey(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	log.Printf("admin: deleting API key %d", id)
	h.writeMutation(w, h.store.DeleteKey(r.Context(), id))
}

func (h *Handler) writeMutation(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		auth.WriteJSON(w, http.StatusNotFound, errBody("API key not found"))
	case err != nil:
		serverError(w, err)
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

func (h *Handler) listLogs(w http.ResponseWriter, r *http.Request) {
	keyID, _ := strconv.ParseInt(r.URL.Query().Get("key_id"), 10, 64)
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	logs, err := h.store.ListLogs(r.Context(), keyID, limit)
	if err != nil {
		serverError(w, err)
		return
	}
	auth.WriteJSON(w, http.StatusOK, logs)
}

func (h *Handler) listRequests(w http.ResponseWriter, r *http.Request) {
	reqs, err := h.store.ListKeyRequests(r.Context())
	if err != nil {
		serverError(w, err)
		return
	}
	auth.WriteJSON(w, http.StatusOK, reqs)
}

// approveRequest creates the API key described in the body and marks the request approved.
func (h *Handler) approveRequest(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var in store.KeyInput
	if !decode(w, r, &in) {
		return
	}
	key, raw, err := h.store.ApproveKeyRequest(r.Context(), id, in, auth.ClientIP(r, h.cfg.TrustProxy))
	if h.writeRequestError(w, err) {
		return
	}
	log.Printf("admin: approved key request %d as API key %d", id, key.ID)
	auth.WriteJSON(w, http.StatusCreated, map[string]any{"key": key, "api_key": raw})
}

func (h *Handler) rejectRequest(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if h.writeRequestError(w, h.store.RejectKeyRequest(r.Context(), id)) {
		return
	}
	log.Printf("admin: rejected key request %d", id)
	w.WriteHeader(http.StatusNoContent)
}

// writeRequestError writes the response for a failed key request action and reports whether it did.
func (h *Handler) writeRequestError(w http.ResponseWriter, err error) bool {
	switch {
	case err == nil:
		return false
	case errors.Is(err, store.ErrNotFound):
		auth.WriteJSON(w, http.StatusNotFound, errBody("request not found"))
	case errors.Is(err, store.ErrAlreadyReviewed):
		auth.WriteJSON(w, http.StatusConflict, errBody(err.Error()))
	case isValidation(err):
		auth.WriteJSON(w, http.StatusBadRequest, errBody(err.Error()))
	default:
		serverError(w, err)
	}
	return true
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(v); err != nil {
		auth.WriteJSON(w, http.StatusBadRequest, errBody("invalid JSON"))
		return false
	}
	return true
}

func pathID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		auth.WriteJSON(w, http.StatusBadRequest, errBody("invalid key id"))
		return 0, false
	}
	return id, true
}

// isValidation reports whether err came from input validation rather than the database.
func isValidation(err error) bool {
	var v *store.ValidationError
	return errors.As(err, &v)
}

func serverError(w http.ResponseWriter, err error) {
	log.Printf("admin: %v", err)
	auth.WriteJSON(w, http.StatusInternalServerError, errBody("internal error"))
}

func errBody(msg string) map[string]string {
	return map[string]string{"error": msg}
}

func randomToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}
