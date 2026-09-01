package session

import (
	"context"
	"errors"
	nhttp "net/http"
	"strings"
	"testing"

	"go.uber.org/mock/gomock"

	"github.com/truewebber/eve-online-mcp/internal/adapter/esi"
	esihttp "github.com/truewebber/eve-online-mcp/internal/adapter/esi/http"
	"github.com/truewebber/eve-online-mcp/internal/adapter/sso"
	ssohttp "github.com/truewebber/eve-online-mcp/internal/adapter/sso/http"
	"github.com/truewebber/eve-online-mcp/internal/domain/character"
	characterpgx "github.com/truewebber/eve-online-mcp/internal/domain/character/pgx"
	confirmpgx "github.com/truewebber/eve-online-mcp/internal/domain/confirm/pgx"
	mutationpgx "github.com/truewebber/eve-online-mcp/internal/domain/mutation/pgx"
	dbsession "github.com/truewebber/eve-online-mcp/internal/domain/session"
	sessionpgx "github.com/truewebber/eve-online-mcp/internal/domain/session/pgx"
	"github.com/truewebber/eve-online-mcp/internal/postgres/pgtest"

	"github.com/truewebber/eve-online-mcp/internal/domain/write"
	"github.com/truewebber/eve-online-mcp/internal/mocks"
)

func TestGuardMailCapUsesMutations(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	logger := mocks.QuietLogger(gomock.NewController(t))
	db := pgtest.Open(t, logger)
	runtime, err := Open(Options{
		Characters: characterpgx.New(db.Pool(), logger),
		Sessions:   sessionpgx.New(db.Pool(), logger),
		Confirms:   confirmpgx.New(db.Pool()),
		Mutations:  mutationpgx.New(db.Pool()),
		ESI:        esihttp.New(esi.Options{}, nhttp.DefaultClient, logger),
		SSO:        ssohttp.New(sso.Options{}, nhttp.DefaultClient, logger),
		Logger:     logger,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.Characters.Upsert(ctx, character.Character{ID: 1, Name: "P", OwnerHash: "h"}); err != nil {
		t.Fatal(err)
	}
	row, err := runtime.Sessions.Create(ctx, dbsession.Session{
		CharacterID: 1, RefreshToken: "rt", Scopes: []string{}, MCPClientID: "c",
	})
	if err != nil {
		t.Fatal(err)
	}
	sess := runtime.ForCharacter(1, row.ID)
	scopes := write.Capabilities()["mail_send"].Scopes
	for range 5 {
		if _, err := sess.Guard.Authorize(ctx, "eve_mail_send", "mail_send", nil, nil, "", scopes); err != nil {
			t.Fatal(err)
		}
		sess.Guard.Record(ctx, "eve_mail_send", "mail_send", nil, "ok")
	}
	_, err = sess.Guard.Authorize(ctx, "eve_mail_send", "mail_send", nil, nil, "", scopes)
	var blocked write.BlockedError
	if !errors.As(err, &blocked) || !strings.Contains(blocked.Msg, "Mail budget exhausted") {
		t.Fatalf("sixth %v", err)
	}
}
