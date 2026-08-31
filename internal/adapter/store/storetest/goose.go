package storetest

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	_ "github.com/jackc/pgx/v5/stdlib" // Registers the pgx database/sql driver.
	"github.com/pressly/goose/v3"
)

var (
	errSQLNotDir  = errors.New("storetest: sql is not a directory")
	errNoRepoRoot = errors.New("storetest: repo root not found")
)

const (
	lockApply   = `SELECT pg_advisory_lock($1)`
	unlockApply = `SELECT pg_advisory_unlock($1)`
)

const applyAdvisoryKey int64 = 87265002

func Apply(ctx context.Context, databaseURL string) error {
	return apply(ctx, databaseURL)
}

func apply(ctx context.Context, databaseURL string) error {
	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		return fmt.Errorf("storetest: goose open: %w", err)
	}
	err = applyLocked(ctx, db)
	if cerr := db.Close(); cerr != nil && err == nil {
		return fmt.Errorf("storetest: goose close: %w", cerr)
	}

	return err
}

func applyLocked(ctx context.Context, db *sql.DB) error {
	conn, err := db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("storetest: goose lock: %w", err)
	}
	if _, err := conn.ExecContext(ctx, lockApply, applyAdvisoryKey); err != nil {
		if cerr := conn.Close(); cerr != nil {
			return fmt.Errorf("storetest: goose lock: %w", errors.Join(err, cerr))
		}

		return fmt.Errorf("storetest: goose lock: %w", err)
	}

	err = up(ctx, db)
	_, uerr := conn.ExecContext(context.WithoutCancel(ctx), unlockApply, applyAdvisoryKey)
	if cerr := conn.Close(); err == nil {
		if uerr != nil {
			return fmt.Errorf("storetest: goose unlock: %w", uerr)
		}
		if cerr != nil {
			return fmt.Errorf("storetest: goose lock close: %w", cerr)
		}
	}

	return err
}

func up(ctx context.Context, db *sql.DB) error {
	fsys, err := migrationFS()
	if err != nil {
		return err
	}
	provider, err := goose.NewProvider(goose.DialectPostgres, db, fsys)
	if err != nil {
		return fmt.Errorf("storetest: goose provider: %w", err)
	}
	if _, err := provider.Up(ctx); err != nil {
		return fmt.Errorf("storetest: goose up: %w", err)
	}

	return nil
}

func migrationFS() (fs.FS, error) {
	dir, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("storetest: wd: %w", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err != nil {
			parent := filepath.Dir(dir)
			if parent == dir {
				return nil, fmt.Errorf("%w: %s", errNoRepoRoot, dir)
			}
			dir = parent

			continue
		}
		sqlDir := filepath.Join(dir, "sql")
		st, err := os.Stat(sqlDir)
		if err != nil {
			return nil, fmt.Errorf("storetest: sql: %w", err)
		}
		if !st.IsDir() {
			return nil, errSQLNotDir
		}

		return os.DirFS(sqlDir), nil
	}
}
