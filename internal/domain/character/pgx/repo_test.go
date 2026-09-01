package pgx

import (
	"context"
	"errors"
	"sync"
	"testing"

	"go.uber.org/mock/gomock"

	"github.com/truewebber/eve-online-mcp/internal/postgres/pgtest"

	"github.com/truewebber/eve-online-mcp/internal/domain/character"
	"github.com/truewebber/eve-online-mcp/internal/mocks"
)

func openRepo(t *testing.T) *Repo {
	t.Helper()
	logger := mocks.QuietLogger(gomock.NewController(t))
	db := pgtest.Open(t, logger)

	return New(db.Pool())
}

func TestUpsertAndOwnerHash(t *testing.T) {
	t.Parallel()
	repo := openRepo(t)
	ctx := context.Background()
	row := character.Character{
		ID:        2112625428,
		Name:      "Jane Doe",
		OwnerHash: "hash-a",
	}
	if err := repo.Upsert(ctx, row); err != nil {
		t.Fatal(err)
	}
	got, err := repo.Get(ctx, row.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.OwnerHash != "hash-a" || !got.Live() {
		t.Fatalf("got %+v", got)
	}
	row.OwnerHash = "hash-b"
	if err := repo.Upsert(ctx, row); err != nil {
		t.Fatal(err)
	}
	got, err = repo.Get(ctx, row.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.OwnerHash != "hash-b" {
		t.Fatalf("got %+v", got)
	}
	if _, err := repo.Get(ctx, 1); !errors.Is(err, character.ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestConcurrentUpsert(t *testing.T) {
	t.Parallel()
	repo := openRepo(t)
	ctx := context.Background()
	id := int64(2112625429)
	var wg sync.WaitGroup
	errc := make(chan error, 2)
	for _, name := range []string{"pilot-a", "pilot-b"} {
		wg.Add(1)
		go func(name string) {
			defer wg.Done()
			errc <- repo.Upsert(ctx, character.Character{
				ID: id, Name: name, OwnerHash: "h",
			})
		}(name)
	}
	wg.Wait()
	close(errc)
	for err := range errc {
		if err != nil {
			t.Fatal(err)
		}
	}
	got, err := repo.Get(ctx, id)
	if err != nil || !got.Live() {
		t.Fatalf("got %+v %v", got, err)
	}
}

func TestDeleteSoft(t *testing.T) {
	t.Parallel()
	repo := openRepo(t)
	ctx := context.Background()
	if err := repo.Upsert(ctx, character.Character{
		ID: 7, Name: "X",
	}); err != nil {
		t.Fatal(err)
	}
	if err := repo.Delete(ctx, 7); err != nil {
		t.Fatal(err)
	}
	got, err := repo.Get(ctx, 7)
	if err != nil || got.Live() {
		t.Fatalf("want soft-deleted, got %+v %v", got, err)
	}
	if err := repo.Delete(ctx, 7); !errors.Is(err, character.ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
	if err := repo.Upsert(ctx, character.Character{
		ID: 7, Name: "X", OwnerHash: "h",
	}); err != nil {
		t.Fatal(err)
	}
	got, err = repo.Get(ctx, 7)
	if err != nil || !got.Live() {
		t.Fatalf("want revived, got %+v %v", got, err)
	}
}
