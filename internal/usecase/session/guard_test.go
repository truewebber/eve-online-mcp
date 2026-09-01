package session

import (
	"context"
	"errors"
	nhttp "net/http"
	"strings"
	"sync"
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
	"github.com/truewebber/eve-online-mcp/internal/domain/write"
	"github.com/truewebber/eve-online-mcp/internal/mocks"
	"github.com/truewebber/eve-online-mcp/internal/postgres/pgtest"
)

func authz(args map[string]any, token string, scopes []string) write.Authz {
	return write.Authz{Tool: write.ToolMailSend, Capability: write.CapMailSend, Args: args, Token: token, Scopes: scopes}
}

func openGuardRuntime(t *testing.T) (*Session, *Session) {
	t.Helper()
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

	return runtime, runtime.ForCharacter(1, row.ID)
}

func recordOK(t *testing.T, g *write.Guard, args map[string]any) {
	t.Helper()
	if err := g.Record(context.Background(), write.Record{
		Tool: write.ToolMailSend, Capability: write.CapMailSend, Args: args, Outcome: write.OutcomeOK,
	}); err != nil {
		t.Fatal(err)
	}
}

func TestGuardMailCapUsesMutations(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	_, sess := openGuardRuntime(t)
	g := sess.Guard
	scopes := write.Capabilities()[write.CapMailSend].Scopes
	for range 5 {
		if _, err := g.Authorize(ctx, authz(nil, "", scopes)); err != nil {
			t.Fatal(err)
		}
		recordOK(t, g, nil)
	}
	_, err := g.Authorize(ctx, authz(nil, "", scopes))
	var blocked write.BlockedError
	if !errors.As(err, &blocked) || !strings.Contains(blocked.Msg, "Mail budget exhausted") {
		t.Fatalf("sixth %v", err)
	}
}

func TestConcurrentFifthMailWriteBlocked(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	runtime, sess := openGuardRuntime(t)
	g := sess.Guard
	scopes := write.Capabilities()[write.CapMailSend].Scopes
	for range 4 {
		recordOK(t, g, nil)
	}
	preview := func(g *write.Guard) string {
		t.Helper()
		out, err := g.Authorize(ctx, authz(nil, "", scopes))
		if err != nil {
			t.Fatal(err)
		}
		token, ok := out.Required["confirm_token"].(string)
		if !ok || token == "" {
			t.Fatalf("preview %+v", out)
		}

		return token
	}
	g1 := runtime.ForCharacter(1, sess.SessionID).Guard
	g2 := runtime.ForCharacter(1, sess.SessionID).Guard
	t1, t2 := preview(g1), preview(g2)
	var wg sync.WaitGroup
	errc := make(chan error, 2)
	send := func(g *write.Guard, token string) {
		defer wg.Done()
		_, err := g.Authorize(ctx, authz(nil, token, scopes))
		if err != nil {
			errc <- err

			return
		}
		errc <- g.Record(ctx, write.Record{
			Tool: write.ToolMailSend, Capability: write.CapMailSend, Outcome: write.OutcomeOK,
		})
	}
	wg.Add(2)
	go send(g1, t1)
	go send(g2, t2)
	wg.Wait()
	close(errc)
	var sent, blocked int
	for err := range errc {
		var wb write.BlockedError
		switch {
		case err == nil:
			sent++
		case errors.As(err, &wb):
			blocked++
		default:
			t.Fatal(err)
		}
	}
	if sent != 1 || blocked != 1 {
		t.Fatalf("sent %d blocked %d", sent, blocked)
	}
}

func TestFailedESIWriteRecorded(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	runtime, sess := openGuardRuntime(t)
	if err := sess.Guard.Record(ctx, write.Record{
		Tool: "eve_ui_set_waypoint", Capability: "waypoint",
		Args:    map[string]any{"destination_id": 30000142},
		Outcome: write.OutcomeError, ESIStatus: 520, Error: "ESI 520 on /ui/autopilot/waypoint",
	}); err != nil {
		t.Fatal(err)
	}
	n, err := runtime.Mutations.CountMailCap(ctx, 1)
	if err != nil || n != 0 {
		t.Fatalf("waypoint counted as mail %d %v", n, err)
	}
}

func TestRefusalBeforeESINotRecorded(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	runtime, sess := openGuardRuntime(t)
	g := sess.Guard
	scopes := write.Capabilities()[write.CapMailSend].Scopes
	for range 5 {
		recordOK(t, g, nil)
	}
	_, err := g.Authorize(ctx, authz(map[string]any{
		"subject": "x", "body": "should not be stored",
	}, "", scopes))
	if !errors.As(err, new(write.BlockedError)) {
		t.Fatalf("want blocked, got %v", err)
	}
	n, err := runtime.Mutations.CountMailCap(ctx, 1)
	if err != nil || n != 5 {
		t.Fatalf("refusal inserted a row %d %v", n, err)
	}
}
