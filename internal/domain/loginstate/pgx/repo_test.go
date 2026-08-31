package pgx

import (
	"context"
	"errors"
	"testing"

	"go.uber.org/mock/gomock"

	"github.com/truewebber/eve-online-mcp/internal/adapter/store"
	"github.com/truewebber/eve-online-mcp/internal/adapter/store/storetest"

	"github.com/truewebber/eve-online-mcp/internal/domain/loginstate"
	"github.com/truewebber/eve-online-mcp/internal/mocks"
)

func openRepo(t *testing.T) (*store.Store, *Repo) {
	t.Helper()
	db := storetest.Open(t, mocks.QuietLogger(gomock.NewController(t)))

	return db, New(db.Pool())
}

func TestPutGetTake(t *testing.T) {
	t.Parallel()
	db, repo := openRepo(t)
	ctx := context.Background()
	u, err := db.CreateUser(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.Put(ctx, loginstate.Login{
		State: "st", PKCEVerifier: "v", Kind: loginstate.KindAlt, UserID: u.ID, Scopes: []string{"s"},
	}); err != nil {
		t.Fatal(err)
	}
	got, err := repo.Get(ctx, "st")
	if err != nil || got.Kind != loginstate.KindAlt || got.UserID != u.ID {
		t.Fatalf("%+v err %v", got, err)
	}
	if err := repo.Put(ctx, loginstate.Login{
		State: "once", PKCEVerifier: "v2", Kind: loginstate.KindMCP,
	}); err != nil {
		t.Fatal(err)
	}
	got, err = repo.Take(ctx, "once")
	if err != nil || got.Kind != loginstate.KindMCP {
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
	_, repo := openRepo(t)
	ctx := context.Background()
	if err := repo.Put(ctx, loginstate.Login{
		State: "st", PKCEVerifier: "v", Kind: loginstate.KindMCP,
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
	_, repo := openRepo(t)
	ctx := context.Background()
	if err := repo.Put(ctx, loginstate.Login{
		State: "old", PKCEVerifier: "v", Kind: loginstate.KindMCP,
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
