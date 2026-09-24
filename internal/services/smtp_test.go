package services

import (
	"bufio"
	"context"
	"net"
	"net/mail"
	"strings"
	"testing"
	"time"

	"mailservice/internal/types"
)

func TestBuildMessageBlocksHeaderInjection(t *testing.T) {
	from, _ := mail.ParseAddress("Acme <no-reply@example.com>")
	raw := string(buildMessage(from, nil, "ada@example.com", "Hi\r\nBcc: victim@example.com", "Hello"))
	head := raw[:strings.Index(raw, "\r\n\r\n")]
	for _, line := range strings.Split(head, "\r\n") {
		if strings.HasPrefix(strings.ToLower(line), "bcc:") {
			t.Fatalf("injected header found: %q", line)
		}
	}
	for _, want := range []string{"From: \"Acme\" <no-reply@example.com>", "To: ada@example.com", "Message-ID: <", "Content-Type: text/plain; charset=UTF-8"} {
		if !strings.Contains(head, want) {
			t.Errorf("missing header %q in:\n%s", want, head)
		}
	}
	if strings.Contains(head, "Reply-To:") {
		t.Errorf("unexpected Reply-To header in:\n%s", head)
	}
}

func TestBuildMessageAddsReplyTo(t *testing.T) {
	from, _ := mail.ParseAddress("noreply@example.com")
	replyTo, _ := mail.ParseAddress("Support <support@example.com>")
	raw := string(buildMessage(from, replyTo, "ada@example.com", "Hi", "Hello"))
	if !strings.Contains(raw, "\r\nReply-To: \"Support\" <support@example.com>\r\n") {
		t.Fatalf("missing Reply-To header:\n%s", raw)
	}
}

// fakeSMTP accepts one plain SMTP session (no TLS, no auth) and returns the received DATA.
func fakeSMTP(t *testing.T) (port string, data <-chan string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	out := make(chan string, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		conn.SetDeadline(time.Now().Add(5 * time.Second))
		r := bufio.NewReader(conn)
		write := func(s string) { conn.Write([]byte(s + "\r\n")) }
		write("220 fake ESMTP")
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				return
			}
			cmd := strings.ToUpper(strings.TrimSpace(line))
			switch {
			case strings.HasPrefix(cmd, "EHLO"):
				write("250 fake")
			case strings.HasPrefix(cmd, "MAIL FROM"), strings.HasPrefix(cmd, "RCPT TO"):
				write("250 ok")
			case cmd == "DATA":
				write("354 go ahead")
				var body strings.Builder
				for {
					l, err := r.ReadString('\n')
					if err != nil || l == ".\r\n" {
						break
					}
					body.WriteString(l)
				}
				out <- body.String()
				write("250 queued")
			case cmd == "QUIT":
				write("221 bye")
				return
			default:
				write("250 ok")
			}
		}
	}()
	_, p, _ := net.SplitHostPort(ln.Addr().String())
	return p, out
}

func TestSMTPMailerDeliversThroughConfiguredServer(t *testing.T) {
	port, data := fakeSMTP(t)
	cfg := types.SMTPConfig{Host: "127.0.0.1", Port: port, From: "Key Owner <owner@example.com>", ReplyTo: "help@example.com"}
	msg := types.MailMessage{To: "ada@example.com", Subject: "Héllo", Body: "Line one\nLine two"}

	if err := NewSMTPMailerFromConfig(cfg).Send(context.Background(), msg); err != nil {
		t.Fatalf("send: %v", err)
	}
	select {
	case got := <-data:
		if !strings.Contains(got, "From: \"Key Owner\" <owner@example.com>") || !strings.Contains(got, "Line one\r\nLine two") {
			t.Fatalf("unexpected message:\n%s", got)
		}
		if !strings.Contains(got, "Reply-To: <help@example.com>") {
			t.Fatalf("missing Reply-To:\n%s", got)
		}
		if !strings.Contains(got, "Subject: =?utf-8?q?H=C3=A9llo?=") {
			t.Fatalf("subject not encoded:\n%s", got)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no message received")
	}
}

func TestMailServiceUsesPerMessageSMTP(t *testing.T) {
	port, data := fakeSMTP(t)
	fallback := &fakeSender{sent: make(chan types.MailMessage, 1)}
	svc := NewMailService(1, fallback)
	svc.Start()

	cfg := &types.SMTPConfig{Host: "127.0.0.1", Port: port, From: "owner@example.com"}
	if err := svc.Enqueue(types.MailMessage{To: "ada@example.com", Body: "via key", SMTP: cfg}); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-data:
		if !strings.Contains(got, "via key") {
			t.Fatalf("unexpected message: %s", got)
		}
	case <-fallback.sent:
		t.Fatal("message went to the default sender instead of the key's SMTP server")
	case <-time.After(3 * time.Second):
		t.Fatal("no message received")
	}
}

func TestSMTPMailerRefusesCredentialsWithoutTLS(t *testing.T) {
	port, _ := fakeSMTP(t)
	cfg := types.SMTPConfig{Host: "127.0.0.1", Port: port, Username: "u", Password: "p", From: "owner@example.com"}
	err := NewSMTPMailerFromConfig(cfg).Send(context.Background(), types.MailMessage{To: "ada@example.com", Body: "x"})
	if err == nil {
		t.Fatal("expected an error when the server offers no AUTH over an unencrypted connection")
	}
}
