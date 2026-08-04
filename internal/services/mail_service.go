package services

import (
	"context"
	"fmt"
	"log"
	"net/smtp"
	"os"
	"strings"
	"sync"

	"mailservice/internal/types"
)

type MailSender interface {
	Send(ctx context.Context, msg types.MailMessage) error
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

func (m *SMTPMailer) Send(ctx context.Context, msg types.MailMessage) error {
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

func (m *NoopMailer) Send(ctx context.Context, msg types.MailMessage) error {
	log.Printf("simulating mail send to %s with subject %q", msg.To, msg.Subject)
	return nil
}

type MailService struct {
	workers int
	queue   chan types.MailMessage
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
		queue:   make(chan types.MailMessage, 100),
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
					log.Printf("worker %d picked job for recipient %s from queue", workerID, msg.To)
					if err := s.sender.Send(context.Background(), msg); err != nil {
						log.Printf("worker %d failed to send mail: %v", workerID, err)
					}
				}
			}
		}(i)
	}
}

func (s *MailService) Enqueue(msg types.MailMessage) error {
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
		log.Printf("queued mail for recipient %s", msg.To)
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

func RenderTemplate(body string, meta map[string][]string) string {
	result := body
	for key, values := range meta {
		placeholder := "{{" + key + "}}"
		if len(values) > 0 {
			result = strings.ReplaceAll(result, placeholder, values[0])
		} else {
			result = strings.ReplaceAll(result, placeholder, "")
		}
	}
	return result
}
