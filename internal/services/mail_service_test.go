package services

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"mailservice/internal/store"
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

type fakeRecorder struct {
	mu       sync.Mutex
	recorded []store.MailRecord
	statuses map[int64][]string
	done     chan struct{}
}

func (f *fakeRecorder) RecordMail(ctx context.Context, m store.MailRecord, recipients []string) ([]int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.recorded = append(f.recorded, m)
	ids := make([]int64, len(recipients))
	for i := range ids {
		ids[i] = int64(i + 1)
	}
	return ids, nil
}

func (f *fakeRecorder) MarkDelivery(ctx context.Context, id int64, status, errMsg string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.statuses[id] = append(f.statuses[id], status)
	if status != store.MailSending {
		f.done <- struct{}{}
	}
	return nil
}

type failingSender struct{}

func (failingSender) Send(ctx context.Context, msg types.MailMessage) error {
	if msg.To == "bad@example.com" {
		return errors.New("550 mailbox unavailable")
	}
	return nil
}

func TestSubmitRecordsEachDelivery(t *testing.T) {
	rec := &fakeRecorder{statuses: map[int64][]string{}, done: make(chan struct{}, 2)}
	svc := NewMailService(1, failingSender{})
	svc.EnableRecording(rec, &types.SMTPConfig{Host: "smtp.example.com", Port: "587", From: "no-reply@example.com"})
	svc.Start()

	queued, err := svc.Submit(context.Background(), types.MailBatch{
		Source: types.SourceAPI, KeyID: 7, KeyName: "billing", Path: "/send/bulk",
		Recipients: []string{"ok@example.com", "bad@example.com"}, Subject: "Hi", Body: "Hello",
	})
	if err != nil || queued != 2 {
		t.Fatalf("submit: %d, %v", queued, err)
	}
	for i := 0; i < 2; i++ {
		select {
		case <-rec.done:
		case <-time.After(2 * time.Second):
			t.Fatal("expected both deliveries to finish")
		}
	}

	rec.mu.Lock()
	defer rec.mu.Unlock()
	if got := rec.recorded[0]; got.Sender != "no-reply@example.com" || got.SMTPHost != "smtp.example.com:587" || got.APIKeyID != 7 {
		t.Fatalf("unexpected record %+v", got)
	}
	if s := strings.Join(rec.statuses[1], ","); s != "sending,sent" {
		t.Fatalf("expected ok@ to be sent, got %s", s)
	}
	if s := strings.Join(rec.statuses[2], ","); s != "sending,failed" {
		t.Fatalf("expected bad@ to fail, got %s", s)
	}
}
