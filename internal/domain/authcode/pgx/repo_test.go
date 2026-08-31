package pgx

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/truewebber/eve-online-mcp/internal/adapter/store"
	"github.com/truewebber/eve-online-mcp/internal/adapter/store/storetest"
	"github.com/truewebber/eve-online-mcp/internal/domain/authcode"
	"github.com/truewebber/eve-online-mcp/internal/logtest"
)

func openRepo(t *testing.T) (*store.Store, *Repo) {
	t.Helper()
	db := storetest.Open(t, logtest.Silent{})

	return db, New(db.Pool())
}

func TestTakeOnce(t *testing.T) {
	t.Parallel()
	db, repo := openRepo(t)
	ctx := context.Background()
	u, err := db.CreateUser(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.Put(ctx, authcode.Code{
		Value: "abc", UserID: u.ID, MCPClientID: "c",
		RedirectURI: "http://localhost/cb", CodeChallenge: "ch",
		ExpiresAt: time.Now().Add(2 * time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	got, err := repo.Take(ctx, "abc")
	if err != nil || got.UserID != u.ID {
		t.Fatalf("take %+v err %v", got, err)
	}
	if _, err := repo.Take(ctx, "abc"); !errors.Is(err, authcode.ErrNotFound) {
		t.Fatalf("second take %v", err)
	}
}

func TestTakeExpired(t *testing.T) {
	t.Parallel()
	db, repo := openRepo(t)
	ctx := context.Background()
	u, err := db.CreateUser(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.Put(ctx, authcode.Code{
		Value: "old", UserID: u.ID, MCPClientID: "c",
		RedirectURI: "r", CodeChallenge: "h",
		ExpiresAt: time.Now().Add(-time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Take(ctx, "old"); !errors.Is(err, authcode.ErrNotFound) {
		t.Fatalf("expired take %v", err)
	}
}

func TestDeleteExpired(t *testing.T) {
	t.Parallel()
	db, repo := openRepo(t)
	ctx := context.Background()
	u, err := db.CreateUser(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.Put(ctx, authcode.Code{
		Value: "oldc", UserID: u.ID, MCPClientID: "c", RedirectURI: "r", CodeChallenge: "h",
		ExpiresAt: time.Now().Add(-time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	n, err := repo.DeleteExpired(ctx)
	if err != nil || n < 1 {
		t.Fatalf("purge %d %v", n, err)
	}
}
