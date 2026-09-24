package services

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"mime"
	"mime/quotedprintable"
	"net"
	"net/mail"
	"net/smtp"
	"os"
	"strings"
	"time"

	"mailservice/internal/types"
)

const (
	dialTimeout    = 10 * time.Second
	sessionTimeout = 60 * time.Second
)

// DefaultSMTPConfig returns the SMTP server from the SMTP_* environment variables,
// or nil when it is not configured. Keys without their own SMTP settings use it.
func DefaultSMTPConfig() *types.SMTPConfig {
	cfg := types.SMTPConfig{
		Host:     strings.TrimSpace(os.Getenv("SMTP_HOST")),
		Port:     strings.TrimSpace(os.Getenv("SMTP_PORT")),
		Username: os.Getenv("SMTP_USERNAME"),
		Password: os.Getenv("SMTP_PASSWORD"),
		From:     strings.TrimSpace(os.Getenv("SMTP_FROM")),
		ReplyTo:  strings.TrimSpace(os.Getenv("SMTP_REPLY_TO")),
	}
	if cfg.Host == "" || cfg.Port == "" || cfg.From == "" {
		return nil
	}
	return &cfg
}

// SMTPConfigured reports whether a default SMTP server is configured.
func SMTPConfigured() bool {
	return DefaultSMTPConfig() != nil
}

// SMTPMailer delivers mail through one SMTP server.
type SMTPMailer struct {
	cfg types.SMTPConfig
}

func NewSMTPMailerFromConfig(cfg types.SMTPConfig) *SMTPMailer {
	return &SMTPMailer{cfg: cfg}
}

// NewSMTPMailer returns a sender for the default SMTP server, or a NoopMailer that only
// logs when none is configured.
func NewSMTPMailer() MailSender {
	if cfg := DefaultSMTPConfig(); cfg != nil {
		return NewSMTPMailerFromConfig(*cfg)
	}
	return &NoopMailer{}
}

// Send delivers msg. Port 465 uses implicit TLS; other ports upgrade with STARTTLS when the
// server offers it. Credentials are only sent over an encrypted connection.
func (m *SMTPMailer) Send(ctx context.Context, msg types.MailMessage) error {
	recipient := msg.To
	if recipient == "" {
		recipient = msg.Target
	}
	if recipient == "" {
		return errors.New("mail recipient is required")
	}
	if strings.ContainsAny(recipient, "\r\n") {
		return errors.New("invalid recipient address")
	}

	cfg := m.cfg
	fromAddr, err := mail.ParseAddress(cfg.From)
	if err != nil {
		return fmt.Errorf("invalid From address %q: %w", cfg.From, err)
	}
	var replyTo *mail.Address
	if cfg.ReplyTo != "" {
		if replyTo, err = mail.ParseAddress(cfg.ReplyTo); err != nil {
			return fmt.Errorf("invalid Reply-To address %q: %w", cfg.ReplyTo, err)
		}
	}

	ctx, cancel := context.WithTimeout(ctx, sessionTimeout)
	defer cancel()

	addr := net.JoinHostPort(cfg.Host, cfg.Port)
	dialer := &net.Dialer{Timeout: dialTimeout}
	tlsConfig := &tls.Config{ServerName: cfg.Host, MinVersion: tls.VersionTLS12}

	var conn net.Conn
	if cfg.Port == "465" {
		conn, err = (&tls.Dialer{NetDialer: dialer, Config: tlsConfig}).DialContext(ctx, "tcp", addr)
	} else {
		conn, err = dialer.DialContext(ctx, "tcp", addr)
	}
	if err != nil {
		return fmt.Errorf("connect to %s: %w", addr, err)
	}
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}

	client, err := smtp.NewClient(conn, cfg.Host)
	if err != nil {
		conn.Close()
		return fmt.Errorf("smtp handshake with %s: %w", addr, err)
	}
	defer client.Close()

	if cfg.Port != "465" {
		if ok, _ := client.Extension("STARTTLS"); ok {
			if err := client.StartTLS(tlsConfig); err != nil {
				return fmt.Errorf("starttls: %w", err)
			}
		}
	}

	if cfg.Username != "" {
		if ok, _ := client.Extension("AUTH"); !ok {
			return errors.New("the SMTP server does not support authentication")
		}
		// PlainAuth refuses to send credentials over an unencrypted connection.
		if err := client.Auth(smtp.PlainAuth("", cfg.Username, cfg.Password, cfg.Host)); err != nil {
			return fmt.Errorf("smtp authentication failed: %w", err)
		}
	}

	if err := client.Mail(fromAddr.Address); err != nil {
		return fmt.Errorf("MAIL FROM rejected: %w", err)
	}
	if err := client.Rcpt(recipient); err != nil {
		return fmt.Errorf("recipient %s rejected: %w", recipient, err)
	}
	w, err := client.Data()
	if err != nil {
		return fmt.Errorf("DATA rejected: %w", err)
	}
	if _, err := w.Write(buildMessage(fromAddr, replyTo, recipient, msg.Subject, msg.Body)); err != nil {
		return fmt.Errorf("write message: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("message rejected: %w", err)
	}
	return client.Quit()
}

// buildMessage renders a plain-text UTF-8 message. Header values have line breaks removed
// so user input cannot inject extra headers. A nil replyTo omits the Reply-To header.
func buildMessage(from, replyTo *mail.Address, to, subject, body string) []byte {
	var buf bytes.Buffer
	header := func(k, v string) {
		buf.WriteString(k + ": " + stripCRLF(v) + "\r\n")
	}
	header("From", from.String())
	if replyTo != nil {
		header("Reply-To", replyTo.String())
	}
	header("To", to)
	header("Subject", mime.QEncoding.Encode("utf-8", stripCRLF(subject)))
	header("Date", time.Now().Format(time.RFC1123Z))
	header("Message-ID", messageID(from.Address))
	header("MIME-Version", "1.0")
	header("Content-Type", "text/plain; charset=UTF-8")
	header("Content-Transfer-Encoding", "quoted-printable")
	buf.WriteString("\r\n")

	qp := quotedprintable.NewWriter(&buf)
	_, _ = qp.Write([]byte(strings.ReplaceAll(body, "\n", "\r\n")))
	_ = qp.Close()
	buf.WriteString("\r\n")
	return buf.Bytes()
}

func stripCRLF(s string) string {
	return strings.NewReplacer("\r", " ", "\n", " ").Replace(s)
}

func messageID(from string) string {
	domain := "localhost"
	if at := strings.LastIndex(from, "@"); at >= 0 {
		domain = from[at+1:]
	}
	buf := make([]byte, 12)
	_, _ = rand.Read(buf)
	return "<" + hex.EncodeToString(buf) + "@" + domain + ">"
}

// SendTest delivers msg synchronously through cfg, for checking SMTP settings.
func SendTest(ctx context.Context, cfg types.SMTPConfig, msg types.MailMessage) error {
	return NewSMTPMailerFromConfig(cfg).Send(ctx, msg)
}
