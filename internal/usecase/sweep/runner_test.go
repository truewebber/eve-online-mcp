package sweep

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.uber.org/mock/gomock"

	"github.com/truewebber/eve-online-mcp/internal/domain/authcode"
	authcodepgx "github.com/truewebber/eve-online-mcp/internal/domain/authcode/pgx"
	"github.com/truewebber/eve-online-mcp/internal/domain/character"
	characterpgx "github.com/truewebber/eve-online-mcp/internal/domain/character/pgx"
	"github.com/truewebber/eve-online-mcp/internal/domain/confirm"
	confirmpgx "github.com/truewebber/eve-online-mcp/internal/domain/confirm/pgx"
	"github.com/truewebber/eve-online-mcp/internal/domain/loginstate"
	loginstatepgx "github.com/truewebber/eve-online-mcp/internal/domain/loginstate/pgx"
	"github.com/truewebber/eve-online-mcp/internal/domain/mutation"
	mutationpgx "github.com/truewebber/eve-online-mcp/internal/domain/mutation/pgx"
	"github.com/truewebber/eve-online-mcp/internal/domain/oauthclient"
	oauthclientpgx "github.com/truewebber/eve-online-mcp/internal/domain/oauthclient/pgx"
	dbsession "github.com/truewebber/eve-online-mcp/internal/domain/session"
	sessionpgx "github.com/truewebber/eve-online-mcp/internal/domain/session/pgx"
	"github.com/truewebber/eve-online-mcp/internal/mocks"
	"github.com/truewebber/eve-online-mcp/internal/postgres"
	"github.com/truewebber/eve-online-mcp/internal/postgres/pgtest"
)

const (
	ageLoginSQL      = `UPDATE login_states SET created_at = now() - interval '20 minutes' WHERE state = $1`
	ageValidTilSQL   = `UPDATE sessions SET valid_til = now() - interval '1 day' WHERE id = $1`
	ageRevokedSQL    = `UPDATE sessions SET revoked_at = now() - interval '91 days' WHERE id = $1`
	ageMutationSQL   = `UPDATE mutations SET created_at = now() - interval '91 days' WHERE character_id = $1 AND summary = $2`
	ageClientSQL     = `UPDATE oauth_clients SET created_at = now() - interval '31 days' WHERE client_id = $1`
	softAgeSQL       = `UPDATE oauth_clients SET deleted_at = now() - interval '31 days' WHERE client_id = $1`
	sessionRowSQL    = `SELECT refresh_token IS NULL, revoked_at IS NOT NULL FROM sessions WHERE id = $1`
	countCharsSQL    = `SELECT COUNT(*) FROM characters`
	countClients     = `SELECT COUNT(*) FROM oauth_clients`
	countCodeSQL     = `SELECT COUNT(*) FROM auth_codes WHERE code = $1`
	expireConfirmSQL = `UPDATE confirm_tokens SET expires_at = now() - interval '1 minute' WHERE token = $1`
	testRedirect     = "http://localhost/cb"
)

func openRunner(t *testing.T) (*Runner, *postgres.DB, *mocks.MockSSOClient) {
	t.Helper()
	ctrl := gomock.NewController(t)
	logger := mocks.QuietLogger(ctrl)
	db := pgtest.Open(t, logger)
	sso := mocks.NewMockSSOClient(ctrl)
	pool := db.Pool()

	lock, err := NewPoolLock(pool)
	if err != nil {
		t.Fatal(err)
	}
	r, err := New(Options{
		Lock:      lock,
		Logins:    loginstatepgx.New(pool),
		Codes:     authcodepgx.New(pool),
		Confirms:  confirmpgx.New(pool),
		Sessions:  sessionpgx.New(pool),
		Mutations: mutationpgx.New(pool),
		Clients:   oauthclientpgx.New(pool),
		SSO:       sso,
		Logger:    logger,
	})
	if err != nil {
		t.Fatal(err)
	}

	return r, db, sso
}

