package sso

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/truewebber/eve-online-mcp/internal/adapter/store"
)

const testRefreshToken = "rt-1"

func openStore(t *testing.T) *store.Store {
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

func TestTokenStorePersistsRefreshNotAccess(t *testing.T) {
	t.Parallel()
	db := openStore(t)
	ctx := context.Background()
	u, err := db.CreateUser(ctx)
	if err != nil {
		t.Fatal(err)
	}
	ts := newTokenStore(db, u.ID)
	tok := &CharacterToken{
		CharacterID:     2112625428,
		CharacterName:   "Jane Doe",
		RefreshToken:    testRefreshToken,
		Scopes:          []string{"publicData"},
		OwnerHash:       "hash",
		AccessToken:     "at-mem",
		AccessExpiresAt: time.Now().Add(time.Hour),
	}
	if err := ts.Upsert(ctx, tok); err != nil {
		t.Fatal(err)
	}
	got := ts.Get(ctx, tok.CharacterID)
	if got == nil || got.RefreshToken != testRefreshToken || got.AccessToken != "at-mem" {
		t.Fatalf("same process got %+v", got)
	}

	fresh := newTokenStore(db, u.ID)
	got = fresh.Get(ctx, tok.CharacterID)
	if got == nil || got.RefreshToken != testRefreshToken {
		t.Fatalf("reload %+v", got)
	}
	if got.AccessToken != "" {
		t.Fatalf("access token must not be durable, got %q", got.AccessToken)
	}
	all := fresh.All(ctx)
	if len(all) != 1 || all[0].CharacterName != "Jane Doe" {
		t.Fatalf("all %v", all)
	}
	if !fresh.Remove(ctx, tok.CharacterID) {
		t.Fatal("remove")
	}
	if fresh.Get(ctx, tok.CharacterID) != nil {
		t.Fatal("still there")
	}
}

func TestTokenStoreRejectsOtherOwner(t *testing.T) {
	t.Parallel()
	db := openStore(t)
	ctx := context.Background()
	a, err := db.CreateUser(ctx)
	if err != nil {
		t.Fatal(err)
	}
	b, err := db.CreateUser(ctx)
	if err != nil {
		t.Fatal(err)
	}
	tok := &CharacterToken{
		CharacterID: 99, CharacterName: "Lock", RefreshToken: "rt",
	}
	if err := newTokenStore(db, a.ID).Upsert(ctx, tok); err != nil {
		t.Fatal(err)
	}
	err = newTokenStore(db, b.ID).Upsert(ctx, tok)
	if !errors.Is(err, store.ErrOwned) {
		t.Fatalf("want ErrOwned, got %v", err)
	}
	if newTokenStore(db, b.ID).Get(ctx, 99) != nil {
		t.Fatal("other user must not see the character")
	}
}

func TestBrokerStoreStaysInMemory(t *testing.T) {
	t.Parallel()
	db := openStore(t)
	ctx := context.Background()
	broker := newTokenStore(db, "")
	tok := &CharacterToken{CharacterID: 7, CharacterName: "Broker", RefreshToken: "rt"}
	if err := broker.Upsert(ctx, tok); err != nil {
		t.Fatal(err)
	}
	_, ok, err := db.OwnerOf(context.Background(), 7)
	if err != nil || ok {
		t.Fatalf("broker must not persist, ok=%v err=%v", ok, err)
	}
	if broker.Get(ctx, 7) == nil {
		t.Fatal("broker memory miss")
	}
}
