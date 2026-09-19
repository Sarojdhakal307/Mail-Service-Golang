package services

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"mailservice/internal/store"
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

// Recorder keeps the mail history: every mail and how its delivery went.
type Recorder interface {
	RecordMail(ctx context.Context, m store.MailRecord, recipients []string) ([]int64, error)
	MarkDelivery(ctx context.Context, id int64, status, errMsg string) error
}

type MailService struct {
	workers int
	queue   chan types.MailMessage
	sender  MailSender
	wg      sync.WaitGroup
	stop    chan struct{}

	recorder    Recorder
	defaultSMTP *types.SMTPConfig
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

// EnableRecording stores every mail submitted from now on in the mail history.
// defaultSMTP is the server used by mail without its own (nil in simulation mode), and is
// only used to record the sender. Call it before Start.
func (s *MailService) EnableRecording(r Recorder, defaultSMTP *types.SMTPConfig) {
	s.recorder = r
	s.defaultSMTP = defaultSMTP
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
					s.deliver(workerID, msg)
				}
			}
		}(i)
	}
}

func (s *MailService) deliver(workerID int, msg types.MailMessage) {
	log.Printf("worker %d picked job for recipient %s from queue", workerID, msg.To)
	s.mark(msg.DeliveryID, store.MailSending, "")

	sender := s.senderFor(msg)
	if err := sender.Send(context.Background(), msg); err != nil {
		log.Printf("worker %d failed to send mail: %v", workerID, err)
		s.mark(msg.DeliveryID, store.MailFailed, err.Error())
		return
	}
	if _, simulated := sender.(*NoopMailer); simulated {
		s.mark(msg.DeliveryID, store.MailSimulated, "")
	} else {
		s.mark(msg.DeliveryID, store.MailSent, "")
	}
}

// mark updates a recorded mail's status. Failures are only logged: the mail itself has
// already been handled.
func (s *MailService) mark(id int64, status, errMsg string) {
	if s.recorder == nil || id == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.recorder.MarkDelivery(ctx, id, status, errMsg); err != nil {
		log.Printf("mail history: mark delivery %d as %s: %v", id, status, err)
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

// SenderFor returns the From address and host:port that mail through smtp (nil for the
// default server) goes out with. Both are empty in simulation mode.
func (s *MailService) SenderFor(smtp *types.SMTPConfig) (from, host string) {
	if smtp == nil {
		smtp = s.defaultSMTP
	}
	if smtp == nil {
		return "", ""
	}
	return smtp.From, smtp.Host + ":" + smtp.Port
}

// Submit records the batch in the mail history and queues one mail per recipient. It
// returns how many were queued. If the history cannot be written the mail is still sent.
func (s *MailService) Submit(ctx context.Context, b types.MailBatch) (int, error) {
	if len(b.Recipients) == 0 || strings.TrimSpace(b.Body) == "" {
		return 0, fmt.Errorf("recipients and body are required")
	}

	ids := make([]int64, len(b.Recipients))
	if s.recorder != nil {
		from, host := s.SenderFor(b.SMTP)
		rec := store.MailRecord{
			APIKeyID: b.KeyID, KeyName: b.KeyName, Source: b.Source, Path: b.Path, IP: b.IP,
			Sender: from, SMTPHost: host, Subject: b.Subject, Body: b.Body,
		}
		if recorded, err := s.recorder.RecordMail(ctx, rec, b.Recipients); err != nil {
			log.Printf("mail history: record %d mail(s) from %s: %v", len(b.Recipients), b.Path, err)
		} else {
			ids = recorded
		}
	}

	queued := 0
	for i, to := range b.Recipients {
		msg := types.MailMessage{To: to, Subject: b.Subject, Body: b.Body, SMTP: b.SMTP, DeliveryID: ids[i]}
		if err := s.Enqueue(msg); err != nil {
			log.Printf("enqueue failed for %s: %v", to, err)
			s.mark(ids[i], store.MailFailed, err.Error())
			continue
		}
		queued++
	}
	return queued, nil
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
