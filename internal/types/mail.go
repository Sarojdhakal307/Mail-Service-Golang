package types

type MailMessage struct {
	To      string `json:"to"`
	Target  string `json:"target"`
	Subject string `json:"subject"`
	Body    string `json:"body"`
	// SMTP overrides the default SMTP server for this message. It is set from the
	// sending API key and never read from or written to JSON.
	SMTP *SMTPConfig `json:"-"`
}

// SMTPConfig is a complete, decrypted SMTP server configuration.
type SMTPConfig struct {
	Host     string
	Port     string
	Username string
	Password string
	From     string
}

type SendRequest struct {
	Target  string `json:"target"`
	To      string `json:"to"`
	Subject string `json:"subject"`
	Body    string `json:"body"`
}

type BulkSendRequest struct {
	Recipients []string `json:"recipients"`
	Subject    string   `json:"subject"`
	Body       string   `json:"body"`
}

type TemplateSendRequest struct {
	Recipients []string            `json:"recipients"`
	Subject    string              `json:"subject"`
	Body       string              `json:"body"`
	Meta       map[string][]string `json:"meta"`
}
