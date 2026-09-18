package services

import (
	"context"
	"testing"
	"time"

	"mailservice/internal/types"
)

type fakeSender struct {
	sent chan types.MailMessage
}

func (f *fakeSender) Send(ctx context.Context, msg types.MailMessage) error {
	f.sent <- msg
	return nil
}

func TestMailServiceProcessesQueue(t *testing.T) {
	sender := &fakeSender{sent: make(chan types.MailMessage, 1)}
	svc := NewMailService(1, sender)
	svc.Start()

	err := svc.Enqueue(types.MailMessage{To: "user@example.com", Subject: "Hello", Body: "Welcome"})
	if err != nil {
		t.Fatalf("enqueue returned error: %v", err)
	}

	select {
	case msg := <-sender.sent:
		if msg.To != "user@example.com" {
			t.Fatalf("expected recipient user@example.com, got %s", msg.To)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("expected queued mail to be processed")
	}
}
