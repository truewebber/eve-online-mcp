package oauth

import (
	"context"
	"errors"
	nhttp "net/http"
	"net/url"
	"strings"
	"testing"

	"go.uber.org/mock/gomock"

	"github.com/truewebber/eve-online-mcp/internal/adapter/esi"
	esihttp "github.com/truewebber/eve-online-mcp/internal/adapter/esi/http"
	"github.com/truewebber/eve-online-mcp/internal/adapter/sso"
	ssohttp "github.com/truewebber/eve-online-mcp/internal/adapter/sso/http"
	"github.com/truewebber/eve-online-mcp/internal/adapter/store"
	"github.com/truewebber/eve-online-mcp/internal/adapter/store/storetest"
	"github.com/truewebber/eve-online-mcp/internal/domain/authcode"
	authcodepgx "github.com/truewebber/eve-online-mcp/internal/domain/authcode/pgx"
	"github.com/truewebber/eve-online-mcp/internal/domain/character"
	characterpgx "github.com/truewebber/eve-online-mcp/internal/domain/character/pgx"
	"github.com/truewebber/eve-online-mcp/internal/domain/confirm"
	confirmpgx "github.com/truewebber/eve-online-mcp/internal/domain/confirm/pgx"
	"github.com/truewebber/eve-online-mcp/internal/domain/loginstate"
	loginstatepgx "github.com/truewebber/eve-online-mcp/internal/domain/loginstate/pgx"

	oauthclientpgx "github.com/truewebber/eve-online-mcp/internal/domain/oauthclient/pgx"
	"github.com/truewebber/eve-online-mcp/internal/mocks"
	"github.com/truewebber/eve-online-mcp/internal/usecase/session"
)

const (
	redirect    = "http://localhost:1/cb"
	janeDoe     = "Jane Doe"
	newRT       = "new-rt"
	altName     = "Alt"
	testHMACKey = "0123456789abcdef0123456789abcdef"
)

func openDB(t *testing.T) *store.Store {
	t.Helper()

	return storetest.Open(t, mocks.QuietLogger(gomock.NewController(t)))
}

func characters(t *testing.T, db *store.Store) character.Repository {
	t.Helper()

	return characterpgx.New(db.Pool(), mocks.QuietLogger(gomock.NewController(t)))
}

func logins(db *store.Store) loginstate.Repository {
	return loginstatepgx.New(db.Pool())
}

func codes(db *store.Store) authcode.Repository {
	return authcodepgx.New(db.Pool())
}

