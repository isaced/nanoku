package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	entsql "entgo.io/ent/dialect/sql"
	_ "modernc.org/sqlite"
)

type DB struct {
	*Client
	conn *sql.DB
}

func OpenDB(path string) (*DB, error) {
	dsn := fmt.Sprintf("file:%s?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)", path)
	conn, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	// SQLite serializes writes (WAL gives us concurrent reads, but only
	// one writer at a time). With the default unlimited pool, several
	// goroutines that each grab a connection and try to write will
	// race on the writer mutex, and a long-running transaction holding
	// the writer starves every other writer behind a busy_timeout
	// retry. Capping the pool to a single connection funnels all
	// writes through the Go scheduler and lets the application see
	// serialization errors as real errors instead of as transient
	// "database is locked" noise.
	conn.SetMaxOpenConns(1)
	conn.SetMaxIdleConns(1)

	drv := entsql.OpenDB("sqlite3", conn)
	client := NewClient(Driver(drv))

	return &DB{Client: client, conn: conn}, nil
}

func (d *DB) Close() error {
	if err := d.Client.Close(); err != nil {
		return err
	}
	return d.conn.Close()
}

func (d *DB) Migrate(ctx context.Context) error {
	return d.Schema.Create(ctx)
}

var ErrNotFound = errors.New("not found")