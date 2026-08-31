package store

import (
	"context"
	"os"
	"testing"
	"time"

	"go.uber.org/mock/gomock"

	"github.com/truewebber/eve-online-mcp/internal/mocks"
)

func openTest(t *testing.T) *Store {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL is unset; run `make postgres` then `make migrate`")
	}
	ctx := context.Background()
	s, err := Open(ctx, dsn, mocks.QuietLogger(gomock.NewController(t)))
	if err != nil {
		t.Fatal(err)
	}
	release, err := s.HoldTestLock(ctx)
	if err != nil {
		s.Close()
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	t.Cleanup(release)
	if err := s.ResetTables(ctx); err != nil {
		t.Fatal(err)
	}

	return s
}

func TestMailLog(t *testing.T) {
	t.Parallel()
	s := openTest(t)
	ctx := context.Background()
	now := time.Now().UTC()
	const characterID int64 = 2112000001
	if err := s.InsertMail(ctx, characterID, now.Add(-2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := s.InsertMail(ctx, characterID, now); err != nil {
		t.Fatal(err)
	}
	n, err := s.CountMailSince(ctx, characterID, now.Add(-time.Hour))
	if err != nil || n != 1 {
		t.Fatalf("count %d %v", n, err)
	}
}
