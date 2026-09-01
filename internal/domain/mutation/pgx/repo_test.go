package pgx

import (
	"testing"
	"time"

	"go.uber.org/mock/gomock"

	"github.com/truewebber/eve-online-mcp/internal/domain/mutation"
	"github.com/truewebber/eve-online-mcp/internal/mocks"
	"github.com/truewebber/eve-online-mcp/internal/postgres/pgtest"
)

func TestMailLog(t *testing.T) {
	t.Parallel()
	repo := New(pgtest.Open(t, mocks.QuietLogger(gomock.NewController(t))).Pool())
	ctx := t.Context()
	now := time.Now().UTC()
	const characterID int64 = 2112000001
	if err := repo.Insert(ctx, mutation.Mail{CharacterID: characterID, SentAt: now.Add(-2 * time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if err := repo.Insert(ctx, mutation.Mail{CharacterID: characterID, SentAt: now}); err != nil {
		t.Fatal(err)
	}
	n, err := repo.CountSince(ctx, characterID, now.Add(-time.Hour))
	if err != nil || n != 1 {
		t.Fatalf("count %d %v", n, err)
	}
}
