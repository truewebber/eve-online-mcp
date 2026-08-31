package pgx

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"go.uber.org/mock/gomock"

	"github.com/truewebber/eve-online-mcp/internal/adapter/store/storetest"

	"github.com/truewebber/eve-online-mcp/internal/domain/character"
	"github.com/truewebber/eve-online-mcp/internal/mocks"
)

const testRefreshTwo = "rt-2"

func openRepo(t *testing.T) *Repo {
	t.Helper()
	logger := mocks.QuietLogger(gomock.NewController(t))
	db := storetest.Open(t, logger)

	return New(db.Pool(), logger)
}

func TestUpsertAndOwnerHash(t *testing.T) {
	t.Parallel()
	repo := openRepo(t)
	ctx := context.Background()
	row := character.Character{
		ID:           2112625428,
		Name:         "Jane Doe",
		OwnerHash:    "hash-a",
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
	if got.RefreshToken != "rt-1" || got.OwnerHash != "hash-a" || !got.Live() {
		t.Fatalf("got %+v", got)
	}
	row.RefreshToken = testRefreshTwo
	row.OwnerHash = "hash-b"
	if err := repo.Upsert(ctx, row); err != nil {
		t.Fatal(err)
	}
	got, err = repo.Get(ctx, row.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.RefreshToken != testRefreshTwo || got.OwnerHash != "hash-b" {
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
	for i, tok := range []string{"rt-a", "rt-b"} {
		wg.Add(1)
		go func(name, refresh string) {
			defer wg.Done()
			errc <- repo.Upsert(ctx, character.Character{
				ID: id, Name: name, OwnerHash: "h", RefreshToken: refresh,
			})
		}("pilot", tok)
		_ = i
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

func TestUpdateRefreshSerializes(t *testing.T) {
	t.Parallel()
	repo := openRepo(t)
	ctx := context.Background()
	id := int64(99)
	if err := repo.Upsert(ctx, character.Character{
		ID: id, Name: "Lock", RefreshToken: "old",
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

func TestDeleteSoft(t *testing.T) {
	t.Parallel()
	repo := openRepo(t)
	ctx := context.Background()
	if err := repo.Upsert(ctx, character.Character{
		ID: 7, Name: "X", RefreshToken: "rt",
	}); err != nil {
		t.Fatal(err)
	}
	if err := repo.Delete(ctx, 7); err != nil {
		t.Fatal(err)
	}
	got, err := repo.Get(ctx, 7)
	if err != nil || got.Live() || got.RefreshToken != "" {
		t.Fatalf("want soft-deleted empty grant, got %+v %v", got, err)
	}
	if err := repo.Delete(ctx, 7); !errors.Is(err, character.ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
	if err := repo.Upsert(ctx, character.Character{
		ID: 7, Name: "X", RefreshToken: testRefreshTwo, OwnerHash: "h",
	}); err != nil {
		t.Fatal(err)
	}
	got, err = repo.Get(ctx, 7)
	if err != nil || !got.Live() || got.RefreshToken != testRefreshTwo {
		t.Fatalf("want revived, got %+v %v", got, err)
	}
}
