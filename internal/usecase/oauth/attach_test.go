package oauth

import (
	"context"
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
	"github.com/truewebber/eve-online-mcp/internal/mocks"
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
)

func openDB(t *testing.T) *postgres.DB {
	t.Helper()

	return pgtest.Open(t, mocks.QuietLogger(gomock.NewController(t)))
}

func characters(t *testing.T, db *postgres.DB) character.Repository {
	t.Helper()

	return characterpgx.New(db.Pool(), mocks.QuietLogger(gomock.NewController(t)))
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

func testESI(t *testing.T) esi.Client {
	t.Helper()

	return esihttp.New(esi.Options{}, nhttp.DefaultClient, mocks.QuietLogger(gomock.NewController(t)))
}

func testSSO(t *testing.T) sso.Client {
	t.Helper()

	return ssohttp.New(sso.Options{
		ClientID:    "test-eve-client",
		CallbackURL: "http://127.0.0.1/auth/callback",
	}, nhttp.DefaultClient, mocks.QuietLogger(gomock.NewController(t)))
}

func testServer(t *testing.T, db *postgres.DB) *Server {
	t.Helper()
	logger := mocks.QuietLogger(gomock.NewController(t))
	runtime, err := session.Open(session.Options{
		Characters: characters(t, db),
		Clients:    oauthclientpgx.New(db.Pool()),
		Logins:     logins(db),
		Codes:      codes(db),
		Confirms:   confirms(db),
		Mutations:  mutationpgx.New(db.Pool()),
		ESI:        testESI(t),
		SSO:        testSSO(t),
		Logger:     logger,
	})
	if err != nil {
		t.Fatal(err)
	}
	s, err := Open(Host{Listen: "127.0.0.1:8765"}, runtime, Options{HMACKey: []byte(testHMACKey)}, logger)
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
		ID: charID, Name: janeDoe, RefreshToken: "old-rt", OwnerHash: "h1",
	}); err != nil {
		t.Fatal(err)
	}
	s := testServer(t, db)
	loc, err := s.finishMCP(ctx, &loginstate.Login{
		MCPClientID: "c", RedirectURI: redirect,
		MCPState: "mcp", CodeChallenge: "x",
	}, &sso.CharacterToken{
		CharacterID: int(charID), CharacterName: janeDoe, RefreshToken: newRT, OwnerHash: "h1",
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := url.Parse(loc)
	if err != nil || got.Query().Get(paramCode) == "" {
		t.Fatalf("redirect %s err %v", loc, err)
	}
	row, err := chars.Get(ctx, charID)
	if err != nil || !row.Live() || row.RefreshToken != newRT {
		t.Fatalf("row %+v err %v", row, err)
	}
	if tok := s.SessionFor(int(charID)).SSO.Get(ctx, int(charID)); tok == nil {
		t.Fatal("session store miss")
	}
}

func TestFinishMCPPreservesClientQuery(t *testing.T) {
	t.Parallel()
	db := openDB(t)
	s := testServer(t, db)
	loc, err := s.finishMCP(t.Context(), &loginstate.Login{
		MCPClientID: "c", RedirectURI: "http://localhost:1/cb?foo=1&bar=two",
		MCPState: "st",
	}, &sso.CharacterToken{
		CharacterID: 8, CharacterName: janeDoe, RefreshToken: "rt",
	})
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
	}, &sso.CharacterToken{
		CharacterID: int(charID), CharacterName: "New", RefreshToken: "rt",
	})
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

func TestOwnerHashChangeReplacesGrant(t *testing.T) {
	t.Parallel()
	db := openDB(t)
	ctx := context.Background()
	const charID int64 = 2112625428
	chars := characters(t, db)
	if err := chars.Upsert(ctx, character.Character{
		ID: charID, Name: janeDoe, RefreshToken: "old-rt", OwnerHash: "old-hash",
	}); err != nil {
		t.Fatal(err)
	}
	s := testServer(t, db)
	if _, err := s.finishMCP(ctx, &loginstate.Login{
		MCPClientID: "c", RedirectURI: redirect,
	}, &sso.CharacterToken{
		CharacterID: int(charID), CharacterName: janeDoe, RefreshToken: newRT, OwnerHash: "new-hash",
	}); err != nil {
		t.Fatal(err)
	}
	row, err := chars.Get(ctx, charID)
	if err != nil || row.OwnerHash != "new-hash" || row.RefreshToken != newRT {
		t.Fatalf("row %+v err %v", row, err)
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
	}, &sso.CharacterToken{
		CharacterID: int(charID), CharacterName: altName, RefreshToken: "rt-1", OwnerHash: "h",
	}); err != nil {
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
	}, &sso.CharacterToken{
		CharacterID: int(charID), CharacterName: altName, RefreshToken: "rt-2", OwnerHash: "h",
	}); err != nil {
		t.Fatal(err)
	}
	row, err = characters(t, db).Get(ctx, charID)
	if err != nil || !row.Live() || row.RefreshToken != "rt-2" {
		t.Fatalf("want revived, got %+v %v", row, err)
	}
	tok, err := s.IssueAccess(int(charID))
	if err != nil {
		t.Fatal(err)
	}
	info, err := s.verifyAccess(tok)
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
			}, &sso.CharacterToken{
				CharacterID: int(charID), CharacterName: janeDoe, RefreshToken: "rt", OwnerHash: "h",
			})
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
