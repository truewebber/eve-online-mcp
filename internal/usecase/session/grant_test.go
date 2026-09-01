package session

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.uber.org/mock/gomock"

	"github.com/truewebber/eve-online-mcp/internal/adapter/esi"
	"github.com/truewebber/eve-online-mcp/internal/adapter/sso"
	"github.com/truewebber/eve-online-mcp/internal/domain/character"
	characterpgx "github.com/truewebber/eve-online-mcp/internal/domain/character/pgx"
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
	runtime, err := Open(pgxOptions(pool, testESIClient(t, logger), ssoClient, logger))
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
	if err := characterpgx.New(db.Pool()).Upsert(ctx, character.Character{
		ID: characterID, Name: "P", OwnerHash: "h",
	}); err != nil {
		t.Fatal(err)
	}
	row, err := sessionpgx.New(db.Pool()).Create(ctx, dbsession.Session{
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
	if err := characterpgx.New(db.Pool()).Upsert(ctx, character.Character{
		ID: characterID, Name: "P", OwnerHash: "h",
	}); err != nil {
		t.Fatal(err)
	}
	repo := sessionpgx.New(db.Pool())
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
	if err := characterpgx.New(db.Pool()).Upsert(ctx, character.Character{
		ID: characterID, Name: "P", OwnerHash: "h",
	}); err != nil {
		t.Fatal(err)
	}
	row, err := sessionpgx.New(db.Pool()).Create(ctx, dbsession.Session{
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

func TestInvalidGrantLeavesSiblingLive(t *testing.T) {
	t.Parallel()
	logger := mocks.QuietLogger(gomock.NewController(t))
	db := pgtest.Open(t, logger)
	ctx := context.Background()
	chars := characterpgx.New(db.Pool())
	repo := sessionpgx.New(db.Pool())
	if err := chars.Upsert(ctx, character.Character{ID: 705, Name: "A", OwnerHash: "h"}); err != nil {
		t.Fatal(err)
	}
	if err := chars.Upsert(ctx, character.Character{ID: 706, Name: "B", OwnerHash: "h"}); err != nil {
		t.Fatal(err)
	}
	dead, err := repo.Create(ctx, dbsession.Session{
		CharacterID: 705, RefreshToken: "dead", Scopes: []string{}, MCPClientID: "c",
	})
	if err != nil {
		t.Fatal(err)
	}
	live, err := repo.Create(ctx, dbsession.Session{
		CharacterID: 706, RefreshToken: "ok", Scopes: []string{}, MCPClientID: "c",
	})
	if err != nil {
		t.Fatal(err)
	}
	ctrl := gomock.NewController(t)
	m := mocks.NewMockSSOClient(ctrl)
	m.EXPECT().AccessToken(gomock.Any(), "dead").Return(nil, sso.ErrInvalidGrant)
	runtime := testRuntime(t, db, m)
	if _, err := runtime.ForCharacter(705, dead.ID).eveAccess(ctx); err == nil {
		t.Fatal("want auth error")
	}
	if _, err := runtime.Sessions.LiveByID(ctx, dead.ID); err == nil {
		t.Fatal("requesting sid must be revoked")
	}
	if _, err := runtime.Sessions.LiveByID(ctx, live.ID); err != nil {
		t.Fatal("sibling session must stay live")
	}
}

func TestCharacterRevokesOnScopeDrift(t *testing.T) {
	t.Parallel()
	logger := mocks.QuietLogger(gomock.NewController(t))
	db := pgtest.Open(t, logger)
	ctx := context.Background()
	const characterID int64 = 708
	if err := characterpgx.New(db.Pool()).Upsert(ctx, character.Character{
		ID: characterID, Name: "P", OwnerHash: "h",
	}); err != nil {
		t.Fatal(err)
	}
	row, err := sessionpgx.New(db.Pool()).Create(ctx, dbsession.Session{
		CharacterID: characterID, RefreshToken: "rt", Scopes: []string{"publicData"},
		MCPClientID: "c",
	})
	if err != nil {
		t.Fatal(err)
	}
	ctrl := gomock.NewController(t)
	runtime := testRuntime(t, db, mocks.NewMockSSOClient(ctrl))
	sess := runtime.ForCharacter(int(characterID), row.ID)
	if _, err := sess.Character(ctx); err == nil {
		t.Fatal("want drift error")
	}
	if _, err := runtime.Sessions.LiveByID(ctx, row.ID); err == nil {
		t.Fatal("drift must revoke")
	}
	if _, err := sess.Character(ctx); err == nil {
		t.Fatal("revoked session must stay unauthorized")
	}
}

func TestTransientESIDoesNotRevoke(t *testing.T) {
	t.Parallel()
	logger := mocks.QuietLogger(gomock.NewController(t))
	db := pgtest.Open(t, logger)
	ctx := context.Background()
	const characterID int64 = 707
	if err := characterpgx.New(db.Pool()).Upsert(ctx, character.Character{
		ID: characterID, Name: "P", OwnerHash: "h",
	}); err != nil {
		t.Fatal(err)
	}
	row, err := sessionpgx.New(db.Pool()).Create(ctx, dbsession.Session{
		CharacterID: characterID, RefreshToken: "rt", Scopes: []string{}, MCPClientID: "c",
	})
	if err != nil {
		t.Fatal(err)
	}
	ctrl := gomock.NewController(t)
	esiMock := mocks.NewMockESIClient(ctrl)
	esiMock.EXPECT().ForUser(gomock.Any()).Return(esiMock).AnyTimes()
	esiMock.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		Return(esi.Result{}, esi.Error{Msg: "esi unavailable", Status: 500})
	runtime, err := Open(pgxOptions(db.Pool(), esiMock, mocks.NewMockSSOClient(ctrl), logger))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.ForCharacter(int(characterID), row.ID).ESI.Get(ctx, "/status", nil, nil, nil); err == nil {
		t.Fatal("want esi error")
	}
	if _, err := runtime.Sessions.LiveByID(ctx, row.ID); err != nil {
		t.Fatal("transient ESI must not revoke")
	}
}
