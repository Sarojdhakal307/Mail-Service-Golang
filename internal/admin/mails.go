package admin

import (
	"errors"
	"log"
	"net/http"
	"strconv"
	"strings"

	"mailservice/internal/auth"
	"mailservice/internal/store"
	"mailservice/internal/types"
)

// listMails returns the mail history, newest first, with the number of mails per status
// for the same key and search.
func (h *Handler) listMails(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f := store.MailFilter{Source: q.Get("source"), Status: q.Get("status"), Query: q.Get("q")}
	f.KeyID, _ = strconv.ParseInt(q.Get("key_id"), 10, 64)
	f.BeforeID, _ = strconv.ParseInt(q.Get("before"), 10, 64)
	f.Limit, _ = strconv.Atoi(q.Get("limit"))

	mails, err := h.store.ListMails(r.Context(), f)
	if err != nil {
		serverError(w, err)
		return
	}
	counts, err := h.store.MailCounts(r.Context(), f)
	if err != nil {
		serverError(w, err)
		return
	}
	// The failed total across all mail drives the badge in the navigation.
	all := counts
	if f.KeyID > 0 || f.Source != "" || strings.TrimSpace(f.Query) != "" {
		if all, err = h.store.MailCounts(r.Context(), store.MailFilter{}); err != nil {
			serverError(w, err)
			return
		}
	}
	auth.WriteJSON(w, http.StatusOK, map[string]any{"mails": mails, "counts": counts, "failed_total": all[store.MailFailed]})
}

func (h *Handler) getMail(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	d, err := h.store.GetMail(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		auth.WriteJSON(w, http.StatusNotFound, errBody("mail not found"))
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	auth.WriteJSON(w, http.StatusOK, d)
}

// retryMail queues a failed mail again, through the SMTP server its key uses now. Retries
// are not counted against the key's limits, which were charged when the mail was sent.
func (h *Handler) retryMail(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	d, err := h.store.GetMail(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		auth.WriteJSON(w, http.StatusNotFound, errBody("mail not found"))
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	if d.Status != store.MailFailed {
		auth.WriteJSON(w, http.StatusConflict, errBody(store.ErrNotRetryable.Error()))
		return
	}

	var smtp *types.SMTPConfig
	if d.Source != types.SourceSystem {
		if d.APIKeyID == nil {
			auth.WriteJSON(w, http.StatusConflict, errBody("the API key that sent this mail was deleted"))
			return
		}
		key, err := h.store.GetKey(r.Context(), *d.APIKeyID)
		if errors.Is(err, store.ErrNotFound) {
			auth.WriteJSON(w, http.StatusConflict, errBody("the API key that sent this mail was deleted"))
			return
		}
		if err != nil {
			serverError(w, err)
			return
		}
		if !key.Active {
			auth.WriteJSON(w, http.StatusConflict, errBody("“"+key.Name+"” is disabled; enable it before sending again"))
			return
		}
		if smtp, err = h.store.SMTPConfig(key); err != nil {
			serverError(w, err)
			return
		}
	}

	from, host := h.cfg.SenderFor(smtp)
	if err := h.store.RequeueDelivery(r.Context(), d.ID, from, host); err != nil {
		if errors.Is(err, store.ErrNotRetryable) {
			auth.WriteJSON(w, http.StatusConflict, errBody(err.Error()))
			return
		}
		serverError(w, err)
		return
	}
	msg := types.MailMessage{To: d.Recipient, Subject: d.Subject, Body: d.Body, SMTP: smtp, DeliveryID: d.ID}
	if err := h.cfg.Enqueue(msg); err != nil {
		_ = h.store.MarkDelivery(r.Context(), d.ID, store.MailFailed, err.Error())
		auth.WriteJSON(w, http.StatusServiceUnavailable, errBody("could not queue the mail: "+err.Error()))
		return
	}
	log.Printf("admin: retrying mail %d to %s", d.ID, d.Recipient)
	auth.WriteJSON(w, http.StatusAccepted, map[string]string{"message": "Mail to " + d.Recipient + " queued again."})
}
