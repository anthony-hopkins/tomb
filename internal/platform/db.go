package platform

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"sort"
	"time"

	// pgx's stdlib shim registers the "pgx" driver for database/sql. Using
	// database/sql keeps the driver swappable and keeps an ORM out of the
	// project (Principle I, research.md D7).
	_ "github.com/jackc/pgx/v5/stdlib"
)

//go:embed all:migrations
var migrationsFS embed.FS

// OpenDB opens the pool and verifies connectivity before returning.
func OpenDB(ctx context.Context, databaseURL string) (*sql.DB, error) {
	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}

	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(30 * time.Minute)

	pingCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := db.PingContext(pingCtx); err != nil {
		db.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}
	return db, nil
}

// Migrate applies every embedded migration in filename order, recording each in
// schema_migrations so re-running is a no-op.
//
// Deliberately minimal: forward-only, no down migrations, no external tool
// (Principle VII). Revisit if a migration ever needs a rollback path.
func Migrate(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			name       text        PRIMARY KEY,
			applied_at timestamptz NOT NULL DEFAULT now()
		)`); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}

	entries, err := fs.Glob(migrationsFS, "migrations/*.sql")
	if err != nil {
		return fmt.Errorf("list migrations: %w", err)
	}
	sort.Strings(entries)

	for _, path := range entries {
		var applied bool
		if err := db.QueryRowContext(ctx,
			`SELECT EXISTS (SELECT 1 FROM schema_migrations WHERE name = $1)`, path,
		).Scan(&applied); err != nil {
			return fmt.Errorf("check migration %s: %w", path, err)
		}
		if applied {
			continue
		}

		body, err := migrationsFS.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read migration %s: %w", path, err)
		}

		// One transaction per migration: a failure leaves no partial schema.
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("begin migration %s: %w", path, err)
		}
		if _, err := tx.ExecContext(ctx, string(body)); err != nil {
			tx.Rollback()
			return fmt.Errorf("apply migration %s: %w", path, err)
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO schema_migrations (name) VALUES ($1)`, path,
		); err != nil {
			tx.Rollback()
			return fmt.Errorf("record migration %s: %w", path, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit migration %s: %w", path, err)
		}
	}
	return nil
}
