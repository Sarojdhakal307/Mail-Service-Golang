package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/lib/pq"
)

const keyPrefix = "msk_"

// ErrNotRecoverable is returned for keys stored before encrypted copies were kept.
var ErrNotRecoverable = errors.New("this key was created before keys could be revealed; create a new key instead")

type APIKey struct {
	ID         int64    `json:"id"`
	Name       string   `json:"name"`
	Address    string   `json:"address"`
	KeyPrefix  string   `json:"key_prefix"`
	AllowedIPs []string `json:"allowed_ips"`
	IsSuper    bool     `json:"is_super"`
	// Recoverable is true when the raw key can be revealed to the super user.
	Recoverable bool       `json:"recoverable"`
	Limits      Limits     `json:"limits"`
	Active      bool       `json:"active"`
	CreatedIP   string     `json:"created_ip"`
	CreatedAt   time.Time  `json:"created_at"`
	LastUsedAt  *time.Time `json:"last_used_at"`
	LastUsedIP  *string    `json:"last_used_ip"`
	Usage       *Usage     `json:"usage,omitempty"`
}

// Limits are maximum mails per rolling window. Zero means unlimited.
type Limits struct {
	Hour  int `json:"hour"`
	Day   int `json:"day"`
	Week  int `json:"week"`
	Month int `json:"month"`
}

// Usage is the number of accepted mails in each rolling window.
type Usage struct {
	Hour  int `json:"hour"`
	Day   int `json:"day"`
	Week  int `json:"week"`
	Month int `json:"month"`
}

// KeyInput holds the editable fields of an API key.
type KeyInput struct {
	Name       string   `json:"name"`
	Address    string   `json:"address"`
	AllowedIPs []string `json:"allowed_ips"`
	IsSuper    bool     `json:"is_super"`
	Limits     Limits   `json:"limits"`
	Active     *bool    `json:"active"`
}

// ValidationError reports invalid key input.
type ValidationError struct{ msg string }

func (e *ValidationError) Error() string { return e.msg }

func invalid(format string, args ...any) error {
	return &ValidationError{msg: fmt.Sprintf(format, args...)}
}

// Normalize trims and validates the input. An empty IP list means all IPs ("*").
func (in *KeyInput) Normalize() error {
	in.Name = strings.TrimSpace(in.Name)
	in.Address = strings.TrimSpace(in.Address)
	if in.Name == "" {
		return invalid("name is required")
	}
	if len(in.Name) > 200 || len(in.Address) > 500 {
		return invalid("name or address is too long")
	}
	if in.Limits.Hour < 0 || in.Limits.Day < 0 || in.Limits.Week < 0 || in.Limits.Month < 0 {
		return invalid("limits cannot be negative")
	}

	ips := []string{}
	for _, entry := range in.AllowedIPs {
		entry = strings.TrimSpace(entry)
		switch {
		case entry == "":
			continue
		case entry == "*":
			in.AllowedIPs = []string{"*"}
			return nil
		case strings.Contains(entry, "/"):
			if _, _, err := net.ParseCIDR(entry); err != nil {
				return invalid("invalid CIDR %q", entry)
			}
		default:
			if net.ParseIP(entry) == nil {
				return invalid("invalid IP address %q", entry)
			}
		}
		ips = append(ips, entry)
	}
	if len(ips) == 0 {
		ips = []string{"*"}
	}
	in.AllowedIPs = ips
	return nil
}

// AllowsIP reports whether ip matches the key's allow list. Super keys allow every IP.
func (k *APIKey) AllowsIP(ip string) bool {
	if k.IsSuper {
		return true
	}
	parsed := net.ParseIP(ip)
	for _, entry := range k.AllowedIPs {
		if entry == "*" {
			return true
		}
		if parsed == nil {
			continue
		}
		if strings.Contains(entry, "/") {
			if _, network, err := net.ParseCIDR(entry); err == nil && network.Contains(parsed) {
				return true
			}
		} else if allowed := net.ParseIP(entry); allowed != nil && allowed.Equal(parsed) {
			return true
		}
	}
	return false
}

// HashKey returns the digest stored for a raw API key.
func HashKey(raw string) []byte {
	sum := sha256.Sum256([]byte(raw))
	return sum[:]
}

