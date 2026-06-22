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