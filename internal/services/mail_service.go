package services

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"

	"mailservice/internal/types"
)

type MailSender interface {
	Send(ctx context.Context, msg types.MailMessage) error
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
					if err := s.senderFor(msg).Send(context.Background(), msg); err != nil {
						log.Printf("worker %d failed to send mail: %v", workerID, err)
					}
				}
			}
		}(i)
	}
}

// senderFor uses the SMTP server attached to the message (from its API key), falling back
// to the default sender.
func (s *MailService) senderFor(msg types.MailMessage) MailSender {
	if msg.SMTP != nil {
		return NewSMTPMailerFromConfig(*msg.SMTP)
	}
	return s.sender
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
