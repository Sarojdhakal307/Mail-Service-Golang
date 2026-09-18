package store

import (
	"context"
	"database/sql"
	_ "embed"
	"errors"
	"fmt"
	"log"
	"time"

	_ "github.com/lib/pq"
)

//go:embed schema.sql
var schema string

var ErrNotFound = errors.New("not found")

type Store struct {
	db     *sql.DB
	cipher *Cipher
}

// Open connects to Postgres, retrying while the database starts up, and applies the schema.
func Open(ctx context.Context, databaseURL string, c *Cipher) (*Store, error) {
	if c == nil {
		return nil, errors.New("an API key cipher is required")
	}
	if databaseURL == "" {
		return nil, errors.New("DATABASE_URL is not set")
	}

	db, err := sql.Open("postgres", databaseURL)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(20)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(30 * time.Minute)

	for attempt := 1; ; attempt++ {
		pingCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		err = db.PingContext(pingCtx)
		cancel()
		if err == nil {
			break
		}
		if attempt == 10 {
			db.Close()
			return nil, fmt.Errorf("connect to database: %w", err)
		}
		log.Printf("database not ready (attempt %d): %v", attempt, err)
		time.Sleep(2 * time.Second)
	}

	if _, err := db.ExecContext(ctx, schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("apply schema: %w", err)
	}
	return &Store{db: db, cipher: c}, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}
