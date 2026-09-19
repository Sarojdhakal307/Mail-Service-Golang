package store

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/lib/pq"
)

// Mail delivery statuses. A mail moves queued → sending → sent, failed or simulated.
// A failed mail can be queued again by the super user.
const (
	MailQueued    = "queued"
	MailSending   = "sending"
	MailSent      = "sent"
	MailFailed    = "failed"
	MailSimulated = "simulated" // no SMTP server configured, the mail was only logged
)

// ErrNotRetryable is returned when retrying a mail that has not failed.
var ErrNotRetryable = errors.New("only failed mails can be sent again")

// MailRecord describes a message sent to one or more recipients.
type MailRecord struct {
	APIKeyID int64 // 0 for system mail
	KeyName  string
	Source   string
	Path     string
	IP       string
	Sender   string // From address, empty in simulation mode
	SMTPHost string // host:port, empty in simulation mode
	Subject  string
	Body     string
}

// MailDelivery is one mail to one recipient, as shown in the mail history.
type MailDelivery struct {
	ID        int64      `json:"id"`
	MailID    int64      `json:"mail_id"`
	APIKeyID  *int64     `json:"api_key_id"`
	KeyName   string     `json:"key_name"`
	Source    string     `json:"source"`
	Path      string     `json:"path"`
	IP        string     `json:"ip"`
	Sender    string     `json:"sender"`
	SMTPHost  string     `json:"smtp_host"`
	Recipient string     `json:"recipient"`
	Subject   string     `json:"subject"`
	Body      string     `json:"body,omitempty"`
	Status    string     `json:"status"`
	Error     string     `json:"error"`
	Attempts  int        `json:"attempts"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
	SentAt    *time.Time `json:"sent_at"`
}

// MailFilter narrows the mail history. Zero values match everything.
type MailFilter struct {
	KeyID    int64
	Source   string
	Status   string
	Query    string // matched against recipient, sender and subject
	BeforeID int64  // for paging: only deliveries with a smaller id
	Limit    int
}

// RecordMail stores a message and one queued delivery per recipient, and returns the
// delivery ids in the same order as recipients.
func (s *Store) RecordMail(ctx context.Context, m MailRecord, recipients []string) ([]int64, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	var keyID sql.NullInt64
	if m.APIKeyID > 0 {
		keyID = sql.NullInt64{Int64: m.APIKeyID, Valid: true}
	}
	var mailID int64
	err = tx.QueryRowContext(ctx, `
		INSERT INTO mail_messages (api_key_id, key_name, source, path, ip, subject, body)
		VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING id`,
		keyID, m.KeyName, m.Source, m.Path, m.IP, m.Subject, m.Body).Scan(&mailID)
	if err != nil {
		return nil, err
	}

	rows, err := tx.QueryContext(ctx, `
		INSERT INTO mail_deliveries (mail_id, position, recipient, sender, smtp_host)
		SELECT $1, t.pos, t.recipient, $3, $4 FROM unnest($2::text[]) WITH ORDINALITY AS t(recipient, pos)
		RETURNING id, position`,
		mailID, pq.Array(recipients), m.Sender, m.SMTPHost)
	if err != nil {
		return nil, err
	}
	ids := make([]int64, len(recipients))
	for rows.Next() {
		var id int64
		var pos int
		if err := rows.Scan(&id, &pos); err != nil {
			rows.Close()
			return nil, err
		}
		ids[pos-1] = id
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return ids, tx.Commit()
}

// MarkDelivery records a status change. Moving to "sending" counts an attempt.
func (s *Store) MarkDelivery(ctx context.Context, id int64, status, errMsg string) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE mail_deliveries SET
			status = $2,
			error = $3,
			updated_at = now(),
			attempts = attempts + CASE WHEN $2 = 'sending' THEN 1 ELSE 0 END,
			sent_at = CASE WHEN $2 IN ('sent', 'simulated') THEN now() ELSE sent_at END
		WHERE id = $1`, id, status, truncate(errMsg, 2000))
	return err
}

// RequeueDelivery queues a failed mail again, with the sender and server it will now use.
// It returns ErrNotRetryable when the mail is not in the failed state.
func (s *Store) RequeueDelivery(ctx context.Context, id int64, sender, smtpHost string) error {
	res, err := s.db.ExecContext(ctx, `
		UPDATE mail_deliveries SET status = 'queued', error = '', sender = $2, smtp_host = $3, updated_at = now()
		WHERE id = $1 AND status = 'failed'`, id, sender, smtpHost)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil {
		return err
	} else if n == 0 {
		return ErrNotRetryable
	}
	return nil
}

