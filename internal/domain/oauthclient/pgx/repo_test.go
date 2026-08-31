package pgx

import (
	"context"
	"errors"
	"testing"

	"github.com/truewebber/eve-online-mcp/internal/adapter/store/storetest"
	"github.com/truewebber/eve-online-mcp/internal/domain/oauthclient"
	"github.com/truewebber/eve-online-mcp/internal/logtest"
)

func TestUpsertAndGet(t *testing.T) {
	t.Parallel()
	db := storetest.Open(t, logtest.Silent{})
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