func generateKey() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return keyPrefix + hex.EncodeToString(buf), nil
}

func displayPrefix(raw string) string {
	if len(raw) > 12 {
		return raw[:12]
	}
	return raw
}

const keyColumns = `id, name, address, key_prefix, allowed_ips, is_super,
	limit_hour, limit_day, limit_week, limit_month, active, created_ip,
	created_at, last_used_at, last_used_ip, key_encrypted IS NOT NULL`

type scanner interface {
	Scan(dest ...any) error
}

func scanKey(row scanner) (*APIKey, error) {
	var k APIKey
	var lastUsed sql.NullTime
	var lastIP sql.NullString
	err := row.Scan(&k.ID, &k.Name, &k.Address, &k.KeyPrefix, pq.Array(&k.AllowedIPs), &k.IsSuper,
		&k.Limits.Hour, &k.Limits.Day, &k.Limits.Week, &k.Limits.Month, &k.Active, &k.CreatedIP,
		&k.CreatedAt, &lastUsed, &lastIP, &k.Recoverable)
	if err != nil {
		return nil, err
	}
	if lastUsed.Valid {
		k.LastUsedAt = &lastUsed.Time
	}
	if lastIP.Valid {
		k.LastUsedIP = &lastIP.String
	}
	return &k, nil
}

// CreateKey generates a new random key. The raw key is returned once and only its hash is stored.
func (s *Store) CreateKey(ctx context.Context, in KeyInput, createdIP string) (*APIKey, string, error) {
	return s.createKey(ctx, s.db, in, createdIP)
}

type rowQueryer interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func (s *Store) createKey(ctx context.Context, q rowQueryer, in KeyInput, createdIP string) (*APIKey, string, error) {
	if err := in.Normalize(); err != nil {
		return nil, "", err
	}
	raw, err := generateKey()
	if err != nil {
		return nil, "", err
	}
	hash := HashKey(raw)
	encrypted, err := s.cipher.Seal(raw, hash)
	if err != nil {
		return nil, "", err
	}
	active := true
	if in.Active != nil {
		active = *in.Active
	}

	row := q.QueryRowContext(ctx, `
		INSERT INTO api_keys (name, address, key_prefix, key_hash, allowed_ips, is_super,
			limit_hour, limit_day, limit_week, limit_month, active, created_ip, key_encrypted)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
		RETURNING `+keyColumns,
		in.Name, in.Address, displayPrefix(raw), hash, pq.Array(in.AllowedIPs), in.IsSuper,
		in.Limits.Hour, in.Limits.Day, in.Limits.Week, in.Limits.Month, active, createdIP, encrypted)
	key, err := scanKey(row)
	if err != nil {
		return nil, "", err
	}
	return key, raw, nil
}

