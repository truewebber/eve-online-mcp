package session

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.uber.org/mock/gomock"

	"github.com/truewebber/eve-online-mcp/internal/adapter/esi"
	esihttp "github.com/truewebber/eve-online-mcp/internal/adapter/esi/http"
	"github.com/truewebber/eve-online-mcp/internal/adapter/sso"
	"github.com/truewebber/eve-online-mcp/internal/domain/character"
	characterpgx "github.com/truewebber/eve-online-mcp/internal/domain/character/pgx"
	confirmpgx "github.com/truewebber/eve-online-mcp/internal/domain/confirm/pgx"
	mutationpgx "github.com/truewebber/eve-online-mcp/internal/domain/mutation/pgx"
	dbsession "github.com/truewebber/eve-online-mcp/internal/domain/session"
	sessionpgx "github.com/truewebber/eve-online-mcp/internal/domain/session/pgx"
	"github.com/truewebber/eve-online-mcp/internal/mocks"
	"github.com/truewebber/eve-online-mcp/internal/postgres"
	"github.com/truewebber/eve-online-mcp/internal/postgres/pgtest"
)

func testRuntime(t *testing.T, db *postgres.DB, ssoClient sso.Client) *Session {
	t.Helper()
	logger := mocks.QuietLogger(gomock.NewController(t))
	pool := db.Pool()
	runtime, err := Open(Options{
		Characters: characterpgx.New(pool, logger),
		Sessions:   sessionpgx.New(pool, logger),
		Confirms:   confirmpgx.New(pool),
		Mutations:  mutationpgx.New(pool),
		ESI:        esihttp.New(esi.Options{}, nil, logger),
		SSO:        ssoClient,
		WithinTx: func(ctx context.Context, fn func(context.Context) error) error {
			return postgres.WithinTx(ctx, pool, fn)
		},
		Logger: logger,
	})
	if err != nil {
		t.Fatal(err)
	}

	return runtime
}

func TestConcurrentRefreshOneCCPCall(t *testing.T) {
	t.Parallel()
	logger := mocks.QuietLogger(gomock.NewController(t))
	db := pgtest.Open(t, logger)
	ctx := context.Background()
	const characterID int64 = 701
	if err := characterpgx.New(db.Pool(), logger).Upsert(ctx, character.Character{
		ID: characterID, Name: "P", OwnerHash: "h",
	}); err != nil {
		t.Fatal(err)
	}
	row, err := sessionpgx.New(db.Pool(), logger).Create(ctx, dbsession.Session{
		CharacterID: characterID, RefreshToken: "R", Scopes: []string{},
		MCPClientID: "c",
	})
	if err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int64
	ctrl := gomock.NewController(t)
	m := mocks.NewMockSSOClient(ctrl)
	m.EXPECT().AccessToken(gomock.Any(), "R").DoAndReturn(func(context.Context, string) (*sso.CharacterToken, error) {
		calls.Add(1)
		time.Sleep(80 * time.Millisecond)

		return &sso.CharacterToken{
			RefreshToken:    "R2",
			AccessToken:     "at",
			AccessExpiresAt: time.Now().Add(time.Hour),
		}, nil
	}).Times(1)
	runtime := testRuntime(t, db, m)
	sess := runtime.ForCharacter(int(characterID), row.ID)
	var wg sync.WaitGroup
	errc := make(chan error, 2)
	for range 2 {
		wg.Go(func() {
			_, err := sess.eveAccess(ctx)
			errc <- err
		})
	}
	wg.Wait()
	close(errc)
	for err := range errc {
		if err != nil {
			t.Fatal(err)
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("CCP exchanges %d", calls.Load())
	}
}

func TestRebuildGrantClearsAccess(t *testing.T) {
	t.Parallel()
	logger := mocks.QuietLogger(gomock.NewController(t))
	db := pgtest.Open(t, logger)
	ctx := context.Background()
	const characterID int64 = 702
	if err := characterpgx.New(db.Pool(), logger).Upsert(ctx, character.Character{
		ID: characterID, Name: "P", OwnerHash: "h",
	}); err != nil {
		t.Fatal(err)
	}
	repo := sessionpgx.New(db.Pool(), logger)
	a, err := repo.Create(ctx, dbsession.Session{
		CharacterID: characterID, RefreshToken: "ra", Scopes: []string{},
		MCPClientID: "c",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Revoke(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	b, err := repo.Create(ctx, dbsession.Session{
		CharacterID: characterID, RefreshToken: "rb", Scopes: []string{},
		MCPClientID: "c",
	})
	if err != nil {
		t.Fatal(err)
	}
	ctrl := gomock.NewController(t)
	m := mocks.NewMockSSOClient(ctrl)
	m.EXPECT().AccessToken(gomock.Any(), "rb").Return(&sso.CharacterToken{
		RefreshToken: "rb", AccessToken: "at-b", AccessExpiresAt: time.Now().Add(time.Hour),
	}, nil)
	runtime := testRuntime(t, db, m)
	sess := runtime.ForCharacter(int(characterID), a.ID)
	sess.grant.set(&sso.CharacterToken{
		RefreshToken: "ra", AccessToken: "at-a", AccessExpiresAt: time.Now().Add(time.Hour),
	})
	sess.RebuildGrant(b.ID)
	if sess.SessionID != b.ID {
		t.Fatalf("sid %d", sess.SessionID)
	}
	if sess.grant.live() != "" {
		t.Fatal("grant cache survived rebuild")
	}
	got, err := sess.eveAccess(ctx)
	if err != nil || got != "at-b" {
		t.Fatalf("access %q err %v", got, err)
	}
}

func TestInvalidGrantRevokesRequestingSID(t *testing.T) {
	t.Parallel()
	logger := mocks.QuietLogger(gomock.NewController(t))
	db := pgtest.Open(t, logger)
	ctx := context.Background()
	const characterID int64 = 703
	if err := characterpgx.New(db.Pool(), logger).Upsert(ctx, character.Character{
		ID: characterID, Name: "P", OwnerHash: "h",
	}); err != nil {
		t.Fatal(err)
	}
	row, err := sessionpgx.New(db.Pool(), logger).Create(ctx, dbsession.Session{
		CharacterID: characterID, RefreshToken: "dead", Scopes: []string{},
		MCPClientID: "c",
	})
	if err != nil {
		t.Fatal(err)
	}
	ctrl := gomock.NewController(t)
	m := mocks.NewMockSSOClient(ctrl)
	m.EXPECT().AccessToken(gomock.Any(), "dead").Return(nil, sso.ErrInvalidGrant)
	runtime := testRuntime(t, db, m)
	sess := runtime.ForCharacter(int(characterID), row.ID)
	if _, err := sess.eveAccess(ctx); err == nil {
		t.Fatal("want auth error")
	}
	if _, err := runtime.Sessions.LiveByID(ctx, row.ID); err == nil {
		t.Fatal("requesting sid must be revoked")
	}
}
