package pgtest

import (
	"context"
	"os"
	"testing"

	"github.com/truewebber/gopkg/log"

	"github.com/truewebber/eve-online-mcp/internal/postgres"
)

func Open(tb testing.TB, logger log.Logger) *postgres.DB {
	tb.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		tb.Skip("DATABASE_URL is unset; run `make postgres` then `make migrate`")
	}

	ctx := context.Background()
	db, err := postgres.Open(ctx, dsn, logger)
	if err != nil {
		tb.Fatal(err)
	}
	if err := apply(ctx, dsn); err != nil {
		db.Close()
		tb.Fatal(err)
	}
	release, err := HoldTestLock(ctx, db, logger)
	if err != nil {
		db.Close()
		tb.Fatal(err)
	}
	tb.Cleanup(db.Close)
	tb.Cleanup(release)
	if err := ResetTables(ctx, db); err != nil {
		tb.Fatal(err)
	}

	return db
}
