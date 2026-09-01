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

func TestUpsertAndGet(t *testing.T) {
	t.Parallel()
	db := pgtest.Open(t, mocks.QuietLogger(gomock.NewController(t)))
	repo := New(db.Pool())
	ctx := context.Background()
	if err := repo.Upsert(ctx, oauthclient.Client{
		ID: "cid", RedirectURIs: []string{"http://localhost/cb"},
	}); err != nil {
		t.Fatal(err)
	}
	got, err := repo.Get(ctx, "cid")
	if err != nil || len(got.RedirectURIs) != 1 || got.RedirectURIs[0] != "http://localhost/cb" {
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
