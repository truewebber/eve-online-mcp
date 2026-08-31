package http

import (
	"context"
	"errors"
	"testing"
	"time"

	"go.uber.org/mock/gomock"

	"github.com/truewebber/eve-online-mcp/internal/adapter/sso"
	"github.com/truewebber/eve-online-mcp/internal/adapter/store"
	"github.com/truewebber/eve-online-mcp/internal/adapter/store/storetest"

	"github.com/truewebber/eve-online-mcp/internal/domain/character"
	characterpgx "github.com/truewebber/eve-online-mcp/internal/domain/character/pgx"
	"github.com/truewebber/eve-online-mcp/internal/mocks"
)

const testRefreshToken = "rt-1"

func openStore(t *testing.T) *store.Store {
	t.Helper()

	return storetest.Open(t, mocks.QuietLogger(gomock.NewController(t)))
}

func openChars(t *testing.T, db *store.Store) character.Repository {
	t.Helper()

	return characterpgx.New(db.Pool(), mocks.QuietLogger(gomock.NewController(t)))
}

func TestTokenStorePersistsRefreshNotAccess(t *testing.T) {
	t.Parallel()
	db := openStore(t)
	chars := openChars(t, db)
	ctx := context.Background()
	u, err := db.CreateUser(ctx)
	if err != nil {
		t.Fatal(err)
	}
	base := New(sso.Options{}, nil, mocks.QuietLogger(gomock.NewController(t)))
	ts := base.ForUser(u.ID, chars)
	tok := &sso.CharacterToken{
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

	fresh := base.ForUser(u.ID, chars)
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
	chars := openChars(t, db)
	ctx := context.Background()
	a, err := db.CreateUser(ctx)
	if err != nil {
		t.Fatal(err)
	}
	b, err := db.CreateUser(ctx)
	if err != nil {
		t.Fatal(err)
	}
	base := New(sso.Options{}, nil, mocks.QuietLogger(gomock.NewController(t)))
	tok := &sso.CharacterToken{
		CharacterID: 99, CharacterName: "Lock", RefreshToken: "rt",
	}
	if err := base.ForUser(a.ID, chars).Upsert(ctx, tok); err != nil {
		t.Fatal(err)
	}
	err = base.ForUser(b.ID, chars).Upsert(ctx, tok)
	if !errors.Is(err, character.ErrOwned) {
		t.Fatalf("want ErrOwned, got %v", err)
	}
	if base.ForUser(b.ID, chars).Get(ctx, 99) != nil {
		t.Fatal("other user must not see the character")
	}
}

func TestBrokerStoreStaysInMemory(t *testing.T) {
	t.Parallel()
	db := openStore(t)
	chars := openChars(t, db)
	ctx := context.Background()
	broker := New(sso.Options{}, nil, mocks.QuietLogger(gomock.NewController(t))).ForUser("", chars)
	tok := &sso.CharacterToken{CharacterID: 7, CharacterName: "Broker", RefreshToken: "rt"}
	if err := broker.Upsert(ctx, tok); err != nil {
		t.Fatal(err)
	}
	_, err := chars.Get(ctx, 7)
	if !errors.Is(err, character.ErrNotFound) {
		t.Fatalf("broker must not persist, err=%v", err)
	}
	if broker.Get(ctx, 7) == nil {
		t.Fatal("broker memory miss")
	}
}
