package routes

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"

	"mailservice/internal/services"
	"mailservice/internal/types"
)

func RegisterRoutes(mux *http.ServeMux, mailService *services.MailService) {
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
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte("invalid JSON"))
			return
		}

		msg := types.MailMessage{To: req.To, Target: req.Target, Subject: req.Subject, Body: req.Body}
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
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte("invalid JSON"))
			return
		}

		if len(req.Recipients) == 0 || strings.TrimSpace(req.Body) == "" {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte("recipients and body are required"))
			return
		}

		queued := 0
		for _, recipient := range req.Recipients {
			if strings.TrimSpace(recipient) == "" {
				continue
			}
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
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte("invalid JSON"))
			return
		}

		if len(req.Recipients) == 0 || strings.TrimSpace(req.Body) == "" {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte("recipients and body are required"))
			return
		}

		queued := 0
		for _, recipient := range req.Recipients {
			if strings.TrimSpace(recipient) == "" {
				continue
			}
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
