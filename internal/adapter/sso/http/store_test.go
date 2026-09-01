package http

import (
	"context"
	"errors"
	"testing"
	"time"

	"go.uber.org/mock/gomock"

	"github.com/truewebber/eve-online-mcp/internal/adapter/sso"
	"github.com/truewebber/eve-online-mcp/internal/postgres"
	"github.com/truewebber/eve-online-mcp/internal/postgres/pgtest"

	"github.com/truewebber/eve-online-mcp/internal/domain/character"
	characterpgx "github.com/truewebber/eve-online-mcp/internal/domain/character/pgx"
	"github.com/truewebber/eve-online-mcp/internal/mocks"
)

const testRefreshToken = "rt-1"

func openStore(t *testing.T) *postgres.DB {
	t.Helper()

	return pgtest.Open(t, mocks.QuietLogger(gomock.NewController(t)))
}

func openChars(t *testing.T, db *postgres.DB) character.Repository {
	t.Helper()

	return characterpgx.New(db.Pool(), mocks.QuietLogger(gomock.NewController(t)))
}

func TestTokenStorePersistsRefreshNotAccess(t *testing.T) {
	t.Parallel()
	db := openStore(t)
	chars := openChars(t, db)
	ctx := context.Background()
	const id = 2112625428
	base := New(sso.Options{}, nil, mocks.QuietLogger(gomock.NewController(t)))
	ts := base.ForCharacter(id, chars)
	tok := &sso.CharacterToken{
		CharacterID:     id,
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

	fresh := base.ForCharacter(id, chars)
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

func TestTokenStoreIsOneCharacter(t *testing.T) {
	t.Parallel()
	db := openStore(t)
	chars := openChars(t, db)
	ctx := context.Background()
	base := New(sso.Options{}, nil, mocks.QuietLogger(gomock.NewController(t)))
	tok := &sso.CharacterToken{
		CharacterID: 99, CharacterName: "Lock", RefreshToken: "rt",
	}
	if err := base.ForCharacter(99, chars).Upsert(ctx, tok); err != nil {
		t.Fatal(err)
	}
	if base.ForCharacter(100, chars).Get(ctx, 99) != nil {
		t.Fatal("other character store must not see this grant")
	}
}

func TestBrokerStoreStaysInMemory(t *testing.T) {
	t.Parallel()
	db := openStore(t)
	chars := openChars(t, db)
	ctx := context.Background()
	broker := New(sso.Options{}, nil, mocks.QuietLogger(gomock.NewController(t))).ForCharacter(0, chars)
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
