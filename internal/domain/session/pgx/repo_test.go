package pgx

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"go.uber.org/mock/gomock"

	"github.com/truewebber/eve-online-mcp/internal/domain/character"
	characterpgx "github.com/truewebber/eve-online-mcp/internal/domain/character/pgx"
	"github.com/truewebber/eve-online-mcp/internal/domain/session"
	"github.com/truewebber/eve-online-mcp/internal/mocks"
	"github.com/truewebber/eve-online-mcp/internal/postgres"
	"github.com/truewebber/eve-online-mcp/internal/postgres/pgtest"
)

func openRepo(t *testing.T) (*Repo, character.Repository, *postgres.DB) {
	t.Helper()
	logger := mocks.QuietLogger(gomock.NewController(t))
	db := pgtest.Open(t, logger)

	return New(db.Pool()), characterpgx.New(db.Pool()), db
}

func seedCharacter(t *testing.T, chars character.Repository, id int64) {
	t.Helper()
	if err := chars.Upsert(context.Background(), character.Character{
		ID: id, Name: "Pilot", OwnerHash: "h",
	}); err != nil {
		t.Fatal(err)
	}
}

func TestCreateAndLiveByID(t *testing.T) {
	t.Parallel()
	repo, chars, _ := openRepo(t)
	ctx := context.Background()
	seedCharacter(t, chars, 11)
	got, err := repo.Create(ctx, session.Session{
		CharacterID: 11, RefreshToken: "rt", Scopes: []string{"s"},
		MCPClientID: "c", ClientName: "Cursor", IP: "127.0.0.1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.ID == 0 || got.RefreshToken != "rt" || got.ValidTil.Before(time.Now()) {
		t.Fatalf("got %+v", got)
	}
	live, err := repo.LiveByID(ctx, got.ID)
	if err != nil || live.CharacterID != 11 {
		t.Fatalf("live %+v err %v", live, err)
	}
}

func TestRevokeAllClearsTokens(t *testing.T) {
	t.Parallel()
	repo, chars, _ := openRepo(t)
	ctx := context.Background()
	seedCharacter(t, chars, 12)
	created, err := repo.Create(ctx, session.Session{
		CharacterID: 12, RefreshToken: "rt-live", Scopes: []string{},
		MCPClientID: "c",
	})
	if err != nil {
		t.Fatal(err)
	}
	revoked, err := repo.RevokeAllForCharacter(ctx, 12)
	if err != nil {
		t.Fatal(err)
	}
	if len(revoked.IDs) != 1 || revoked.IDs[0] != created.ID {
		t.Fatalf("ids %+v", revoked.IDs)
	}
	if len(revoked.Tokens) != 1 || revoked.Tokens[0] != "rt-live" {
		t.Fatalf("tokens %+v", revoked.Tokens)
	}
	if _, err := repo.LiveByID(ctx, created.ID); !errors.Is(err, session.ErrNotFound) {
		t.Fatalf("want dead, got %v", err)
	}
}

func TestExpiredUnrevokedDoesNotBlockCreate(t *testing.T) {
	t.Parallel()
	repo, chars, db := openRepo(t)
	ctx := context.Background()
	seedCharacter(t, chars, 13)
	old, err := repo.Create(ctx, session.Session{
		CharacterID: 13, RefreshToken: "old", Scopes: []string{},
		MCPClientID: "c",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool().Exec(ctx, `UPDATE sessions SET valid_til = now() - interval '1 day' WHERE id = $1`, old.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.LiveByID(ctx, old.ID); !errors.Is(err, session.ErrNotFound) {
		t.Fatalf("expired still live: %v", err)
	}
	revoked, err := repo.RevokeAllForCharacter(ctx, 13)
	if err != nil {
		t.Fatal(err)
	}
	if len(revoked.IDs) != 1 {
		t.Fatalf("want expired row revoked, got %+v", revoked)
	}
	next, err := repo.Create(ctx, session.Session{
		CharacterID: 13, RefreshToken: "new", Scopes: []string{},
		MCPClientID: "c",
	})
	if err != nil {
		t.Fatal(err)
	}
	if next.ID == old.ID {
		t.Fatal("replaced session reused id")
	}
}

func TestLockForRefreshSerializesAndRereads(t *testing.T) {
	t.Parallel()
	repo, chars, _ := openRepo(t)
	ctx := context.Background()
	seedCharacter(t, chars, 14)
	row, err := repo.Create(ctx, session.Session{
		CharacterID: 14, RefreshToken: "old", Scopes: []string{},
		MCPClientID: "c",
	})
	if err != nil {
		t.Fatal(err)
	}
	race := startRefreshLockRace(ctx, repo, row.ID)
	race.assertHeld(t)
	race.finish(t)
	live, err := repo.LiveByID(ctx, row.ID)
	if err != nil {
		t.Fatal(err)
	}
	if live.RefreshToken != "from-b" {
		t.Fatalf("token %s", live.RefreshToken)
	}
	race.assertOrder(t)
}

type refreshLockRace struct {
	started, release, doneB chan struct{}
	mu                      sync.Mutex
	order                   []string
	errc                    chan error
}

func startRefreshLockRace(ctx context.Context, repo *Repo, id int64) *refreshLockRace {
	r := &refreshLockRace{
		started: make(chan struct{}),
		release: make(chan struct{}),
		doneB:   make(chan struct{}),
		errc:    make(chan error, 2),
	}
	go func() {
		r.errc <- repo.LockForRefresh(ctx, id, func(_ string) (string, error) {
			r.mu.Lock()
			r.order = append(r.order, "a-start")
			r.mu.Unlock()
			close(r.started)
			<-r.release
			r.mu.Lock()
			r.order = append(r.order, "a-end")
			r.mu.Unlock()

			return "from-a", nil
		})
	}()
	<-r.started
	go func() {
		r.errc <- repo.LockForRefresh(ctx, id, func(tok string) (string, error) {
			r.mu.Lock()
			r.order = append(r.order, "b:"+tok)
			r.mu.Unlock()

			return "from-b", nil
		})
		close(r.doneB)
	}()

	return r
}

func (r *refreshLockRace) assertHeld(t *testing.T) {
	t.Helper()
	time.Sleep(80 * time.Millisecond)
	r.mu.Lock()
	for _, step := range r.order {
		if len(step) > 0 && step[0] == 'b' {
			r.mu.Unlock()
			t.Fatalf("b ran before a released: %v", r.order)
		}
	}
	snapshot := append([]string(nil), r.order...)
	r.mu.Unlock()
	if len(snapshot) != 1 || snapshot[0] != "a-start" {
		t.Fatalf("during lock: %v", snapshot)
	}
}

func (r *refreshLockRace) finish(t *testing.T) {
	t.Helper()
	close(r.release)
	select {
	case <-r.doneB:
	case <-time.After(5 * time.Second):
		t.Fatal("b blocked forever")
	}
	if err := <-r.errc; err != nil {
		t.Fatal(err)
	}
	if err := <-r.errc; err != nil {
		t.Fatal(err)
	}
}

func (r *refreshLockRace) assertOrder(t *testing.T) {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.order) != 3 || r.order[0] != "a-start" || r.order[1] != "a-end" || r.order[2] != "b:from-a" {
		t.Fatalf("order %v", r.order)
	}
}

const (
	sessionTokenSQL = `SELECT refresh_token IS NULL, revoked_at IS NOT NULL FROM sessions WHERE id = $1`
	ageValidTilSQL  = `UPDATE sessions SET valid_til = now() - interval '1 day' WHERE id = $1`
	ageRevokedSQL   = `UPDATE sessions SET revoked_at = now() - interval '91 days' WHERE id = $1`
	countSessions   = `SELECT COUNT(*) FROM sessions`
)

func TestExpireValidTil(t *testing.T) {
	t.Parallel()
	repo, chars, db := openRepo(t)
	ctx := context.Background()
	seedCharacter(t, chars, 15)
	seedCharacter(t, chars, 16)
	expired, err := repo.Create(ctx, session.Session{
		CharacterID: 15, RefreshToken: "rt-exp", Scopes: []string{}, MCPClientID: "c",
	})
	if err != nil {
		t.Fatal(err)
	}
	live, err := repo.Create(ctx, session.Session{
		CharacterID: 16, RefreshToken: "rt-ok", Scopes: []string{}, MCPClientID: "c",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool().Exec(ctx, ageValidTilSQL, expired.ID); err != nil {
		t.Fatal(err)
	}
	revoked, err := repo.ExpireValidTil(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(revoked.IDs) != 1 || revoked.IDs[0] != expired.ID {
		t.Fatalf("ids %+v", revoked.IDs)
	}
	if len(revoked.Tokens) != 1 || revoked.Tokens[0] != "rt-exp" {
		t.Fatalf("tokens %+v", revoked.Tokens)
	}
	var tokenGone, marked bool
	if err := db.Pool().QueryRow(ctx, sessionTokenSQL, expired.ID).Scan(&tokenGone, &marked); err != nil {
		t.Fatal(err)
	}
	if !tokenGone || !marked {
		t.Fatalf("expired row tokenGone=%v marked=%v", tokenGone, marked)
	}
	got, err := repo.LiveByID(ctx, live.ID)
	if err != nil || got.RefreshToken != "rt-ok" {
		t.Fatalf("live %+v err %v", got, err)
	}
}

func TestPurgeRevoked(t *testing.T) {
	t.Parallel()
	repo, chars, db := openRepo(t)
	ctx := context.Background()
	seedCharacter(t, chars, 17)
	seedCharacter(t, chars, 18)
	old, err := repo.Create(ctx, session.Session{
		CharacterID: 17, RefreshToken: "aged", Scopes: []string{}, MCPClientID: "c",
	})
	if err != nil {
		t.Fatal(err)
	}
	recent, err := repo.Create(ctx, session.Session{
		CharacterID: 18, RefreshToken: "new", Scopes: []string{}, MCPClientID: "c",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Revoke(ctx, old.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Revoke(ctx, recent.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool().Exec(ctx, ageRevokedSQL, old.ID); err != nil {
		t.Fatal(err)
	}
	n, err := repo.PurgeRevoked(ctx)
	if err != nil || n != 1 {
		t.Fatalf("purged %d %v", n, err)
	}
	var left int
	if err := db.Pool().QueryRow(ctx, countSessions).Scan(&left); err != nil {
		t.Fatal(err)
	}
	if left != 1 {
		t.Fatalf("left %d", left)
	}
}

func TestLockCharacterRequiresTx(t *testing.T) {
	t.Parallel()
	repo, _, _ := openRepo(t)
	if err := repo.LockCharacter(context.Background(), 1); !errors.Is(err, session.ErrNeedTx) {
		t.Fatalf("got %v", err)
	}
}
