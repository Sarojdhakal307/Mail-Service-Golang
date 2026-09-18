package store

import (
	"context"
	"database/sql"
	"errors"
	"net/mail"
	"regexp"
	"strings"
	"time"
)

// phonePattern allows digits, spaces and common separators, with an optional leading +.
var phonePattern = regexp.MustCompile(`^\+?[0-9 ()./-]+$`)

// Key request statuses.
const (
	RequestPending  = "pending"
	RequestApproved = "approved"
	RequestRejected = "rejected"
)

var ErrAlreadyReviewed = errors.New("this request has already been reviewed")

// KeyRequest is an API key request submitted from the public site.
type KeyRequest struct {
	ID             int64      `json:"id"`
	Name           string     `json:"name"`
	Email          string     `json:"email"`
	Phone          string     `json:"phone"`
	Organization   string     `json:"organization"`
	Address        string     `json:"address"`
	UseCase        string     `json:"use_case"`
	ExpectedVolume string     `json:"expected_volume"`
	CallerIPs      string     `json:"caller_ips"`
	Message        string     `json:"message"`
	IP             string     `json:"ip"`
	UserAgent      string     `json:"user_agent"`
	Status         string     `json:"status"`
	APIKeyID       *int64     `json:"api_key_id"`
	CreatedAt      time.Time  `json:"created_at"`
	ReviewedAt     *time.Time `json:"reviewed_at"`
}

// KeyRequestInput holds the fields a visitor submits.
type KeyRequestInput struct {
	Name           string `json:"name"`
	Email          string `json:"email"`
	Phone          string `json:"phone"`
	Organization   string `json:"organization"`
	Address        string `json:"address"`
	UseCase        string `json:"use_case"`
	ExpectedVolume string `json:"expected_volume"`
	CallerIPs      string `json:"caller_ips"`
	Message        string `json:"message"`
}

func (in *KeyRequestInput) Normalize() error {
	fields := []struct {
		value    *string
		label    string
		max      int
		required bool
	}{
		{&in.Name, "name", 120, true},
		{&in.Email, "email", 254, true},
		{&in.Phone, "phone number", 30, true},
		{&in.Organization, "organization", 160, false},
		{&in.Address, "address", 300, false},
		{&in.UseCase, "use case", 2000, true},
		{&in.ExpectedVolume, "expected volume", 50, false},
		{&in.CallerIPs, "server IPs", 500, false},
		{&in.Message, "message", 5000, false},
	}
	for _, f := range fields {
		*f.value = strings.TrimSpace(*f.value)
		if f.required && *f.value == "" {
			return invalid("%s is required", f.label)
		}
		if len(*f.value) > f.max {
			return invalid("%s must be at most %d characters", f.label, f.max)
		}
	}
	addr, err := mail.ParseAddress(in.Email)
	if err != nil || addr.Address != in.Email {
		return invalid("enter a valid email address")
	}
	if !validPhone(in.Phone) {
		return invalid("enter a valid phone number, including the country code if you are outside the country")
	}
	if len(in.UseCase) < 20 {
		return invalid("please describe your use case in at least 20 characters")
	}
	return nil
}

func validPhone(phone string) bool {
	if !phonePattern.MatchString(phone) {
		return false
	}
	digits := 0
	for _, r := range phone {
		if r >= '0' && r <= '9' {
			digits++
		}
	}
	return digits >= 7 && digits <= 15
}

const requestColumns = `id, name, email, phone, organization, address, use_case, expected_volume,
	caller_ips, message, ip, user_agent, status, api_key_id, created_at, reviewed_at`

func scanRequest(row scanner) (*KeyRequest, error) {
	var r KeyRequest
	var keyID sql.NullInt64
	var reviewed sql.NullTime
	err := row.Scan(&r.ID, &r.Name, &r.Email, &r.Phone, &r.Organization, &r.Address, &r.UseCase, &r.ExpectedVolume,
		&r.CallerIPs, &r.Message, &r.IP, &r.UserAgent, &r.Status, &keyID, &r.CreatedAt, &reviewed)
	if err != nil {
		return nil, err
	}
	if keyID.Valid {
		r.APIKeyID = &keyID.Int64
	}
	if reviewed.Valid {
		r.ReviewedAt = &reviewed.Time
	}
	return &r, nil
}

func (s *Store) CreateKeyRequest(ctx context.Context, in KeyRequestInput, ip, userAgent string) (*KeyRequest, error) {
	if err := in.Normalize(); err != nil {
		return nil, err
	}
	return scanRequest(s.db.QueryRowContext(ctx, `
		INSERT INTO key_requests (name, email, phone, organization, address, use_case, expected_volume,
			caller_ips, message, ip, user_agent)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		RETURNING `+requestColumns,
		in.Name, in.Email, in.Phone, in.Organization, in.Address, in.UseCase, in.ExpectedVolume,
		in.CallerIPs, in.Message, ip, truncate(userAgent, 300)))
}

// ListKeyRequests returns the newest requests first.
func (s *Store) ListKeyRequests(ctx context.Context) ([]*KeyRequest, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+requestColumns+` FROM key_requests ORDER BY created_at DESC LIMIT 500`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []*KeyRequest{}
	for rows.Next() {
		r, err := scanRequest(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ApproveKeyRequest creates an API key for a pending request and links the two atomically.
func (s *Store) ApproveKeyRequest(ctx context.Context, id int64, in KeyInput, adminIP string) (*APIKey, string, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, "", err
	}
	defer tx.Rollback()

	if err := lockPendingRequest(ctx, tx, id); err != nil {
		return nil, "", err
	}
	key, raw, err := s.createKey(ctx, tx, in, adminIP)
	if err != nil {
		return nil, "", err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE key_requests SET status = $2, api_key_id = $3, reviewed_at = now() WHERE id = $1`,
		id, RequestApproved, key.ID); err != nil {
		return nil, "", err
	}
	if err := tx.Commit(); err != nil {
		return nil, "", err
	}
	return key, raw, nil
}

func (s *Store) RejectKeyRequest(ctx context.Context, id int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if err := lockPendingRequest(ctx, tx, id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE key_requests SET status = $2, reviewed_at = now() WHERE id = $1`, id, RequestRejected); err != nil {
		return err
	}
	return tx.Commit()
}

func lockPendingRequest(ctx context.Context, tx *sql.Tx, id int64) error {
	var status string
	err := tx.QueryRowContext(ctx, `SELECT status FROM key_requests WHERE id = $1 FOR UPDATE`, id).Scan(&status)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if status != RequestPending {
		return ErrAlreadyReviewed
	}
	return nil
}

// Ping reports whether the database is reachable.
func (s *Store) Ping(ctx context.Context) error {
	return s.db.PingContext(ctx)
}
