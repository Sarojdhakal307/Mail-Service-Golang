package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/smtp"
	"os"
	"strings"
	"sync"
)

type MailMessage struct {
	To      string `json:"to"`
	Target  string `json:"target"`
	Subject string `json:"subject"`
	Body    string `json:"body"`
}

type MailSender interface {
	Send(ctx context.Context, msg MailMessage) error
}

type SMTPMailer struct {
	host     string
	port     string
	username string
	password string
	from     string
}

func NewSMTPMailer() MailSender {
	host := os.Getenv("SMTP_HOST")
	port := os.Getenv("SMTP_PORT")
	username := os.Getenv("SMTP_USERNAME")
	password := os.Getenv("SMTP_PASSWORD")
	from := os.Getenv("SMTP_FROM")

	if host == "" || port == "" || from == "" {
		return &NoopMailer{}
	}

	return &SMTPMailer{host: host, port: port, username: username, password: password, from: from}
}

func (m *SMTPMailer) Send(ctx context.Context, msg MailMessage) error {
	addr := fmt.Sprintf("%s:%s", m.host, m.port)
	auth := smtp.PlainAuth("", m.username, m.password, m.host)
	recipient := []string{msg.To}
	if msg.To == "" && msg.Target != "" {
		recipient = []string{msg.Target}
	}
	if len(recipient) == 0 || recipient[0] == "" {
		return fmt.Errorf("mail recipient is required")
	}

	body := []byte(fmt.Sprintf("To: %s\r\nSubject: %s\r\n\r\n%s\r\n", recipient[0], msg.Subject, msg.Body))
	return smtp.SendMail(addr, auth, m.from, recipient, body)
}

type NoopMailer struct{}

func (m *NoopMailer) Send(ctx context.Context, msg MailMessage) error {
	log.Printf("simulating mail send to %s with subject %q", msg.To, msg.Subject)
	return nil
}

type MailService struct {
	workers int
	queue   chan MailMessage
	sender  MailSender
	wg      sync.WaitGroup
	stop    chan struct{}
}

func NewMailService(workers int, sender MailSender) *MailService {
	if workers <= 0 {
		workers = 1
	}
	return &MailService{
		workers: workers,
		queue:   make(chan MailMessage, 100),
		sender:  sender,
		stop:    make(chan struct{}),
	}
}

func (s *MailService) Start() {
	for i := 0; i < s.workers; i++ {
		s.wg.Add(1)
		go func(workerID int) {
			defer s.wg.Done()
			for {
				select {
				case <-s.stop:
					return
				case msg, ok := <-s.queue:
					if !ok {
						return
					}
					if err := s.sender.Send(context.Background(), msg); err != nil {
						log.Printf("worker %d failed to send mail: %v", workerID, err)
					}
				}
			}
		}(i)
	}
}

func (s *MailService) Enqueue(msg MailMessage) error {
	recipient := msg.To
	if recipient == "" {
		recipient = msg.Target
	}
	if strings.TrimSpace(recipient) == "" || strings.TrimSpace(msg.Body) == "" {
		return fmt.Errorf("recipient and body are required")
	}
	msg.To = recipient
	select {
	case s.queue <- msg:
		return nil
	case <-s.stop:
		return fmt.Errorf("mail service is shutting down")
	}
}

func (s *MailService) Shutdown() {
	close(s.stop)
	close(s.queue)
	s.wg.Wait()
}

type sendRequest struct {
	Target  string `json:"target"`
	To      string `json:"to"`
	Subject string `json:"subject"`
	Body    string `json:"body"`
}

type bulkSendRequest struct {
	Recipients []string `json:"recipients"`
	Subject    string   `json:"subject"`
	Body       string   `json:"body"`
}

func main() {
	mailService := NewMailService(3, NewSMTPMailer())
	mailService.Start()

	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("/send", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}

		var req sendRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte("invalid JSON"))
			return
		}

		msg := MailMessage{To: req.To, Target: req.Target, Subject: req.Subject, Body: req.Body}
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

		var req bulkSendRequest
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
			msg := MailMessage{To: recipient, Subject: req.Subject, Body: req.Body}
			if err := mailService.Enqueue(msg); err != nil {
				log.Printf("bulk enqueue failed for %s: %v", recipient, err)
				continue
			}
			queued++
		}

		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(fmt.Sprintf("%d mails queued", queued)))
	})

	fmt.Println("Listening on :8080")
	log.Fatal(http.ListenAndServe(":8080", mux))
}
