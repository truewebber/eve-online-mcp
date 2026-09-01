package oauth

import (
	"context"
	"errors"
	nhttp "net/http"
	"net/url"
	"testing"

	"go.uber.org/mock/gomock"

	"github.com/truewebber/eve-online-mcp/internal/adapter/esi"
	esihttp "github.com/truewebber/eve-online-mcp/internal/adapter/esi/http"
	"github.com/truewebber/eve-online-mcp/internal/adapter/sso"
	ssohttp "github.com/truewebber/eve-online-mcp/internal/adapter/sso/http"
	"github.com/truewebber/eve-online-mcp/internal/domain/authcode"
	authcodepgx "github.com/truewebber/eve-online-mcp/internal/domain/authcode/pgx"
	"github.com/truewebber/eve-online-mcp/internal/domain/character"
	characterpgx "github.com/truewebber/eve-online-mcp/internal/domain/character/pgx"
	"github.com/truewebber/eve-online-mcp/internal/domain/confirm"
	confirmpgx "github.com/truewebber/eve-online-mcp/internal/domain/confirm/pgx"
	"github.com/truewebber/eve-online-mcp/internal/domain/loginstate"
	loginstatepgx "github.com/truewebber/eve-online-mcp/internal/domain/loginstate/pgx"
	mutationpgx "github.com/truewebber/eve-online-mcp/internal/domain/mutation/pgx"
	oauthclientpgx "github.com/truewebber/eve-online-mcp/internal/domain/oauthclient/pgx"
	dbsession "github.com/truewebber/eve-online-mcp/internal/domain/session"
	sessionpgx "github.com/truewebber/eve-online-mcp/internal/domain/session/pgx"
	"github.com/truewebber/eve-online-mcp/internal/domain/write"
	"github.com/truewebber/eve-online-mcp/internal/mocks"
	"github.com/truewebber/eve-online-mcp/internal/observe"
	"github.com/truewebber/eve-online-mcp/internal/postgres"
	"github.com/truewebber/eve-online-mcp/internal/postgres/pgtest"
	"github.com/truewebber/eve-online-mcp/internal/usecase/session"
)

const (
	redirect    = "http://localhost:1/cb"
	janeDoe     = "Jane Doe"
	newRT       = "new-rt"
	altName     = "Alt"
	testHMACKey = "0123456789abcdef0123456789abcdef"
	scopePublic = "publicData"
	testListen  = "127.0.0.1:8765"
)

func openDB(t *testing.T) *postgres.DB {
	t.Helper()

	return pgtest.Open(t, mocks.QuietLogger(gomock.NewController(t)))
}

func characters(t *testing.T, db *postgres.DB) character.Repository {
	t.Helper()

	return characterpgx.New(db.Pool())
}

func sessions(t *testing.T, db *postgres.DB) dbsession.Repository {
	t.Helper()

	return sessionpgx.New(db.Pool())
}

func logins(db *postgres.DB) loginstate.Repository {
	return loginstatepgx.New(db.Pool())
}

func codes(db *postgres.DB) authcode.Repository {
	return authcodepgx.New(db.Pool())
}

func confirms(db *postgres.DB) confirm.Repository {
	return confirmpgx.New(db.Pool())
}

func testHost() Host {
	return Host{
		PublicURL:   "http://" + testListen,
		MCPPath:     "/mcp",
		CallbackURL: "http://" + testListen + "/auth/callback",
	}
}

func testESI(t *testing.T) esi.Client {
	t.Helper()
	c, err := esihttp.New(esi.Options{
		BaseURL:    esi.DefaultBaseURL,
		CompatDate: "2026-08-18",
		UserAgent:  "eve-mcp-test",
		Observe:    observe.New(),
	}, nhttp.DefaultClient, mocks.QuietLogger(gomock.NewController(t)))
	if err != nil {
		t.Fatal(err)
	}

	return c
}

func testSSO(t *testing.T) sso.Client {
	t.Helper()
	ctrl := gomock.NewController(t)
	m := mocks.NewMockSSOClient(ctrl)
	login, err := ssohttp.New(sso.Options{
		ClientID:    "test-eve-client",
		CallbackURL: "http://127.0.0.1/auth/callback",
		UserAgent:   "eve-mcp-test",
	}, nhttp.DefaultClient, mocks.QuietLogger(ctrl))
	if err != nil {
		t.Fatal(err)
	}
	m.EXPECT().PrepareLogin(gomock.Any()).DoAndReturn(login.PrepareLogin).AnyTimes()
	m.EXPECT().ExchangeCode(gomock.Any(), gomock.Any(), gomock.Any()).Return(&sso.CharacterToken{
		CharacterID: 1, CharacterName: janeDoe, RefreshToken: "rt",
	}, nil).AnyTimes()
	m.EXPECT().AccessToken(gomock.Any(), gomock.Any()).Return(&sso.CharacterToken{
		AccessToken: "at", RefreshToken: "rt",
	}, nil).AnyTimes()
	m.EXPECT().Revoke(gomock.Any(), gomock.Any()).AnyTimes()

	return m
}

