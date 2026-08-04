package types

type MailMessage struct {
	To      string `json:"to"`
	Target  string `json:"target"`
	Subject string `json:"subject"`
	Body    string `json:"body"`
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
	Recipients []string          `json:"recipients"`
	Subject    string            `json:"subject"`
	Body       string            `json:"body"`
	Meta       map[string][]string `json:"meta"`
}
