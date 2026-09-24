package store

import (
	"net/mail"
	"strconv"
	"strings"

	"mailservice/internal/types"
)

// SMTPSettings is the SMTP server assigned to a key, as shown to the admin.
// The password itself is never exposed; HasPassword says whether one is stored.
type SMTPSettings struct {
	Host        string `json:"host"`
	Port        string `json:"port"`
	Username    string `json:"username"`
	From        string `json:"from"`
	ReplyTo     string `json:"reply_to"`
	HasPassword bool   `json:"has_password"`
}

// SMTPInput is the SMTP part of a create or update. A nil input or an empty host
// removes the key's SMTP server so it uses the default. On update, an empty password
// keeps the stored one; clearing the username also clears the password.
type SMTPInput struct {
	Host     string `json:"host"`
	Port     string `json:"port"`
	Username string `json:"username"`
	Password string `json:"password"`
	From     string `json:"from"`
	ReplyTo  string `json:"reply_to"`
}

// enabled reports whether the input assigns an SMTP server.
func (in *SMTPInput) enabled() bool {
	return in != nil && in.Host != ""
}

func (in *SMTPInput) normalize() error {
	if in == nil {
		return nil
	}
	in.Host = strings.TrimSpace(in.Host)
	in.Port = strings.TrimSpace(in.Port)
	in.Username = strings.TrimSpace(in.Username)
	in.From = strings.TrimSpace(in.From)
	in.ReplyTo = strings.TrimSpace(in.ReplyTo)
	if in.Host == "" {
		*in = SMTPInput{}
		return nil
	}

	if len(in.Host) > 253 || strings.ContainsAny(in.Host, " /:@") {
		return invalid("SMTP host must be a host name such as smtp.example.com")
	}
	if in.Port == "" {
		in.Port = "587"
	}
	if port, err := strconv.Atoi(in.Port); err != nil || port < 1 || port > 65535 {
		return invalid("SMTP port must be a number between 1 and 65535")
	}
	if len(in.Username) > 320 || len(in.Password) > 1024 {
		return invalid("SMTP username or password is too long")
	}
	if in.From == "" {
		return invalid("SMTP From address is required")
	}
	if _, err := mail.ParseAddress(in.From); err != nil || len(in.From) > 320 {
		return invalid("SMTP From must be an email address, e.g. no-reply@example.com or Acme <no-reply@example.com>")
	}
	if in.ReplyTo != "" {
		if _, err := mail.ParseAddress(in.ReplyTo); err != nil || len(in.ReplyTo) > 320 {
			return invalid("SMTP Reply-To must be an email address, e.g. support@example.com")
		}
	}
	if in.Username == "" {
		in.Password = ""
	}
	return nil
}

// smtpAAD binds an encrypted SMTP password to its key, separately from the key itself.
func smtpAAD(keyHash []byte) []byte {
	return append([]byte("smtp-password:"), keyHash...)
}

// SMTPConfig returns the key's decrypted SMTP server, or nil when the key uses the default.
func (s *Store) SMTPConfig(key *APIKey) (*types.SMTPConfig, error) {
	if key.SMTP == nil {
		return nil, nil
	}
	cfg := &types.SMTPConfig{
		Host:     key.SMTP.Host,
		Port:     key.SMTP.Port,
		Username: key.SMTP.Username,
		From:     key.SMTP.From,
		ReplyTo:  key.SMTP.ReplyTo,
	}
	if len(key.smtpPassword) > 0 {
		password, err := s.cipher.Open(key.smtpPassword, smtpAAD(key.keyHash))
		if err != nil {
			return nil, err
		}
		cfg.Password = password
	}
	return cfg, nil
}
