package admin

import (
	"errors"
	"log"
	"net/http"
	"net/mail"
	"strconv"
	"strings"
	"time"

	"mailservice/internal/auth"
	"mailservice/internal/store"
	"mailservice/internal/types"
)

const (
	composePath          = "/admin/compose"
	maxComposeRecipients = 500
	maxSubjectLength     = 998
	maxComposeBody       = 512 << 10
)

// compose sends mail as one of the API keys: through the key's SMTP server, counted
// against its limits and recorded in its request log. The key's IP allow list does not
// apply because the super user, not the key's client, is sending.
func (h *Handler) compose(w http.ResponseWriter, r *http.Request) {
	var body struct {
		KeyID      int64    `json:"key_id"`
		Recipients []string `json:"recipients"`
		Subject    string   `json:"subject"`
		Body       string   `json:"body"`
	}
	if err := decodeLimit(w, r, &body, maxComposeBody+64<<10); err != nil {
		auth.WriteJSON(w, http.StatusBadRequest, errBody("invalid JSON"))
		return
	}

	recipients, err := parseRecipients(body.Recipients)
	if err != nil {
		auth.WriteJSON(w, http.StatusBadRequest, errBody(err.Error()))
		return
	}
	subject := strings.TrimSpace(body.Subject)
	if len(subject) > maxSubjectLength {
		auth.WriteJSON(w, http.StatusBadRequest, errBody("subject must be at most 998 characters"))
		return
	}
	if strings.TrimSpace(body.Body) == "" {
		auth.WriteJSON(w, http.StatusBadRequest, errBody("message is required"))
		return
	}
	if len(body.Body) > maxComposeBody {
		auth.WriteJSON(w, http.StatusBadRequest, errBody("message must be at most 512 KB"))
		return
	}

	key, err := h.store.GetKey(r.Context(), body.KeyID)
	if errors.Is(err, store.ErrNotFound) {
		auth.WriteJSON(w, http.StatusNotFound, errBody("choose an API key to send with"))
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	if !key.Active {
		auth.WriteJSON(w, http.StatusConflict, errBody("“"+key.Name+"” is disabled; enable it or choose another key"))
		return
	}

	smtp, err := h.store.SMTPConfig(key)
	if err != nil {
		serverError(w, err)
		return
	}

	entry := store.RequestLog{
		IP:        auth.ClientIP(r, h.cfg.TrustProxy),
		Method:    http.MethodPost,
		Path:      composePath,
		UserAgent: r.UserAgent(),
	}
	err = h.store.Reserve(r.Context(), key, entry, len(recipients))
	var limitErr *store.LimitError
	if errors.As(err, &limitErr) {
		if limitErr.RetryAt != nil {
			w.Header().Set("Retry-After", strconv.Itoa(max(int(time.Until(*limitErr.RetryAt).Seconds())+1, 1)))
		}
		auth.WriteJSON(w, http.StatusTooManyRequests, errBody(limitErr.Error()))
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}

	queued := 0
	for _, to := range recipients {
		msg := types.MailMessage{To: to, Subject: subject, Body: body.Body, SMTP: smtp}
		if err := h.cfg.Enqueue(msg); err != nil {
			log.Printf("admin: compose enqueue to %s failed: %v", to, err)
			continue
		}
		queued++
	}

	via := "the default SMTP server"
	switch {
	case smtp != nil:
		via = smtp.Host + ":" + smtp.Port + " as " + smtp.From
	case h.cfg.DefaultSMTP == nil:
		via = "simulation mode (no SMTP server configured, mail is only logged)"
	}
	log.Printf("admin: composed %d mail(s) with key %d via %s", queued, key.ID, via)
	auth.WriteJSON(w, http.StatusAccepted, map[string]any{
		"queued":  queued,
		"message": plural(queued, "mail") + " queued with “" + key.Name + "” through " + via + ".",
	})
}

// parseRecipients validates and de-duplicates addresses, accepting "Name <a@b.c>" forms.
func parseRecipients(values []string) ([]string, error) {
	seen := make(map[string]bool)
	var out []string
	for _, v := range values {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		addr, err := mail.ParseAddress(v)
		if err != nil {
			return nil, errors.New("“" + v + "” is not a valid email address")
		}
		lower := strings.ToLower(addr.Address)
		if seen[lower] {
			continue
		}
		seen[lower] = true
		out = append(out, addr.Address)
	}
	if len(out) == 0 {
		return nil, errors.New("add at least one recipient")
	}
	if len(out) > maxComposeRecipients {
		return nil, errors.New("send to at most 500 recipients at a time")
	}
	return out, nil
}

func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return strconv.Itoa(n) + " " + word + "s"
}
