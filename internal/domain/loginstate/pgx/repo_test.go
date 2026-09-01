package pgx

import (
	"context"
	"errors"
	"testing"

	"go.uber.org/mock/gomock"

	"github.com/truewebber/eve-online-mcp/internal/postgres/pgtest"

	"github.com/truewebber/eve-online-mcp/internal/domain/loginstate"
	"github.com/truewebber/eve-online-mcp/internal/mocks"
)

func openRepo(t *testing.T) *Repo {
	t.Helper()
	db := pgtest.Open(t, mocks.QuietLogger(gomock.NewController(t)))

	return New(db.Pool())
}

func TestPutGetTake(t *testing.T) {
	t.Parallel()
	repo := openRepo(t)
	ctx := context.Background()
	if err := repo.Put(ctx, loginstate.Login{
		State: "st", PKCEVerifier: "v", Scopes: []string{"s"},
	}); err != nil {
		t.Fatal(err)
	}
	got, err := repo.Get(ctx, "st")
	if err != nil || got.PKCEVerifier != "v" {
		t.Fatalf("%+v err %v", got, err)
	}
	if err := repo.Put(ctx, loginstate.Login{
		State: "once", PKCEVerifier: "v2",
	}); err != nil {
		t.Fatal(err)
	}
	got, err = repo.Take(ctx, "once")
	if err != nil || got.PKCEVerifier != "v2" {
		t.Fatalf("take %+v err %v", got, err)
	}
	if _, err := repo.Take(ctx, "once"); !errors.Is(err, loginstate.ErrNotFound) {
		t.Fatalf("second take %v", err)
	}
	if _, err := repo.Get(ctx, "missing"); !errors.Is(err, loginstate.ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestGetExpires(t *testing.T) {
	t.Parallel()
	repo := openRepo(t)
	ctx := context.Background()
	if err := repo.Put(ctx, loginstate.Login{
		State: "st", PKCEVerifier: "v",
	}); err != nil {
		t.Fatal(err)
	}
	_, err := repo.pool.Exec(ctx, `UPDATE login_states SET created_at = now() - interval '20 minutes' WHERE state = 'st'`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Get(ctx, "st"); !errors.Is(err, loginstate.ErrNotFound) {
		t.Fatalf("expired login still visible: %v", err)
	}
}

func TestDeleteExpired(t *testing.T) {
	t.Parallel()
	repo := openRepo(t)
	ctx := context.Background()
	if err := repo.Put(ctx, loginstate.Login{
		State: "old", PKCEVerifier: "v",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.pool.Exec(ctx, `UPDATE login_states SET created_at = now() - interval '20 minutes' WHERE state = 'old'`); err != nil {
		t.Fatal(err)
	}
	n, err := repo.DeleteExpired(ctx)
	if err != nil || n < 1 {
		t.Fatalf("purge %d %v", n, err)
	}
}