func testServer(t *testing.T, db *postgres.DB) *Server {
	t.Helper()

	return testServerSSO(t, db, testSSO(t))
}

func grantedToken(id int, name, refresh, hash string) *sso.CharacterToken {
	return &sso.CharacterToken{
		CharacterID: id, CharacterName: name, RefreshToken: refresh, OwnerHash: hash,
		Scopes: write.RequestedScopes(),
	}
}

func testServerSSO(t *testing.T, db *postgres.DB, ssoClient sso.Client) *Server {
	t.Helper()
	logger := mocks.QuietLogger(gomock.NewController(t))
	pool := db.Pool()
	runtime, err := session.Open(session.Options{
		Characters: characters(t, db),
		Sessions:   sessions(t, db),
		Clients:    oauthclientpgx.New(pool),
		Logins:     logins(db),
		Codes:      codes(db),
		Confirms:   confirms(db),
		Mutations:  mutationpgx.New(pool),
		ESI:        testESI(t),
		SSO:        ssoClient,
		WithinTx: func(ctx context.Context, fn func(context.Context) error) error {
			return postgres.WithinTx(ctx, pool, fn)
		},
		Logger: logger,
	})
	if err != nil {
		t.Fatal(err)
	}
	s, err := Open(testHost(), runtime, Options{HMACKey: []byte(testHMACKey)}, logger)
	if err != nil {
		t.Fatal(err)
	}

	return s
}

func TestFinishMCPUpsertsCharacter(t *testing.T) {
	t.Parallel()
	db := openDB(t)
	ctx := context.Background()
	const charID int64 = 2112625428
	chars := characters(t, db)
	if err := chars.Upsert(ctx, character.Character{
		ID: charID, Name: janeDoe, OwnerHash: "h1",
	}); err != nil {
		t.Fatal(err)
	}
	s := testServer(t, db)
	loc, err := s.finishMCP(ctx, &loginstate.Login{
		MCPClientID: "c", RedirectURI: redirect,
		MCPState: "mcp", CodeChallenge: "x",
	}, grantedToken(int(charID), janeDoe, newRT, "h1"))
	if err != nil {
		t.Fatal(err)
	}
	got, err := url.Parse(loc)
	if err != nil || got.Query().Get(paramCode) == "" {
		t.Fatalf("redirect %s err %v", loc, err)
	}
	row, err := chars.Get(ctx, charID)
	if err != nil || !row.Live() || row.OwnerHash != "h1" {
		t.Fatalf("row %+v err %v", row, err)
	}
	parked, err := codes(db).Get(ctx, got.Query().Get(paramCode))
	if err != nil || parked.RefreshToken != newRT {
		t.Fatalf("parked grant %+v err %v", parked, err)
	}
}

func TestFinishMCPPreservesClientQuery(t *testing.T) {
	t.Parallel()
	db := openDB(t)
	s := testServer(t, db)
	loc, err := s.finishMCP(t.Context(), &loginstate.Login{
		MCPClientID: "c", RedirectURI: "http://localhost:1/cb?foo=1&bar=two",
		MCPState: "st",
	}, grantedToken(8, janeDoe, "rt", ""))
	if err != nil {
		t.Fatal(err)
	}
	got, err := url.Parse(loc)
	if err != nil {
		t.Fatal(err)
	}
	q := got.Query()
	if q.Get("foo") != "1" || q.Get("bar") != "two" {
		t.Fatalf("client query lost: %q", loc)
	}
	if q.Get(paramCode) == "" || q.Get("state") != "st" {
		t.Fatalf("code/state %q", loc)
	}
}

func TestFinishMCPCreatesCharacter(t *testing.T) {
	t.Parallel()
	db := openDB(t)
	s := testServer(t, db)
	const charID int64 = 42
	loc, err := s.finishMCP(context.Background(), &loginstate.Login{
		MCPClientID: "c", RedirectURI: redirect,
	}, grantedToken(int(charID), "New", "rt", ""))
	if err != nil {
		t.Fatal(err)
	}
	if loc == "" {
		t.Fatal("empty redirect")
	}
	row, err := characters(t, db).Get(context.Background(), charID)
	if err != nil || !row.Live() {
		t.Fatalf("row %+v err %v", row, err)
	}
}

