package pgx

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/truewebber/eve-online-mcp/internal/adapter/store"
	"github.com/truewebber/eve-online-mcp/internal/adapter/store/storetest"
	"github.com/truewebber/eve-online-mcp/internal/domain/character"
	"github.com/truewebber/eve-online-mcp/internal/logtest"
)

func openRepo(t *testing.T) (*store.Store, *Repo) {
	t.Helper()
	db := storetest.Open(t, logtest.Silent{})

	return db, New(db.Pool(), logtest.Silent{})
}

func TestOwnership(t *testing.T) {
	t.Parallel()
	db, repo := openRepo(t)
	ctx := context.Background()
	a, err := db.CreateUser(ctx)
	if err != nil {
		t.Fatal(err)
	}
	b, err := db.CreateUser(ctx)
	if err != nil {
		t.Fatal(err)
	}
	row := character.Character{
		ID:           2112625428,
		UserID:       a.ID,
		Name:         "Jane Doe",
		OwnerHash:    "hash",
		RefreshToken: "rt-1",
		Scopes:       []string{"esi-wallet.read_character_wallet.v1"},
	}
	if err := repo.Upsert(ctx, row); err != nil {
		t.Fatal(err)
	}
	got, err := repo.Get(ctx, row.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.UserID != a.ID || got.RefreshToken != "rt-1" {
		t.Fatalf("got %+v", got)
	}
	row.RefreshToken = "rt-2"
	if err := repo.Upsert(ctx, row); err != nil {
		t.Fatal(err)
	}
	got, err = repo.Get(ctx, row.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.RefreshToken != "rt-2" || got.UserID != a.ID {
		t.Fatalf("got %+v", got)
	}
	row.UserID = b.ID
	if err := repo.Upsert(ctx, row); !errors.Is(err, character.ErrOwned) {
		t.Fatalf("want ErrOwned, got %v", err)
	}
	list, err := repo.ListByUser(ctx, a.ID)
	if err != nil || len(list) != 1 {
		t.Fatalf("list %v %v", list, err)
	}
	if _, err := repo.Get(ctx, 1); !errors.Is(err, character.ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
	_, err = db.Pool().Exec(ctx, `
		INSERT INTO characters (character_id, user_id, name, refresh_token)
		VALUES ($1, $2, 'x', 'y')`, row.ID, b.ID)
	if err == nil {
		t.Fatal("duplicate character_id must fail at the database")
	}
}

func TestUpdateRefreshSerializes(t *testing.T) {
	t.Parallel()
	db, repo := openRepo(t)
	ctx := context.Background()
	u, err := db.CreateUser(ctx)
	if err != nil {
		t.Fatal(err)
	}
	id := int64(99)
	if err := repo.Upsert(ctx, character.Character{
		ID: id, UserID: u.ID, Name: "Lock", RefreshToken: "old",
	}); err != nil {
		t.Fatal(err)
	}

	started := make(chan struct{})
	release := make(chan struct{})
	var mu sync.Mutex
	var order []string
	errc := make(chan error, 2)

	go func() {
		errc <- repo.UpdateRefresh(ctx, id, func(_ string) (string, error) {
			mu.Lock()
			order = append(order, "a-start")
			mu.Unlock()
			close(started)
			<-release
			mu.Lock()
			order = append(order, "a-end")
			mu.Unlock()

			return "from-a", nil
		})
	}()
	<-started
	doneB := make(chan struct{})
	go func() {
		errc <- repo.UpdateRefresh(ctx, id, func(tok string) (string, error) {
			mu.Lock()
			order = append(order, "b:"+tok)
			mu.Unlock()

			return "from-b", nil
		})
		close(doneB)
	}()
	time.Sleep(80 * time.Millisecond)
	mu.Lock()
	for _, step := range order {
		if len(step) > 0 && step[0] == 'b' {
			mu.Unlock()
			t.Fatalf("b ran before a released: %v", order)
		}
	}
	snapshot := append([]string(nil), order...)
	mu.Unlock()
	if len(snapshot) != 1 || snapshot[0] != "a-start" {
		t.Fatalf("during lock: %v", snapshot)
	}
	close(release)
	select {
	case <-doneB:
	case <-time.After(5 * time.Second):
		t.Fatal("b blocked forever")
	}
	if err := <-errc; err != nil {
		t.Fatal(err)
	}
	if err := <-errc; err != nil {
		t.Fatal(err)
	}
	got, err := repo.Get(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if got.RefreshToken != "from-b" {
		t.Fatalf("token %s", got.RefreshToken)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(order) != 3 || order[0] != "a-start" || order[1] != "a-end" || order[2] != "b:from-a" {
		t.Fatalf("order %v", order)
	}
}

func TestDelete(t *testing.T) {
	t.Parallel()
	db, repo := openRepo(t)
	ctx := context.Background()
	u, err := db.CreateUser(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.Upsert(ctx, character.Character{
		ID: 7, UserID: u.ID, Name: "X", RefreshToken: "rt",
	}); err != nil {
		t.Fatal(err)
	}
	if err := repo.Delete(ctx, 7); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Get(ctx, 7); !errors.Is(err, character.ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
	if err := repo.Delete(ctx, 7); !errors.Is(err, character.ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}
