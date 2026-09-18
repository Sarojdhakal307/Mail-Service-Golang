package public

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"mime"
	"net/http"
	"sync"
	"time"

	"mailservice/internal/auth"
	"mailservice/internal/store"
)

const (
	maxRequestsPerIP = 3
	requestWindow    = time.Hour
)

// Store is the subset of the store the public API needs.
type Store interface {
	Ping(ctx context.Context) error
	CreateKeyRequest(ctx context.Context, in store.KeyRequestInput, ip, userAgent string) (*store.KeyRequest, error)
}

type Config struct {
	Version    string
	Delivery   string // "smtp" or "simulation"
	Workers    int
	TrustProxy bool
}

type Handler struct {
	store     Store
	cfg       Config
	startedAt time.Time

	mu       sync.Mutex
	attempts map[string][]time.Time
}

func New(s Store, cfg Config) *Handler {
	return &Handler{store: s, cfg: cfg, startedAt: time.Now(), attempts: make(map[string][]time.Time)}
}

// PublicPaths lists the routes that must not require an API key.
func PublicPaths() []string {
	return []string{"/api/status", "/api/key-requests"}
}

func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/status", h.status)
	mux.HandleFunc("POST /api/key-requests", h.createRequest)
}

func (h *Handler) status(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()

	status, database := "operational", "ok"
	if err := h.store.Ping(ctx); err != nil {
		status, database = "degraded", "unavailable"
	}
	w.Header().Set("Cache-Control", "no-store")
	auth.WriteJSON(w, http.StatusOK, map[string]any{
		"status":         status,
		"version":        h.cfg.Version,
		"uptime_seconds": int(time.Since(h.startedAt).Seconds()),
		"database":       database,
		"delivery":       h.cfg.Delivery,
		"workers":        h.cfg.Workers,
		"server_time":    time.Now().UTC(),
	})
}

func (h *Handler) createRequest(w http.ResponseWriter, r *http.Request) {
	// Requiring JSON stops plain cross-site HTML forms from posting here.
	if mediaType, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type")); mediaType != "application/json" {
		auth.WriteJSON(w, http.StatusUnsupportedMediaType, errBody("send the request as JSON"))
		return
	}

	var body struct {
		store.KeyRequestInput
		Website string `json:"website"` // honeypot: hidden from people, filled in by bots
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<10)).Decode(&body); err != nil {
		auth.WriteJSON(w, http.StatusBadRequest, errBody("invalid request"))
		return
	}

	ip := auth.ClientIP(r, h.cfg.TrustProxy)
	if body.Website != "" {
		log.Printf("public: dropped key request from %s (honeypot)", ip)
		auth.WriteJSON(w, http.StatusCreated, map[string]string{"message": "Thanks, your request was received."})
		return
	}
	if !h.allow(ip) {
		auth.WriteJSON(w, http.StatusTooManyRequests,
			errBody("you have sent several requests recently; please wait an hour before trying again"))
		return
	}

	req, err := h.store.CreateKeyRequest(r.Context(), body.KeyRequestInput, ip, r.UserAgent())
	var v *store.ValidationError
	switch {
	case errors.As(err, &v):
		auth.WriteJSON(w, http.StatusBadRequest, errBody(err.Error()))
	case err != nil:
		log.Printf("public: store key request: %v", err)
		auth.WriteJSON(w, http.StatusInternalServerError, errBody("could not save your request, please try again later"))
	default:
		h.record(ip)
		log.Printf("public: key request %d from %s <%s>", req.ID, ip, req.Email)
		auth.WriteJSON(w, http.StatusCreated, map[string]string{
			"message": "Thanks, " + req.Name + ". Your request was received and we will contact you at " + req.Email + ".",
		})
	}
}

func (h *Handler) allow(ip string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	cutoff := time.Now().Add(-requestWindow)
	recent := h.attempts[ip][:0]
	for _, t := range h.attempts[ip] {
		if t.After(cutoff) {
			recent = append(recent, t)
		}
	}
	if len(recent) == 0 {
		delete(h.attempts, ip)
	} else {
		h.attempts[ip] = recent
	}
	return len(recent) < maxRequestsPerIP
}

func (h *Handler) record(ip string) {
	h.mu.Lock()
	h.attempts[ip] = append(h.attempts[ip], time.Now())
	h.mu.Unlock()
}

func errBody(msg string) map[string]string {
	return map[string]string{"error": msg}
}
