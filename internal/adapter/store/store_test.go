package store

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/truewebber/eve-online-mcp/internal/logtest"
)

func openTest(t *testing.T) *Store {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL is unset; run `make postgres` then `make migrate`")
	}
	ctx := context.Background()
	s, err := Open(ctx, dsn, logtest.Silent{})
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

func TestCreateUserAndGet(t *testing.T) {
	t.Parallel()
	s := openTest(t)
	ctx := context.Background()
	u, err := s.CreateUser(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if u.ID == "" {
		t.Fatalf("user %+v", u)
	}
	got, err := s.GetUser(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != u.ID {
		t.Fatalf("got %s", got.ID)
	}
	ok, err := s.UserExists(ctx, u.ID)
	if err != nil || !ok {
		t.Fatalf("exists %v %v", ok, err)
	}
	if _, err := s.GetUser(ctx, "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestGetOrCreateSecretStable(t *testing.T) {
	t.Parallel()
	s := openTest(t)
	ctx := context.Background()
	a, err := s.GetOrCreateSecret(ctx, "mcp_jwt_hmac")
	if err != nil || len(a) != SecretBytes {
		t.Fatalf("a %v %v", a, err)
	}
	b, err := s.GetOrCreateSecret(ctx, "mcp_jwt_hmac")
	if err != nil || string(a) != string(b) {
		t.Fatalf("unstable in same Open")
	}
	s2, err := Open(ctx, os.Getenv("DATABASE_URL"), logtest.Silent{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s2.Close)
	c, err := s2.GetOrCreateSecret(ctx, "mcp_jwt_hmac")
	if err != nil || string(a) != string(c) {
		t.Fatalf("unstable across Open")
	}
}

func TestMailLog(t *testing.T) {
	t.Parallel()
	s := openTest(t)
	ctx := context.Background()
	now := time.Now().UTC()
	if err := s.InsertMail(ctx, "u1", now.Add(-2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := s.InsertMail(ctx, "u1", now); err != nil {
		t.Fatal(err)
	}
	n, err := s.CountMailSince(ctx, "u1", now.Add(-time.Hour))
	if err != nil || n != 1 {
		t.Fatalf("count %d %v", n, err)
	}
}
