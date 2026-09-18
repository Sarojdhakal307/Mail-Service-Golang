package auth

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net"
	"net/http"
	"strings"

	"mailservice/internal/store"
)

// KeyStore is the subset of the store the middleware needs.
type KeyStore interface {
	FindKeyByHash(ctx context.Context, hash []byte) (*store.APIKey, error)
	LogRequest(ctx context.Context, entry store.RequestLog) error
}

// Caller identifies the authenticated client of a request.
type Caller struct {
	Key *store.APIKey
	IP  string
}

type ctxKey struct{}

// CallerFrom returns the authenticated caller stored by the middleware.
func CallerFrom(ctx context.Context) (Caller, bool) {
	c, ok := ctx.Value(ctxKey{}).(Caller)
	return c, ok
}

// WithCaller returns a context carrying c. Useful for tests.
func WithCaller(ctx context.Context, c Caller) context.Context {
	return context.WithValue(ctx, ctxKey{}, c)
}

// APIKeyAuth protects every path that is not explicitly public.
type APIKeyAuth struct {
	store       KeyStore
	trustProxy  bool
	publicPaths map[string]bool
	publicTrees []string
}

// NewAPIKeyAuth builds the middleware. Public entries ending in "/" match the whole subtree.
func NewAPIKeyAuth(s KeyStore, trustProxy bool, public ...string) *APIKeyAuth {
	a := &APIKeyAuth{store: s, trustProxy: trustProxy, publicPaths: make(map[string]bool)}
	for _, p := range public {
		if strings.HasSuffix(p, "/") && p != "/" {
			a.publicTrees = append(a.publicTrees, p)
		} else {
			a.publicPaths[p] = true
		}
	}
	return a
}

func (a *APIKeyAuth) isPublic(path string) bool {
	if a.publicPaths[path] {
		return true
	}
	for _, prefix := range a.publicTrees {
		if strings.HasPrefix(path, prefix) {
			return true
		}
	}
	return false
}

// Middleware accepts a key from "X-API-Key" or "Authorization: Bearer <key>" and rejects
// requests with a missing, unknown or disabled key, or from an IP the key does not allow.
func (a *APIKeyAuth) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if a.isPublic(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}

		ip := ClientIP(r, a.trustProxy)
		raw := extractKey(r)
		if raw == "" {
			log.Printf("auth: missing API key for %s %s from %s", r.Method, r.URL.Path, ip)
			writeError(w, http.StatusUnauthorized, "missing API key: send it in the X-API-Key header or as Authorization: Bearer <key>")
			return
		}

		key, err := a.store.FindKeyByHash(r.Context(), store.HashKey(raw))
		if errors.Is(err, store.ErrNotFound) {
			log.Printf("auth: invalid API key for %s %s from %s", r.Method, r.URL.Path, ip)
			writeError(w, http.StatusUnauthorized, "invalid API key")
			return
		}
		if err != nil {
			log.Printf("auth: key lookup failed: %v", err)
			writeError(w, http.StatusServiceUnavailable, "authentication temporarily unavailable")
			return
		}

		entry := store.RequestLog{APIKeyID: key.ID, IP: ip, Method: r.Method, Path: r.URL.Path, UserAgent: r.UserAgent()}
		if !key.Active {
			entry.Status, entry.Message = store.StatusKeyDisabled, "API key is disabled"
			a.logRejected(r.Context(), entry)
			writeError(w, http.StatusForbidden, "API key is disabled")
			return
		}
		if !key.AllowsIP(ip) {
			entry.Status, entry.Message = store.StatusIPDenied, "IP address "+ip+" is not allowed for this API key"
			a.logRejected(r.Context(), entry)
			writeError(w, http.StatusForbidden, entry.Message)
			return
		}

		next.ServeHTTP(w, r.WithContext(WithCaller(r.Context(), Caller{Key: key, IP: ip})))
	})
}

func (a *APIKeyAuth) logRejected(ctx context.Context, entry store.RequestLog) {
	log.Printf("auth: rejected key %d from %s: %s", entry.APIKeyID, entry.IP, entry.Message)
	if err := a.store.LogRequest(ctx, entry); err != nil {
		log.Printf("auth: failed to record rejected request: %v", err)
	}
}

func extractKey(r *http.Request) string {
	if key := strings.TrimSpace(r.Header.Get("X-API-Key")); key != "" {
		return key
	}
	authz := r.Header.Get("Authorization")
	if len(authz) > len("Bearer ") && strings.EqualFold(authz[:len("Bearer ")], "Bearer ") {
		return strings.TrimSpace(authz[len("Bearer "):])
	}
	return ""
}

// ClientIP returns the caller's IP. X-Forwarded-For is only honoured when trustProxy is set,
// because clients can otherwise forge it to bypass IP restrictions.
func ClientIP(r *http.Request, trustProxy bool) string {
	if trustProxy {
		if fwd := r.Header.Get("X-Forwarded-For"); fwd != "" {
			first, _, _ := strings.Cut(fwd, ",")
			if ip := strings.TrimSpace(first); net.ParseIP(ip) != nil {
				return ip
			}
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func writeError(w http.ResponseWriter, status int, message string) {
	if status == http.StatusUnauthorized {
		w.Header().Set("WWW-Authenticate", `Bearer realm="mailservice"`)
	}
	WriteJSON(w, status, map[string]string{"error": message})
}

// WriteJSON writes v as a JSON response.
func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
