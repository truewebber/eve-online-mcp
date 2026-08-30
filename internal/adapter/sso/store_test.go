package sso

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"eve-mcp/internal/adapter/store"
)

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
		RefreshToken:    "rt-1",
		Scopes:          []string{"publicData"},
		OwnerHash:       "hash",
		AccessToken:     "at-mem",
		AccessExpiresAt: time.Now().Add(time.Hour),
	}
	if err := ts.Upsert(tok); err != nil {
		t.Fatal(err)
	}
	got := ts.Get(tok.CharacterID)
	if got == nil || got.RefreshToken != "rt-1" || got.AccessToken != "at-mem" {
		t.Fatalf("same process got %+v", got)
	}

	fresh := newTokenStore(db, u.ID)
	got = fresh.Get(tok.CharacterID)
	if got == nil || got.RefreshToken != "rt-1" {
		t.Fatalf("reload %+v", got)
	}
	if got.AccessToken != "" {
		t.Fatalf("access token must not be durable, got %q", got.AccessToken)
	}
	all := fresh.All()
	if len(all) != 1 || all[0].CharacterName != "Jane Doe" {
		t.Fatalf("all %v", all)
	}
	if !fresh.Remove(tok.CharacterID) {
		t.Fatal("remove")
	}
	if fresh.Get(tok.CharacterID) != nil {
		t.Fatal("still there")
	}
}

func TestTokenStoreRejectsOtherOwner(t *testing.T) {
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
	if err := newTokenStore(db, a.ID).Upsert(tok); err != nil {
		t.Fatal(err)
	}
	err = newTokenStore(db, b.ID).Upsert(tok)
	if !errors.Is(err, store.ErrOwned) {
		t.Fatalf("want ErrOwned, got %v", err)
	}
	if newTokenStore(db, b.ID).Get(99) != nil {
		t.Fatal("other user must not see the character")
	}
}

func TestBrokerStoreStaysInMemory(t *testing.T) {
	db := openStore(t)
	broker := newTokenStore(db, "")
	tok := &CharacterToken{CharacterID: 7, CharacterName: "Broker", RefreshToken: "rt"}
	if err := broker.Upsert(tok); err != nil {
		t.Fatal(err)
	}
	_, ok, err := db.OwnerOf(context.Background(), 7)
	if err != nil || ok {
		t.Fatalf("broker must not persist, ok=%v err=%v", ok, err)
	}
	if broker.Get(7) == nil {
		t.Fatal("broker memory miss")
	}
}