func seedCharacter(t *testing.T, db *postgres.DB, id int64) {
	t.Helper()
	if err := characterpgx.New(db.Pool()).Upsert(
		context.Background(), character.Character{ID: id, Name: "P", OwnerHash: "h"},
	); err != nil {
		t.Fatal(err)
	}
}

func TestExpireSessionRevokesAtCCP(t *testing.T) {
	t.Parallel()
	r, db, sso := openRunner(t)
	ctx := t.Context()
	seedCharacter(t, db, 31)
	row, err := r.sessions.Create(ctx, dbsession.Session{
		CharacterID: 31, RefreshToken: "rt-exp", Scopes: []string{}, MCPClientID: "c",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool().Exec(ctx, ageValidTilSQL, row.ID); err != nil {
		t.Fatal(err)
	}
	sso.EXPECT().Revoke(gomock.Any(), "rt-exp").Do(func(ctx context.Context, _ string) {
		var tokenGone, marked bool
		if err := db.Pool().QueryRow(ctx, sessionRowSQL, row.ID).Scan(&tokenGone, &marked); err != nil {
			t.Fatal(err)
		}
		if !tokenGone || !marked {
			t.Fatalf("CCP revoke saw tokenGone=%v marked=%v", tokenGone, marked)
		}
	})
	r.Once(ctx)
}

func TestFailedCCPRevokeLeavesSessionRevoked(t *testing.T) {
	t.Parallel()
	r, db, sso := openRunner(t)
	ctx := t.Context()
	seedCharacter(t, db, 32)
	row, err := r.sessions.Create(ctx, dbsession.Session{
		CharacterID: 32, RefreshToken: "rt-fail", Scopes: []string{}, MCPClientID: "c",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool().Exec(ctx, ageValidTilSQL, row.ID); err != nil {
		t.Fatal(err)
	}
	sso.EXPECT().Revoke(gomock.Any(), "rt-fail")
	r.Once(ctx)
	var tokenGone, marked bool
	if err := db.Pool().QueryRow(ctx, sessionRowSQL, row.ID).Scan(&tokenGone, &marked); err != nil {
		t.Fatal(err)
	}
	if !tokenGone || !marked {
		t.Fatalf("tokenGone=%v marked=%v", tokenGone, marked)
	}
}

func TestAbandonedAuthCodeRevokedBeforeDelete(t *testing.T) {
	t.Parallel()
	r, db, sso := openRunner(t)
	ctx := t.Context()
	if err := r.codes.Put(ctx, authcode.Code{
		Value: "parked", CharacterID: 1, RefreshToken: "rt-code",
		MCPClientID: "c", RedirectURI: "r", CodeChallenge: "h",
		ExpiresAt: time.Now().Add(-time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	sso.EXPECT().Revoke(gomock.Any(), "rt-code").Do(func(ctx context.Context, _ string) {
		var n int
		if err := db.Pool().QueryRow(ctx, countCodeSQL, "parked").Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 0 {
			t.Fatal("code still present during CCP revoke")
		}
	})
	r.Once(ctx)
}

func TestAdjacentRowsSurvive(t *testing.T) {
	t.Parallel()
	r, db, sso := openRunner(t)
	ctx := t.Context()
	live := seedAdjacent(t, r, db)
	sso.EXPECT().Revoke(gomock.Any(), "rt-exp")
	sso.EXPECT().Revoke(gomock.Any(), "rt-code")
	r.Once(ctx)
	assertAdjacentKept(t, r, db, live)
	assertAdjacentGone(t, r)
}

func seedAdjacent(t *testing.T, r *Runner, db *postgres.DB) int64 {
	t.Helper()
	ctx := t.Context()
	seedCharacter(t, db, 33)
	seedCharacter(t, db, 34)
	live, err := r.sessions.Create(ctx, dbsession.Session{
		CharacterID: 33, RefreshToken: "rt-ok", Scopes: []string{}, MCPClientID: "keep-client",
	})
	if err != nil {
		t.Fatal(err)
	}
	expired, err := r.sessions.Create(ctx, dbsession.Session{
		CharacterID: 34, RefreshToken: "rt-exp", Scopes: []string{}, MCPClientID: "c",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool().Exec(ctx, ageValidTilSQL, expired.ID); err != nil {
		t.Fatal(err)
	}
	putLogin(t, r, "old-login")
	putLogin(t, r, "live-login")
	if _, err := db.Pool().Exec(ctx, ageLoginSQL, "old-login"); err != nil {
		t.Fatal(err)
	}
	putCode(t, r, "old-code", "rt-code", time.Now().Add(-time.Minute))
	putCode(t, r, "live-code", "rt-keep", time.Now().Add(2*time.Minute))
	if err := r.confirms.Put(ctx, confirm.Confirm{
		Value: "old-confirm", SessionID: live.ID, Tool: "t", ArgsDigest: "d",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool().Exec(ctx, expireConfirmSQL, "old-confirm"); err != nil {
		t.Fatal(err)
	}
	putMutation(t, r, "keep")
	putMutation(t, r, "drop")
	if _, err := db.Pool().Exec(ctx, ageMutationSQL, int64(33), "drop"); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"keep-client", "abandoned", "longgone"} {
		if err := r.clients.Upsert(ctx, oauthclient.Client{ID: id, RedirectURIs: []string{testRedirect}}); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Pool().Exec(ctx, ageClientSQL, id); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Pool().Exec(ctx, softAgeSQL, "longgone"); err != nil {
		t.Fatal(err)
	}

	return live.ID
}

func putLogin(t *testing.T, r *Runner, state string) {
	t.Helper()
	if err := r.logins.Put(t.Context(), loginstate.Login{State: state, PKCEVerifier: "v"}); err != nil {
		t.Fatal(err)
	}
}

func putCode(t *testing.T, r *Runner, value, token string, exp time.Time) {
	t.Helper()
	if err := r.codes.Put(t.Context(), authcode.Code{
		Value: value, CharacterID: 33, RefreshToken: token,
		MCPClientID: "c", RedirectURI: "r", CodeChallenge: "h", ExpiresAt: exp,
	}); err != nil {
		t.Fatal(err)
	}
}

func putMutation(t *testing.T, r *Runner, summary string) {
	t.Helper()
	if err := r.mutations.Append(t.Context(), mutation.Mutation{
		CharacterID: 33, Tool: mutation.ToolMailSend, Capability: "mail_send",
		ArgsDigest: summary + "digest00000000", Summary: summary, Outcome: mutation.OutcomeOK,
	}); err != nil {
		t.Fatal(err)
	}
}

func assertAdjacentKept(t *testing.T, r *Runner, db *postgres.DB, liveID int64) {
	t.Helper()
	ctx := t.Context()
	if _, err := r.sessions.LiveByID(ctx, liveID); err != nil {
		t.Fatalf("live session: %v", err)
	}
	if _, err := r.logins.Get(ctx, "live-login"); err != nil {
		t.Fatalf("live login: %v", err)
	}
	if _, err := r.codes.Get(ctx, "live-code"); err != nil {
		t.Fatalf("live code: %v", err)
	}
	if _, err := r.clients.Get(ctx, "keep-client"); err != nil {
		t.Fatalf("used client: %v", err)
	}
	kept, err := r.mutations.CountMailCap(ctx, 33)
	if err != nil || kept != 1 {
		t.Fatalf("mutations kept %d %v", kept, err)
	}
	var clients int
	if err := db.Pool().QueryRow(ctx, countClients).Scan(&clients); err != nil || clients != 2 {
		t.Fatalf("clients %d %v", clients, err)
	}
	var chars int
	if err := db.Pool().QueryRow(ctx, countCharsSQL).Scan(&chars); err != nil || chars != 2 {
		t.Fatalf("characters swept: %d %v", chars, err)
	}
}

func assertAdjacentGone(t *testing.T, r *Runner) {
	t.Helper()
	ctx := t.Context()
	if _, err := r.logins.Get(ctx, "old-login"); err == nil {
		t.Fatal("expired login survived")
	}
	if _, err := r.codes.Get(ctx, "old-code"); err == nil {
		t.Fatal("expired code survived")
	}
	if _, err := r.confirms.Get(ctx, "old-confirm"); err == nil {
		t.Fatal("expired confirm survived")
	}
	if _, err := r.clients.Get(ctx, "abandoned"); err == nil {
		t.Fatal("abandoned client still visible")
	}
}

func TestLockHeldSkipsWork(t *testing.T) {
	t.Parallel()
	r, db, sso := openRunner(t)
	ctx := t.Context()
	seedCharacter(t, db, 35)
	row, err := r.sessions.Create(ctx, dbsession.Session{
		CharacterID: 35, RefreshToken: "rt-held", Scopes: []string{}, MCPClientID: "c",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool().Exec(ctx, ageValidTilSQL, row.ID); err != nil {
		t.Fatal(err)
	}
	conn, err := db.Pool().Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var held bool
	if err := conn.QueryRow(ctx, tryLockSQL, lockKey).Scan(&held); err != nil || !held {
		conn.Release()
		t.Fatalf("hold %v %v", held, err)
	}
	r.Once(ctx)
	var tokenGone, marked bool
	if err := db.Pool().QueryRow(ctx, sessionRowSQL, row.ID).Scan(&tokenGone, &marked); err != nil {
		t.Fatal(err)
	}
	if tokenGone || marked {
		t.Fatal("swept while another pod held the lock")
	}
	if _, err := conn.Exec(ctx, unlockSQL, lockKey); err != nil {
		t.Fatal(err)
	}
	conn.Release()
	sso.EXPECT().Revoke(gomock.Any(), "rt-held")
	r.Once(ctx)
	if err := db.Pool().QueryRow(ctx, sessionRowSQL, row.ID).Scan(&tokenGone, &marked); err != nil {
		t.Fatal(err)
	}
	if !tokenGone || !marked {
		t.Fatalf("after unlock tokenGone=%v marked=%v", tokenGone, marked)
	}
}

func TestTwoSweepersDoTheWorkOnce(t *testing.T) {
	t.Parallel()
	r1, db, sso := openRunner(t)
	logger := mocks.QuietLogger(gomock.NewController(t))
	pool := db.Pool()
	lock, err := NewPoolLock(pool)
	if err != nil {
		t.Fatal(err)
	}
	r2, err := New(Options{
		Lock:      lock,
		Logins:    loginstatepgx.New(pool),
		Codes:     authcodepgx.New(pool),
		Confirms:  confirmpgx.New(pool),
		Sessions:  sessionpgx.New(pool),
		Mutations: mutationpgx.New(pool),
		Clients:   oauthclientpgx.New(pool),
		SSO:       sso,
		Logger:    logger,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	seedCharacter(t, db, 36)
	row, err := r1.sessions.Create(ctx, dbsession.Session{
		CharacterID: 36, RefreshToken: "rt-once", Scopes: []string{}, MCPClientID: "c",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool().Exec(ctx, ageValidTilSQL, row.ID); err != nil {
		t.Fatal(err)
	}
	var n atomic.Int64
	sso.EXPECT().Revoke(gomock.Any(), "rt-once").Do(func(context.Context, string) {
		n.Add(1)
	}).AnyTimes()
	var wg sync.WaitGroup
	wg.Go(func() { r1.Once(ctx) })
	wg.Go(func() { r2.Once(ctx) })
	wg.Wait()
	if n.Load() != 1 {
		t.Fatalf("revokes %d", n.Load())
	}
}
