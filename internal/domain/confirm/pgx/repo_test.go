package pgx

import (
	"context"
	"errors"
	"testing"

	"go.uber.org/mock/gomock"

	"github.com/truewebber/eve-online-mcp/internal/postgres/pgtest"

	"github.com/truewebber/eve-online-mcp/internal/domain/confirm"
	"github.com/truewebber/eve-online-mcp/internal/mocks"
)

const (
	toolWaypoint = "eve_ui_set_waypoint"
	toolMail     = "eve_mail_send"
)

func openRepo(t *testing.T) (*Repo, int64) {
	t.Helper()
	db := pgtest.Open(t, mocks.QuietLogger(gomock.NewController(t)))
	ctx := context.Background()
	if _, err := db.Pool().Exec(ctx, `
		INSERT INTO characters (character_id, name, owner_hash) VALUES (1, 'P', 'h')`); err != nil {
		t.Fatal(err)
	}
	var sid int64
	if err := db.Pool().QueryRow(ctx, `
		INSERT INTO sessions (character_id, refresh_token, scopes, mcp_client_id, valid_til)
		VALUES (1, 'rt', '{}', 'c', now() + interval '30 days')
		RETURNING id`).Scan(&sid); err != nil {
		t.Fatal(err)
	}

	return New(db.Pool()), sid
}

func TestPutGetDelete(t *testing.T) {
	t.Parallel()
	repo, sid := openRepo(t)
	ctx := context.Background()
	if err := repo.Put(ctx, confirm.Confirm{
		Value: "peek", SessionID: sid, Tool: toolWaypoint, ArgsDigest: "ab",
	}); err != nil {
		t.Fatal(err)
	}
	got, err := repo.Get(ctx, "peek")
	if err != nil || got.Tool != toolWaypoint || got.SessionID != sid {
		t.Fatalf("get %+v err %v", got, err)
	}
	if err := repo.Delete(ctx, "peek"); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Get(ctx, "peek"); !errors.Is(err, confirm.ErrNotFound) {
		t.Fatalf("deleted still there: %v", err)
	}
}

func TestTakeOnceAndExpiry(t *testing.T) {
	t.Parallel()
	repo, sid := openRepo(t)
	ctx := context.Background()
	if err := repo.Put(ctx, confirm.Confirm{
		Value: "fresh", SessionID: sid, Tool: toolMail, ArgsDigest: "deadbeef",
	}); err != nil {
		t.Fatal(err)
	}
	got, err := repo.Take(ctx, "fresh")
	if err != nil || got.Tool != toolMail {
		t.Fatalf("take %+v err %v", got, err)
	}
	if _, err := repo.Take(ctx, "fresh"); !errors.Is(err, confirm.ErrNotFound) {
		t.Fatalf("second take %v", err)
	}
	if err := repo.Put(ctx, confirm.Confirm{
		Value: "old", SessionID: sid, Tool: toolWaypoint, ArgsDigest: "x",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.pool.Exec(ctx, `UPDATE confirm_tokens SET expires_at = now() - interval '1 minute' WHERE token = 'old'`); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Take(ctx, "old"); !errors.Is(err, confirm.ErrNotFound) {
		t.Fatalf("expired token honoured: %v", err)
	}
}

func TestCountAndDeleteExpired(t *testing.T) {
	t.Parallel()
	repo, sid := openRepo(t)
	ctx := context.Background()
	if err := repo.Put(ctx, confirm.Confirm{
		Value: "a", SessionID: sid, Tool: "t", ArgsDigest: "d",
	}); err != nil {
		t.Fatal(err)
	}
	n, err := repo.Count(ctx, sid)
	if err != nil || n != 1 {
		t.Fatalf("count %d %v", n, err)
	}
	if _, err := repo.pool.Exec(ctx, `UPDATE confirm_tokens SET expires_at = now() - interval '1 minute' WHERE token = 'a'`); err != nil {
		t.Fatal(err)
	}
	purged, err := repo.DeleteExpired(ctx)
	if err != nil || purged < 1 {
		t.Fatalf("purge %d %v", purged, err)
	}
}