func confirms(db *store.Store) confirm.Repository {
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

func testServer(t *testing.T, db *store.Store) *Server {
	t.Helper()
	logger := mocks.QuietLogger(gomock.NewController(t))
	runtime, err := session.Open(session.Options{
		Store:      db,
		Characters: characters(t, db),
		Clients:    oauthclientpgx.New(db.Pool()),
		Logins:     logins(db),
		Codes:      codes(db),
		Confirms:   confirms(db),
		ESI:        testESI(t),
		SSO:        testSSO(t),
		Logger:     logger,
	})
	if err != nil {
		t.Fatal(err)
	}
	s, err := Open(Host{Listen: "127.0.0.1:8765"}, runtime, db, Options{HMACKey: []byte(testHMACKey)}, logger)
	if err != nil {
		t.Fatal(err)
	}

	return s
}

func TestFinishMCPAttachesExistingOwner(t *testing.T) {
	t.Parallel()
	db := openDB(t)
	ctx := context.Background()
	u, err := db.CreateUser(ctx)
	if err != nil {
		t.Fatal(err)
	}
	const charID int64 = 2112625428
	chars := characters(t, db)
	if err := chars.Upsert(ctx, character.Character{
		ID: charID, UserID: u.ID, Name: janeDoe, RefreshToken: "old-rt",
	}); err != nil {
		t.Fatal(err)
	}
	s := testServer(t, db)
	loc, err := s.finishMCP(ctx, &loginstate.Login{
		MCPClientID: "c", RedirectURI: redirect,
		MCPState: "mcp", CodeChallenge: "x",
	}, &sso.CharacterToken{
		CharacterID: int(charID), CharacterName: janeDoe, RefreshToken: newRT,
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := url.Parse(loc)
	if err != nil || got.Query().Get(paramCode) == "" {
		t.Fatalf("redirect %s err %v", loc, err)
	}
	row, err := chars.Get(ctx, charID)
	if err != nil || row.UserID != u.ID {
		t.Fatalf("owner %v err %v", row, err)
	}
	if row.RefreshToken != newRT {
		t.Fatalf("refresh %s", row.RefreshToken)
	}
	if tok := s.SessionFor(u.ID).SSO.Get(ctx, int(charID)); tok == nil {
		t.Fatal("session store miss")
	}
}

func TestFinishMCPCreatesUser(t *testing.T) {
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
	if err != nil || row.UserID == "" {
		t.Fatalf("owner %v err %v", row, err)
	}
	if _, err := db.GetUser(context.Background(), row.UserID); err != nil {
		t.Fatal(err)
	}
}

func TestFinishAltRefusesOtherUser(t *testing.T) {
	t.Parallel()
	db := openDB(t)
	ctx := context.Background()
	a, err := db.CreateUser(ctx)
	if err != nil {
		t.Fatal(err)
	}
	b, err := db.CreateUser(ctx)
	if err != nil {
		t.Fatal(err)
	}
	const charID int64 = 2112625428
	chars := characters(t, db)
	if err := chars.Upsert(ctx, character.Character{
		ID: charID, UserID: a.ID, Name: janeDoe, RefreshToken: "a-rt",
	}); err != nil {
		t.Fatal(err)
	}
	s := testServer(t, db)
	err = s.finishAlt(ctx, &loginstate.Login{UserID: b.ID}, &sso.CharacterToken{
		CharacterID: int(charID), CharacterName: janeDoe, RefreshToken: "b-rt",
	})
	if !errors.As(err, new(CharacterOwnedError)) {
		t.Fatalf("want CharacterOwnedError, got %v", err)
	}
	if !strings.Contains(err.Error(), "eve_auth_logout") {
		t.Fatalf("want actionable message, got %q", err)
	}
	row, err := chars.Get(ctx, charID)
	if err != nil || row.UserID != a.ID {
		t.Fatalf("owner %v err %v", row, err)
	}
	if row.RefreshToken != "a-rt" {
		t.Fatalf("A must keep the token, got %s", row.RefreshToken)
	}
	if tok := s.SessionFor(b.ID).SSO.Get(ctx, int(charID)); tok != nil {
		t.Fatal("B must not hold the character")
	}
}

func TestFinishAltRefreshesOwnCharacter(t *testing.T) {
	t.Parallel()
	db := openDB(t)
	ctx := context.Background()
	u, err := db.CreateUser(ctx)
	if err != nil {
		t.Fatal(err)
	}
	const charID int64 = 99
	chars := characters(t, db)
	if err := chars.Upsert(ctx, character.Character{
		ID: charID, UserID: u.ID, Name: altName, RefreshToken: "old-rt",
	}); err != nil {
		t.Fatal(err)
	}
	s := testServer(t, db)
	if err := s.finishAlt(ctx, &loginstate.Login{UserID: u.ID}, &sso.CharacterToken{
		CharacterID: int(charID), CharacterName: altName, RefreshToken: newRT,
	}); err != nil {
		t.Fatal(err)
	}
	row, err := chars.Get(ctx, charID)
	if err != nil || row.UserID != u.ID {
		t.Fatalf("owner %v err %v", row, err)
	}
	if row.RefreshToken != newRT {
		t.Fatalf("refresh %s", row.RefreshToken)
	}
	n, err := chars.ListByUser(ctx, u.ID)
	if err != nil || len(n) != 1 {
		t.Fatalf("rows %d err %v", len(n), err)
	}
}
