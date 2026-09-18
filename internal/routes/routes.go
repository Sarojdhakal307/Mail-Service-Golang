package routes

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"mailservice/internal/auth"
	"mailservice/internal/services"
	"mailservice/internal/store"
	"mailservice/internal/types"
)

const maxBodyBytes = 1 << 20

// Quota records a request against the caller's API key and enforces its limits.
type Quota interface {
	Reserve(ctx context.Context, key *store.APIKey, entry store.RequestLog, n int) error
}

func RegisterRoutes(mux *http.ServeMux, mailService *services.MailService, quota Quota) {
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	mux.HandleFunc("/send", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}

		var req types.SendRequest
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes)).Decode(&req); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte("invalid JSON"))
			return
		}

		msg := types.MailMessage{To: req.To, Target: req.Target, Subject: req.Subject, Body: req.Body}
		if strings.TrimSpace(msg.To) == "" && strings.TrimSpace(msg.Target) == "" || strings.TrimSpace(msg.Body) == "" {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte("recipient and body are required"))
			return
		}

		if !reserve(w, r, quota, 1) {
			return
		}

		if err := mailService.Enqueue(msg); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(err.Error()))
			return
		}

		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte("mail queued"))
	})

	mux.HandleFunc("/send/bulk", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}

		var req types.BulkSendRequest
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes)).Decode(&req); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte("invalid JSON"))
			return
		}

		recipients := nonBlank(req.Recipients)
		if len(recipients) == 0 || strings.TrimSpace(req.Body) == "" {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte("recipients and body are required"))
			return
		}

		if !reserve(w, r, quota, len(recipients)) {
			return
		}

		queued := 0
		for _, recipient := range recipients {
			msg := types.MailMessage{To: recipient, Subject: req.Subject, Body: req.Body}
			if err := mailService.Enqueue(msg); err != nil {
				log.Printf("bulk enqueue failed for %s: %v", recipient, err)
				continue
			}
			queued++
		}

		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(fmt.Sprintf("%d mails queued", queued)))
	})

	mux.HandleFunc("/send/template", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}

		var req types.TemplateSendRequest
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes)).Decode(&req); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte("invalid JSON"))
			return
		}

		recipients := nonBlank(req.Recipients)
		if len(recipients) == 0 || strings.TrimSpace(req.Body) == "" {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte("recipients and body are required"))
			return
		}

		if !reserve(w, r, quota, len(recipients)) {
			return
		}

		queued := 0
		for _, recipient := range recipients {
			body := services.RenderTemplate(req.Body, req.Meta)
			msg := types.MailMessage{To: recipient, Subject: req.Subject, Body: body}
			if err := mailService.Enqueue(msg); err != nil {
				log.Printf("template enqueue failed for %s: %v", recipient, err)
				continue
			}
			queued++
		}

		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(fmt.Sprintf("%d templated mails queued", queued)))
	})
}

func nonBlank(values []string) []string {
	out := make([]string, 0, len(values))
	for _, v := range values {
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	}
	return out
}

// reserve charges n mails to the caller's API key. It writes the error response and
// returns false when the request must not proceed.
func reserve(w http.ResponseWriter, r *http.Request, quota Quota, n int) bool {
	caller, ok := auth.CallerFrom(r.Context())
	if !ok {
		auth.WriteJSON(w, http.StatusUnauthorized, map[string]string{"error": "missing API key"})
		return false
	}

	entry := store.RequestLog{IP: caller.IP, Method: r.Method, Path: r.URL.Path, UserAgent: r.UserAgent()}
	err := quota.Reserve(r.Context(), caller.Key, entry, n)
	if err == nil {
		return true
	}

	var limitErr *store.LimitError
	if errors.As(err, &limitErr) {
		if limitErr.RetryAt != nil {
			seconds := int(time.Until(*limitErr.RetryAt).Seconds()) + 1
			w.Header().Set("Retry-After", strconv.Itoa(max(seconds, 1)))
		}
		auth.WriteJSON(w, http.StatusTooManyRequests, map[string]any{
			"error":     limitErr.Error(),
			"window":    limitErr.Window,
			"limit":     limitErr.Limit,
			"used":      limitErr.Used,
			"requested": limitErr.Requested,
			"retry_at":  limitErr.RetryAt,
		})
		return false
	}

	log.Printf("quota check failed: %v", err)
	auth.WriteJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "could not verify usage limits, try again"})
	return false
}