// FailInterruptedDeliveries marks mails that were still queued or being sent as failed.
// The queue lives in memory, so they were lost when the service stopped.
func (s *Store) FailInterruptedDeliveries(ctx context.Context) (int64, error) {
	res, err := s.db.ExecContext(ctx, `
		UPDATE mail_deliveries SET status = 'failed', updated_at = now(),
			error = 'the service restarted before this mail was sent'
		WHERE status IN ('queued', 'sending')`)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

const deliveryColumns = `d.id, d.mail_id, m.api_key_id, m.key_name, m.source, m.path, m.ip, d.sender, d.smtp_host,
	d.recipient, m.subject, d.status, d.error, d.attempts, d.created_at, d.updated_at, d.sent_at`

func scanDelivery(row scanner, extra ...any) (*MailDelivery, error) {
	var d MailDelivery
	var keyID sql.NullInt64
	var sentAt sql.NullTime
	dest := []any{&d.ID, &d.MailID, &keyID, &d.KeyName, &d.Source, &d.Path, &d.IP, &d.Sender, &d.SMTPHost,
		&d.Recipient, &d.Subject, &d.Status, &d.Error, &d.Attempts, &d.CreatedAt, &d.UpdatedAt, &sentAt}
	if err := row.Scan(append(dest, extra...)...); err != nil {
		return nil, err
	}
	if keyID.Valid {
		d.APIKeyID = &keyID.Int64
	}
	if sentAt.Valid {
		d.SentAt = &sentAt.Time
	}
	return &d, nil
}

// where builds the WHERE clause shared by the list and the counts. Status is left out so
// the counts can show every status for the same key and search.
func (f MailFilter) where(args *[]any, withStatus bool) string {
	var conds []string
	add := func(cond string, v any) {
		*args = append(*args, v)
		conds = append(conds, strings.ReplaceAll(cond, "?", "$"+strconv.Itoa(len(*args))))
	}
	if f.KeyID > 0 {
		add("m.api_key_id = ?", f.KeyID)
	}
	if f.Source != "" {
		add("m.source = ?", f.Source)
	}
	if withStatus && f.Status != "" {
		add("d.status = ?", f.Status)
	}
	if q := strings.TrimSpace(f.Query); q != "" {
		add("(strpos(lower(d.recipient), lower(?)) > 0 OR strpos(lower(d.sender), lower(?)) > 0 OR strpos(lower(m.subject), lower(?)) > 0)", q)
	}
	if withStatus && f.BeforeID > 0 {
		add("d.id < ?", f.BeforeID)
	}
	if len(conds) == 0 {
		return ""
	}
	return " WHERE " + strings.Join(conds, " AND ")
}

// ListMails returns deliveries matching f, newest first, without their bodies.
func (s *Store) ListMails(ctx context.Context, f MailFilter) ([]*MailDelivery, error) {
	if f.Limit <= 0 || f.Limit > 500 {
		f.Limit = 100
	}
	args := []any{}
	where := f.where(&args, true)
	args = append(args, f.Limit)
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+deliveryColumns+`
		FROM mail_deliveries d JOIN mail_messages m ON m.id = d.mail_id`+where+`
		ORDER BY d.id DESC
		LIMIT $`+strconv.Itoa(len(args)), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []*MailDelivery{}
	for rows.Next() {
		d, err := scanDelivery(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// MailCounts returns the number of deliveries per status that match f, ignoring its
// status and paging.
func (s *Store) MailCounts(ctx context.Context, f MailFilter) (map[string]int, error) {
	args := []any{}
	rows, err := s.db.QueryContext(ctx, `
		SELECT d.status, count(*)
		FROM mail_deliveries d JOIN mail_messages m ON m.id = d.mail_id`+f.where(&args, false)+`
		GROUP BY d.status`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	counts := map[string]int{MailQueued: 0, MailSending: 0, MailSent: 0, MailFailed: 0, MailSimulated: 0}
	for rows.Next() {
		var status string
		var n int
		if err := rows.Scan(&status, &n); err != nil {
			return nil, err
		}
		counts[status] = n
	}
	return counts, rows.Err()
}

// GetMail returns one delivery with its message body.
func (s *Store) GetMail(ctx context.Context, id int64) (*MailDelivery, error) {
	var body string
	d, err := scanDelivery(s.db.QueryRowContext(ctx, `
		SELECT `+deliveryColumns+`, m.body
		FROM mail_deliveries d JOIN mail_messages m ON m.id = d.mail_id
		WHERE d.id = $1`, id), &body)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	d.Body = body
	return d, nil
}
