package oauth

import (
	"context"
	"net/url"
	"os"
	"testing"

	"eve-mcp/internal/adapter/sso"
	"eve-mcp/internal/adapter/store"
	"eve-mcp/internal/usecase/session"
)

func openDB(t *testing.T) *store.Store {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL is unset; run `make postgres`")
	}
	ctx := context.Background()
	s, err := store.Open(ctx, dsn)
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
		Store: db,
		SSO: sso.Options{
			ClientID:    "test-eve-client",
			CallbackURL: "http://127.0.0.1/auth/callback",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	s, err := Open(Host{Listen: "127.0.0.1:8765"}, runtime, db)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestFinishMCPAttachesExistingOwner(t *testing.T) {
	db := openDB(t)
	ctx := context.Background()
	u, err := db.CreateUser(ctx)
	if err != nil {
		t.Fatal(err)
	}
	const charID int64 = 2112625428
	if err := db.UpsertCharacter(ctx, u.ID, store.CharacterRow{
		CharacterID: charID, Name: "Jane Doe", RefreshToken: "old-rt",
	}); err != nil {
		t.Fatal(err)
	}
	s := testServer(t, db)
	loc, err := s.finishMCP(ctx, &store.LoginState{
		MCPClientID: "c", RedirectURI: "http://localhost:1/cb",
		MCPState: "mcp", CodeChallenge: "x",
	}, &sso.CharacterToken{
		CharacterID: int(charID), CharacterName: "Jane Doe", RefreshToken: "new-rt",
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := url.Parse(loc)
	if err != nil || got.Query().Get("code") == "" {
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
	if row.RefreshToken != "new-rt" {
		t.Fatalf("refresh %s", row.RefreshToken)
	}
	if tok := s.SessionFor(u.ID).SSO.Store.Get(int(charID)); tok == nil {
		t.Fatal("session store miss")
	}
}

func TestFinishMCPCreatesUser(t *testing.T) {
	db := openDB(t)
	s := testServer(t, db)
	const charID int64 = 42
	loc, err := s.finishMCP(context.Background(), &store.LoginState{
		MCPClientID: "c", RedirectURI: "http://localhost:1/cb",
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
