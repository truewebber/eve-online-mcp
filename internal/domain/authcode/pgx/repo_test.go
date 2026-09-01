package pgx

import (
	"context"
	"errors"
	"testing"
	"time"

	"go.uber.org/mock/gomock"

	"github.com/truewebber/eve-online-mcp/internal/postgres/pgtest"

	"github.com/truewebber/eve-online-mcp/internal/domain/authcode"
	"github.com/truewebber/eve-online-mcp/internal/mocks"
)

func openRepo(t *testing.T) *Repo {
	t.Helper()
	db := pgtest.Open(t, mocks.QuietLogger(gomock.NewController(t)))

	return New(db.Pool())
}

func TestTakeOnce(t *testing.T) {
	t.Parallel()
	repo := openRepo(t)
	ctx := context.Background()
	const characterID int64 = 2112000001
	if err := repo.Put(ctx, authcode.Code{
		Value: "abc", CharacterID: characterID, MCPClientID: "c",
		RedirectURI: "http://localhost/cb", CodeChallenge: "ch",
		ExpiresAt: time.Now().Add(2 * time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	got, err := repo.Take(ctx, "abc")
	if err != nil || got.CharacterID != characterID {
		t.Fatalf("take %+v err %v", got, err)
	}
	if _, err := repo.Take(ctx, "abc"); !errors.Is(err, authcode.ErrNotFound) {
		t.Fatalf("second take %v", err)
	}
}

func TestTakeExpired(t *testing.T) {
	t.Parallel()
	repo := openRepo(t)
	ctx := context.Background()
	if err := repo.Put(ctx, authcode.Code{
		Value: "old", CharacterID: 1, MCPClientID: "c",
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
	repo := openRepo(t)
	ctx := context.Background()
	if err := repo.Put(ctx, authcode.Code{
		Value: "oldc", CharacterID: 1, MCPClientID: "c", RedirectURI: "r", CodeChallenge: "h",
		ExpiresAt: time.Now().Add(-time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	n, err := repo.DeleteExpired(ctx)
	if err != nil || n < 1 {
		t.Fatalf("purge %d %v", n, err)
	}
}
