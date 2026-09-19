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

// Quota records a request against the caller's API key and enforces its limits, and
// resolves the SMTP server the key sends through.
type Quota interface {
	Reserve(ctx context.Context, key *store.APIKey, entry store.RequestLog, n int) error
	SMTPConfig(key *store.APIKey) (*types.SMTPConfig, error)
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

		batch, ok := reserve(w, r, quota, 1)
		if !ok {
			return
		}
		batch.Recipients = []string{strings.TrimSpace(msg.To)}
		if batch.Recipients[0] == "" {
			batch.Recipients[0] = strings.TrimSpace(msg.Target)
		}
		batch.Subject, batch.Body = msg.Subject, msg.Body

		if queued, err := mailService.Submit(r.Context(), batch); err != nil || queued == 0 {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("mail could not be queued"))
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

		batch, ok := reserve(w, r, quota, len(recipients))
		if !ok {
			return
		}
		batch.Recipients, batch.Subject, batch.Body = recipients, req.Subject, req.Body

		queued, err := mailService.Submit(r.Context(), batch)
		if err != nil {
			log.Printf("bulk submit failed: %v", err)
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

		batch, ok := reserve(w, r, quota, len(recipients))
		if !ok {
			return
		}
		// Every recipient gets the same rendered body, so the batch is stored once.
		batch.Recipients, batch.Subject, batch.Body = recipients, req.Subject, services.RenderTemplate(req.Body, req.Meta)

		queued, err := mailService.Submit(r.Context(), batch)
		if err != nil {
			log.Printf("template submit failed: %v", err)
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

// reserve resolves the caller's SMTP server and charges n mails to its API key. It returns
// a batch filled in with the caller's key, IP and SMTP server, or writes the error response
// and returns false when the request must not proceed.
func reserve(w http.ResponseWriter, r *http.Request, quota Quota, n int) (types.MailBatch, bool) {
	smtp, ok := reserveSMTP(w, r, quota, n)
	if !ok {
		return types.MailBatch{}, false
	}
	caller, _ := auth.CallerFrom(r.Context())
	return types.MailBatch{
		Source:  types.SourceAPI,
		KeyID:   caller.Key.ID,
		KeyName: caller.Key.Name,
		Path:    r.URL.Path,
		IP:      caller.IP,
		SMTP:    smtp,
	}, true
}

func reserveSMTP(w http.ResponseWriter, r *http.Request, quota Quota, n int) (*types.SMTPConfig, bool) {
	caller, ok := auth.CallerFrom(r.Context())
	if !ok {
		auth.WriteJSON(w, http.StatusUnauthorized, map[string]string{"error": "missing API key"})
		return nil, false
	}

	// Load SMTP settings first so a broken configuration does not use up the key's limits.
	smtp, err := quota.SMTPConfig(caller.Key)
	if err != nil {
		log.Printf("smtp config for key %d: %v", caller.Key.ID, err)
		auth.WriteJSON(w, http.StatusInternalServerError,
			map[string]string{"error": "the SMTP settings for this API key could not be loaded; contact the administrator"})
		return nil, false
	}

	entry := store.RequestLog{IP: caller.IP, Method: r.Method, Path: r.URL.Path, UserAgent: r.UserAgent()}
	err = quota.Reserve(r.Context(), caller.Key, entry, n)
	if err == nil {
		return smtp, true
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
		return nil, false
	}

	log.Printf("quota check failed: %v", err)
	auth.WriteJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "could not verify usage limits, try again"})
	return nil, false
}
