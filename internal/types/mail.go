package types

type MailMessage struct {
	To      string `json:"to"`
	Target  string `json:"target"`
	Subject string `json:"subject"`
	Body    string `json:"body"`
	// SMTP overrides the default SMTP server for this message. It is set from the
	// sending API key and never read from or written to JSON.
	SMTP *SMTPConfig `json:"-"`
	// DeliveryID is the mail's record in the mail history, or 0 when it is not recorded.
	DeliveryID int64 `json:"-"`
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

// Where a mail came from.
const (
	SourceAPI     = "api"     // an API client using its key
	SourceCompose = "compose" // the super user, from the admin console
	SourceSystem  = "system"  // the service itself, such as the key request auto-reply
)

// MailBatch is one message to one or more recipients. Each recipient gets a separate mail,
// and every mail is recorded with its delivery status.
type MailBatch struct {
	Source string
	// KeyID is the sending API key, or 0 for system mail.
	KeyID      int64
	KeyName    string
	Path       string // endpoint the mail was submitted through
	IP         string // client IP of that request
	Recipients []string
	Subject    string
	Body       string
	// SMTP is the key's own SMTP server, or nil for the default.
	SMTP *SMTPConfig
}
