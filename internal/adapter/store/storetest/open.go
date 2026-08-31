package storetest

import (
	"context"
	"os"
	"testing"

	"github.com/truewebber/gopkg/log"

	"github.com/truewebber/eve-online-mcp/internal/adapter/store"
)

func Open(tb testing.TB, logger log.Logger) *store.Store {
	tb.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		tb.Skip("DATABASE_URL is unset; run `make postgres` then `make migrate`")
	}

	ctx := context.Background()
	s, err := store.Open(ctx, dsn, logger)
	if err != nil {
		tb.Fatal(err)
	}
	if err := apply(ctx, dsn); err != nil {
		s.Close()
		tb.Fatal(err)
	}
	release, err := s.HoldTestLock(ctx)
	if err != nil {
		s.Close()
		tb.Fatal(err)
	}
	tb.Cleanup(s.Close)
	tb.Cleanup(release)
	if err := s.ResetTables(ctx); err != nil {
		tb.Fatal(err)
	}

	return s
}