func TestOwnerHashChangeReplacesIdentity(t *testing.T) {
	t.Parallel()
	db := openDB(t)
	ctx := context.Background()
	const charID int64 = 2112625428
	chars := characters(t, db)
	if err := chars.Upsert(ctx, character.Character{
		ID: charID, Name: janeDoe, OwnerHash: "old-hash",
	}); err != nil {
		t.Fatal(err)
	}
	s := testServer(t, db)
	pred, err := sessions(t, db).Create(ctx, dbsession.Session{
		CharacterID: charID, RefreshToken: "old-rt", Scopes: write.RequestedScopes(),
		MCPClientID: "c",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.finishMCP(ctx, &loginstate.Login{
		MCPClientID: "c", RedirectURI: redirect,
	}, grantedToken(int(charID), janeDoe, newRT, "new-hash")); err != nil {
		t.Fatal(err)
	}
	row, err := chars.Get(ctx, charID)
	if err != nil || row.OwnerHash != "new-hash" {
		t.Fatalf("row %+v err %v", row, err)
	}
	if _, err := s.runtime.Sessions.LiveByID(ctx, pred.ID); err == nil {
		t.Fatal("old session must be revoked")
	}
}

func TestLogoutSoftDeleteThenRelogin(t *testing.T) {
	t.Parallel()
	db := openDB(t)
	ctx := context.Background()
	const charID int64 = 99
	s := testServer(t, db)
	if _, err := s.finishMCP(ctx, &loginstate.Login{
		MCPClientID: "c", RedirectURI: redirect,
	}, grantedToken(int(charID), altName, "rt-1", "h")); err != nil {
		t.Fatal(err)
	}
	if err := characters(t, db).Delete(ctx, charID); err != nil {
		t.Fatal(err)
	}
	row, err := characters(t, db).Get(ctx, charID)
	if err != nil || row.Live() {
		t.Fatalf("want soft-deleted, got %+v %v", row, err)
	}
	if _, err := s.finishMCP(ctx, &loginstate.Login{
		MCPClientID: "c", RedirectURI: redirect,
	}, grantedToken(int(charID), altName, "rt-2", "h")); err != nil {
		t.Fatal(err)
	}
	row, err = characters(t, db).Get(ctx, charID)
	if err != nil || !row.Live() {
		t.Fatalf("want revived, got %+v %v", row, err)
	}
	created, err := sessions(t, db).Create(ctx, dbsession.Session{
		CharacterID: charID, RefreshToken: "rt-2", Scopes: write.RequestedScopes(),
		MCPClientID: "c",
	})
	if err != nil {
		t.Fatal(err)
	}
	tok, err := s.IssueAccess(int(charID), created.ID)
	if err != nil {
		t.Fatal(err)
	}
	info, err := s.VerifyAccess(ctx, tok, nil)
	if err != nil || info.UserID != "99" {
		t.Fatalf("sub %+v err %v", info, err)
	}
}

func TestConcurrentFinishMCP(t *testing.T) {
	t.Parallel()
	db := openDB(t)
	ctx := context.Background()
	const charID int64 = 77
	s := testServer(t, db)
	errc := make(chan error, 2)
	for range 2 {
		go func() {
			_, err := s.finishMCP(ctx, &loginstate.Login{
				MCPClientID: "c", RedirectURI: redirect,
			}, grantedToken(int(charID), janeDoe, "rt", "h"))
			errc <- err
		}()
	}
	for range 2 {
		if err := <-errc; err != nil {
			t.Fatal(err)
		}
	}
	row, err := characters(t, db).Get(ctx, charID)
	if err != nil || !row.Live() {
		t.Fatalf("row %+v err %v", row, err)
	}
}

func TestShortGrantWritesNoCode(t *testing.T) {
	t.Parallel()
	db := openDB(t)
	ctx := context.Background()
	s := testServer(t, db)
	loc, err := s.finishMCP(ctx, &loginstate.Login{
		MCPClientID: "c", RedirectURI: redirect,
	}, &sso.CharacterToken{
		CharacterID: 88, CharacterName: janeDoe, RefreshToken: "rt",
		Scopes: []string{scopePublic},
	})
	short, ok := errors.AsType[ShortGrantError](err)
	if !ok || loc != "" {
		t.Fatalf("loc %q err %v", loc, err)
	}
	if len(short.Missing) == 0 {
		t.Fatal("missing scopes empty")
	}
	var count int
	if err := db.Pool().QueryRow(ctx, `SELECT count(*) FROM auth_codes`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("auth_codes %d err %v", count, err)
	}
	if _, err := characters(t, db).Get(ctx, 88); err == nil {
		t.Fatal("short grant must not upsert a character")
	}
}
