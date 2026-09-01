package pgx

import (
	"context"
	"errors"
	"testing"

	"go.uber.org/mock/gomock"

	"github.com/truewebber/eve-online-mcp/internal/postgres/pgtest"

	"github.com/truewebber/eve-online-mcp/internal/domain/oauthclient"
	"github.com/truewebber/eve-online-mcp/internal/mocks"
)

const testRedirect = "http://localhost/cb"

func TestUpsertAndGet(t *testing.T) {
	t.Parallel()
	db := pgtest.Open(t, mocks.QuietLogger(gomock.NewController(t)))
	repo := New(db.Pool())
	ctx := context.Background()
	if err := repo.Upsert(ctx, oauthclient.Client{
		ID: "cid", RedirectURIs: []string{testRedirect},
	}); err != nil {
		t.Fatal(err)
	}
	got, err := repo.Get(ctx, "cid")
	if err != nil || len(got.RedirectURIs) != 1 || got.RedirectURIs[0] != testRedirect {
		t.Fatalf("%+v err %v", got, err)
	}
	if err := repo.Upsert(ctx, oauthclient.Client{
		ID: "cid", RedirectURIs: []string{"http://localhost/other"},
	}); err != nil {
		t.Fatal(err)
	}
	got, err = repo.Get(ctx, "cid")
	if err != nil || got.RedirectURIs[0] != "http://localhost/other" {
		t.Fatalf("update %+v err %v", got, err)
	}
	if _, err := repo.Get(ctx, "missing"); !errors.Is(err, oauthclient.ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

const (
	ageClientSQL  = `UPDATE oauth_clients SET created_at = now() - interval '31 days' WHERE client_id = $1`
	softAgeSQL    = `UPDATE oauth_clients SET deleted_at = now() - interval '31 days' WHERE client_id = $1`
	softRecentSQL = `UPDATE oauth_clients SET deleted_at = now() - interval '1 day' WHERE client_id = $1`
	insertCharSQL = `INSERT INTO characters (character_id, name, owner_hash) VALUES ($1, 'P', 'h')`
	insertSessSQL = `
		INSERT INTO sessions (character_id, refresh_token, scopes, mcp_client_id, valid_til)
		VALUES ($1, 'rt', '{}', $2, now() + interval '30 days')`
	countClientsSQL = `SELECT COUNT(*) FROM oauth_clients`
)

func TestSweepClients(t *testing.T) {
	t.Parallel()
	db := pgtest.Open(t, mocks.QuietLogger(gomock.NewController(t)))
	repo := New(db.Pool())
	ctx := context.Background()
	for _, id := range []string{"abandoned", "used", "young", "longgone", "softday"} {
		if err := repo.Upsert(ctx, oauthclient.Client{
			ID: id, RedirectURIs: []string{testRedirect},
		}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Pool().Exec(ctx, insertCharSQL, int64(21)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool().Exec(ctx, insertSessSQL, int64(21), "used"); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"abandoned", "used", "longgone", "softday"} {
		if _, err := db.Pool().Exec(ctx, ageClientSQL, id); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Pool().Exec(ctx, softAgeSQL, "longgone"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool().Exec(ctx, softRecentSQL, "softday"); err != nil {
		t.Fatal(err)
	}
	soft, err := repo.SoftDeleteAbandoned(ctx)
	if err != nil || soft != 1 {
		t.Fatalf("soft %d %v", soft, err)
	}
	if _, err := repo.Get(ctx, "abandoned"); !errors.Is(err, oauthclient.ErrNotFound) {
		t.Fatalf("abandoned still visible: %v", err)
	}
	if _, err := repo.Get(ctx, "used"); err != nil {
		t.Fatalf("used client gone: %v", err)
	}
	if _, err := repo.Get(ctx, "young"); err != nil {
		t.Fatalf("young client gone: %v", err)
	}
	hard, err := repo.DeleteLongSoftDeleted(ctx)
	if err != nil || hard != 1 {
		t.Fatalf("hard %d %v", hard, err)
	}
	var n int
	if err := db.Pool().QueryRow(ctx, countClientsSQL).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 4 {
		t.Fatalf("rows %d", n)
	}
}
