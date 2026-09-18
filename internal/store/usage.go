package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// Request log statuses.
const (
	StatusAccepted      = "accepted"
	StatusLimitExceeded = "limit_exceeded"
	StatusIPDenied      = "ip_denied"
	StatusKeyDisabled   = "key_disabled"
)

type RequestLog struct {
	ID        int64     `json:"id"`
	APIKeyID  int64     `json:"api_key_id"`
	KeyName   string    `json:"key_name"`
	IP        string    `json:"ip"`
	Method    string    `json:"method"`
	Path      string    `json:"path"`
	UserAgent string    `json:"user_agent"`
	MailCount int       `json:"mail_count"`
	Status    string    `json:"status"`
	Message   string    `json:"message"`
	CreatedAt time.Time `json:"created_at"`
}

type window struct {
	name     string
	label    string
	span     string
	duration time.Duration
}

var windows = []window{
	{"hour", "Hourly", "hour", time.Hour},
	{"day", "Daily", "24 hours", 24 * time.Hour},
	{"week", "Weekly", "7 days", 7 * 24 * time.Hour},
	{"month", "Monthly", "30 days", 30 * 24 * time.Hour},
}

func (l Limits) get(name string) int {
	switch name {
	case "hour":
		return l.Hour
	case "day":
		return l.Day
	case "week":
		return l.Week
	default:
		return l.Month
	}
}

func (u Usage) get(name string) int {
	switch name {
	case "hour":
		return u.Hour
	case "day":
		return u.Day
	case "week":
		return u.Week
	default:
		return u.Month
	}
}

// LimitError explains which rate limit a request would exceed.
type LimitError struct {
	Window    string     `json:"window"`
	Limit     int        `json:"limit"`
	Used      int        `json:"used"`
	Requested int        `json:"requested"`
	RetryAt   *time.Time `json:"retry_at,omitempty"`
	label     string
	span      string
}

func (e *LimitError) Error() string {
	if e.Requested > e.Limit {
		return fmt.Sprintf("%s limit exceeded: this request contains %d mails but the limit is %d mails per %s; split it into smaller requests",
			e.label, e.Requested, e.Limit, e.span)
	}
	msg := fmt.Sprintf("%s limit exceeded: %d of %d mails already used in the last %s and this request needs %d more",
		e.label, e.Used, e.Limit, e.span, e.Requested)
	if e.RetryAt != nil {
		msg += ". Try again after " + e.RetryAt.UTC().Format(time.RFC3339)
	}
	return msg
}

// Reserve checks the key's limits for n mails and records the request. Checks and the
// insert run under a per-key lock so concurrent requests cannot overshoot a limit.
// Super keys are recorded but never limited. Returns *LimitError when a limit is hit.
func (s *Store) Reserve(ctx context.Context, key *APIKey, entry RequestLog, n int) error {
	entry.APIKeyID = key.ID
	entry.MailCount = n
	entry.Status = StatusAccepted

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if !key.IsSuper {
		if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, key.ID); err != nil {
			return err
		}

		var u Usage
		err := tx.QueryRowContext(ctx, `
			SELECT
				COALESCE(SUM(mail_count) FILTER (WHERE created_at > now() - interval '1 hour'), 0),
				COALESCE(SUM(mail_count) FILTER (WHERE created_at > now() - interval '24 hours'), 0),
				COALESCE(SUM(mail_count) FILTER (WHERE created_at > now() - interval '7 days'), 0),
				COALESCE(SUM(mail_count), 0)
			FROM request_logs
			WHERE api_key_id = $1 AND status = 'accepted' AND created_at > now() - interval '30 days'`,
			key.ID).Scan(&u.Hour, &u.Day, &u.Week, &u.Month)
		if err != nil {
			return err
		}

		for _, w := range windows {
			limit := key.Limits.get(w.name)
			used := u.get(w.name)
			if limit == 0 || used+n <= limit {
				continue
			}

			limitErr := &LimitError{Window: w.name, Limit: limit, Used: used, Requested: n, label: w.label, span: w.span}
			if n <= limit {
				retryAt, err := retryTime(ctx, tx, key.ID, w, used+n-limit)
				if err != nil {
					return err
				}
				limitErr.RetryAt = retryAt
			}

			entry.Status = StatusLimitExceeded
			entry.Message = limitErr.Error()
			if err := insertLog(ctx, tx, entry); err != nil {
				return err
			}
			if err := tx.Commit(); err != nil {
				return err
			}
			return limitErr
		}
	}

	if err := insertLog(ctx, tx, entry); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE api_keys SET last_used_at = now(), last_used_ip = $2 WHERE id = $1`,
		key.ID, entry.IP); err != nil {
		return err
	}
	return tx.Commit()
}

// retryTime finds when enough past usage leaves the rolling window to free `need` mails.
func retryTime(ctx context.Context, tx *sql.Tx, keyID int64, w window, need int) (*time.Time, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT created_at, mail_count FROM request_logs
		WHERE api_key_id = $1 AND status = 'accepted' AND created_at > now() - make_interval(secs => $2)
		ORDER BY created_at`,
		keyID, w.duration.Seconds())
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	freed := 0
	for rows.Next() {
		var at time.Time
		var count int
		if err := rows.Scan(&at, &count); err != nil {
			return nil, err
		}
		freed += count
		if freed >= need {
			retry := at.Add(w.duration)
			return &retry, nil
		}
	}
	return nil, rows.Err()
}

func insertLog(ctx context.Context, exec interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}, e RequestLog) error {
	_, err := exec.ExecContext(ctx, `
		INSERT INTO request_logs (api_key_id, ip, method, path, user_agent, mail_count, status, message)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		e.APIKeyID, e.IP, e.Method, e.Path, truncate(e.UserAgent, 300), e.MailCount, e.Status, e.Message)
	return err
}

// LogRequest records a request that was rejected before reaching a mail handler.
func (s *Store) LogRequest(ctx context.Context, e RequestLog) error {
	return insertLog(ctx, s.db, e)
}

// ListLogs returns the newest request logs, optionally for a single key.
func (s *Store) ListLogs(ctx context.Context, keyID int64, limit int) ([]RequestLog, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT l.id, l.api_key_id, k.name, l.ip, l.method, l.path, l.user_agent,
			l.mail_count, l.status, l.message, l.created_at
		FROM request_logs l
		JOIN api_keys k ON k.id = l.api_key_id
		WHERE $1 = 0 OR l.api_key_id = $1
		ORDER BY l.created_at DESC
		LIMIT $2`, keyID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	logs := []RequestLog{}
	for rows.Next() {
		var l RequestLog
		if err := rows.Scan(&l.ID, &l.APIKeyID, &l.KeyName, &l.IP, &l.Method, &l.Path, &l.UserAgent,
			&l.MailCount, &l.Status, &l.Message, &l.CreatedAt); err != nil {
			return nil, err
		}
		logs = append(logs, l)
	}
	return logs, rows.Err()
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}