// SyncConfigSuperKey makes raw the only active key that came from SUPER_API_KEY. Keys from
// earlier values are disabled, so changing or clearing SUPER_API_KEY revokes the old key.
// Pass "" to disable every configured super key. Returns how many old keys were disabled.
func (s *Store) SyncConfigSuperKey(ctx context.Context, raw string) (int64, error) {
	var current []byte
	if raw != "" {
		if err := s.EnsureSuperKey(ctx, raw); err != nil {
			return 0, err
		}
		current = HashKey(raw)
	}
	res, err := s.db.ExecContext(ctx, `
		UPDATE api_keys SET active = FALSE
		WHERE created_ip = 'config' AND active AND ($1::bytea IS NULL OR key_hash <> $1)`, current)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// EnsureSuperKey registers a key supplied through configuration as an active super key.
func (s *Store) EnsureSuperKey(ctx context.Context, raw string) error {
	hash := HashKey(raw)
	encrypted, err := s.cipher.Seal(raw, hash)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO api_keys (name, address, key_prefix, key_hash, allowed_ips, is_super, active, created_ip, key_encrypted)
		VALUES ('Super key (SUPER_API_KEY)', '', $1, $2, '{*}', TRUE, TRUE, 'config', $3)
		ON CONFLICT (key_hash) DO UPDATE SET is_super = TRUE, active = TRUE, key_encrypted = EXCLUDED.key_encrypted`,
		displayPrefix(raw), hash, encrypted)
	return err
}

// RevealKey decrypts and returns the raw API key.
func (s *Store) RevealKey(ctx context.Context, id int64) (string, error) {
	var hash, encrypted []byte
	err := s.db.QueryRowContext(ctx, `SELECT key_hash, key_encrypted FROM api_keys WHERE id = $1`, id).
		Scan(&hash, &encrypted)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", err
	}
	if encrypted == nil {
		return "", ErrNotRecoverable
	}
	return s.cipher.Open(encrypted, hash)
}

func (s *Store) FindKeyByHash(ctx context.Context, hash []byte) (*APIKey, error) {
	key, err := scanKey(s.db.QueryRowContext(ctx, `SELECT `+keyColumns+` FROM api_keys WHERE key_hash = $1`, hash))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return key, err
}

// ListKeys returns all keys with their current usage per window.
func (s *Store) ListKeys(ctx context.Context) ([]*APIKey, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+prefixed("k.", keyColumns)+`,
			COALESCE(SUM(l.mail_count) FILTER (WHERE l.created_at > now() - interval '1 hour'), 0),
			COALESCE(SUM(l.mail_count) FILTER (WHERE l.created_at > now() - interval '24 hours'), 0),
			COALESCE(SUM(l.mail_count) FILTER (WHERE l.created_at > now() - interval '7 days'), 0),
			COALESCE(SUM(l.mail_count), 0)
		FROM api_keys k
		LEFT JOIN request_logs l ON l.api_key_id = k.id AND l.status = 'accepted'
			AND l.created_at > now() - interval '30 days'
		GROUP BY k.id
		ORDER BY k.created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	keys := []*APIKey{}
	for rows.Next() {
		var u Usage
		var lastUsed sql.NullTime
		var lastIP sql.NullString
		k := &APIKey{}
		err := rows.Scan(&k.ID, &k.Name, &k.Address, &k.KeyPrefix, pq.Array(&k.AllowedIPs), &k.IsSuper,
			&k.Limits.Hour, &k.Limits.Day, &k.Limits.Week, &k.Limits.Month, &k.Active, &k.CreatedIP,
			&k.CreatedAt, &lastUsed, &lastIP, &k.Recoverable, &u.Hour, &u.Day, &u.Week, &u.Month)
		if err != nil {
			return nil, err
		}
		if lastUsed.Valid {
			k.LastUsedAt = &lastUsed.Time
		}
		if lastIP.Valid {
			k.LastUsedIP = &lastIP.String
		}
		k.Usage = &u
		keys = append(keys, k)
	}
	return keys, rows.Err()
}

func prefixed(prefix, columns string) string {
	parts := strings.Split(columns, ",")
	for i, p := range parts {
		parts[i] = prefix + strings.TrimSpace(p)
	}
	return strings.Join(parts, ", ")
}

func (s *Store) UpdateKey(ctx context.Context, id int64, in KeyInput) (*APIKey, error) {
	if err := in.Normalize(); err != nil {
		return nil, err
	}
	key, err := scanKey(s.db.QueryRowContext(ctx, `
		UPDATE api_keys SET name = $2, address = $3, allowed_ips = $4, is_super = $5,
			limit_hour = $6, limit_day = $7, limit_week = $8, limit_month = $9,
			active = COALESCE($10, active)
		WHERE id = $1
		RETURNING `+keyColumns,
		id, in.Name, in.Address, pq.Array(in.AllowedIPs), in.IsSuper,
		in.Limits.Hour, in.Limits.Day, in.Limits.Week, in.Limits.Month, in.Active))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return key, err
}

func (s *Store) SetKeyActive(ctx context.Context, id int64, active bool) error {
	res, err := s.db.ExecContext(ctx, `UPDATE api_keys SET active = $2 WHERE id = $1`, id, active)
	return notFoundIfNoRows(res, err)
}

func (s *Store) DeleteKey(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM api_keys WHERE id = $1`, id)
	return notFoundIfNoRows(res, err)
}

func notFoundIfNoRows(res sql.Result, err error) error {
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}
