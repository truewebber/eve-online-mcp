package oauth

import (
	"context"
	"errors"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/truewebber/eve-online-mcp/internal/adapter/sso"
	"github.com/truewebber/eve-online-mcp/internal/adapter/store"
	"github.com/truewebber/eve-online-mcp/internal/logtest"
	"github.com/truewebber/eve-online-mcp/internal/usecase/session"
)

const (
	redirect = "http://localhost:1/cb"
	janeDoe  = "Jane Doe"
	newRT    = "new-rt"
	altName  = "Alt"
)

func openDB(t *testing.T) *store.Store {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL is unset; run `make postgres`")
	}
	ctx := context.Background()
	s, err := store.Open(ctx, dsn, logtest.Silent{})
	if err != nil {
		t.Fatal(err)
	}
	release, err := s.HoldTestLock(ctx)
	if err != nil {
		s.Close()
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	t.Cleanup(release)
	if err := s.ResetTables(ctx); err != nil {
		t.Fatal(err)
	}

	return s
}

func testServer(t *testing.T, db *store.Store) *Server {
	t.Helper()
	runtime, err := session.Open(session.Options{
		Store:  db,
		Logger: logtest.Silent{},
		SSO: sso.Options{
			ClientID:    "test-eve-client",
			CallbackURL: "http://127.0.0.1/auth/callback",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	s, err := Open(Host{Listen: "127.0.0.1:8765"}, runtime, db, logtest.Silent{})
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
	if err := db.UpsertCharacter(ctx, u.ID, store.CharacterRow{
		CharacterID: charID, Name: janeDoe, RefreshToken: "old-rt",
	}); err != nil {
		t.Fatal(err)
	}
	s := testServer(t, db)
	loc, err := s.finishMCP(ctx, &store.LoginState{
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
	owner, ok, err := db.OwnerOf(ctx, charID)
	if err != nil || !ok || owner != u.ID {
		t.Fatalf("owner %s ok %v err %v", owner, ok, err)
	}
	row, err := db.GetCharacter(ctx, charID)
	if err != nil {
		t.Fatal(err)
	}
	if row.RefreshToken != newRT {
		t.Fatalf("refresh %s", row.RefreshToken)
	}
	if tok := s.SessionFor(u.ID).SSO.Store.Get(ctx, int(charID)); tok == nil {
		t.Fatal("session store miss")
	}
}

func TestFinishMCPCreatesUser(t *testing.T) {
	t.Parallel()
	db := openDB(t)
	s := testServer(t, db)
	const charID int64 = 42
	loc, err := s.finishMCP(context.Background(), &store.LoginState{
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
	owner, ok, err := db.OwnerOf(context.Background(), charID)
	if err != nil || !ok || owner == "" {
		t.Fatalf("owner %s ok %v err %v", owner, ok, err)
	}
	if _, err := db.GetUser(context.Background(), owner); err != nil {
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
	if err := db.UpsertCharacter(ctx, a.ID, store.CharacterRow{
		CharacterID: charID, Name: janeDoe, RefreshToken: "a-rt",
	}); err != nil {
		t.Fatal(err)
	}
	s := testServer(t, db)
	err = s.finishAlt(ctx, &store.LoginState{UserID: b.ID}, &sso.CharacterToken{
		CharacterID: int(charID), CharacterName: janeDoe, RefreshToken: "b-rt",
	})
	if !errors.As(err, new(CharacterOwnedError)) {
		t.Fatalf("want CharacterOwnedError, got %v", err)
	}
	if !strings.Contains(err.Error(), "eve_auth_logout") {
		t.Fatalf("want actionable message, got %q", err)
	}
	owner, ok, err := db.OwnerOf(ctx, charID)
	if err != nil || !ok || owner != a.ID {
		t.Fatalf("owner %s ok %v err %v", owner, ok, err)
	}
	row, err := db.GetCharacter(ctx, charID)
	if err != nil {
		t.Fatal(err)
	}
	if row.RefreshToken != "a-rt" {
		t.Fatalf("A must keep the token, got %s", row.RefreshToken)
	}
	if tok := s.SessionFor(b.ID).SSO.Store.Get(ctx, int(charID)); tok != nil {
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
	if err := db.UpsertCharacter(ctx, u.ID, store.CharacterRow{
		CharacterID: charID, Name: altName, RefreshToken: "old-rt",
	}); err != nil {
		t.Fatal(err)
	}
	s := testServer(t, db)
	if err := s.finishAlt(ctx, &store.LoginState{UserID: u.ID}, &sso.CharacterToken{
		CharacterID: int(charID), CharacterName: altName, RefreshToken: newRT,
	}); err != nil {
		t.Fatal(err)
	}
	owner, ok, err := db.OwnerOf(ctx, charID)
	if err != nil || !ok || owner != u.ID {
		t.Fatalf("owner %s ok %v err %v", owner, ok, err)
	}
	row, err := db.GetCharacter(ctx, charID)
	if err != nil {
		t.Fatal(err)
	}
	if row.RefreshToken != newRT {
		t.Fatalf("refresh %s", row.RefreshToken)
	}
	n, err := db.ListCharacters(ctx, u.ID)
	if err != nil || len(n) != 1 {
		t.Fatalf("rows %d err %v", len(n), err)
	}
}
