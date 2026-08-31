package pgx

import (
	"context"
	"errors"
	"testing"

	"github.com/truewebber/eve-online-mcp/internal/adapter/store/storetest"
	"github.com/truewebber/eve-online-mcp/internal/domain/confirm"
	"github.com/truewebber/eve-online-mcp/internal/logtest"
)

const (
	toolWaypoint = "eve_ui_set_waypoint"
	toolMail     = "eve_mail_send"
)

func openRepo(t *testing.T) *Repo {
	t.Helper()

	return New(storetest.Open(t, logtest.Silent{}).Pool())
}

func TestPutGetDelete(t *testing.T) {
	t.Parallel()
	repo := openRepo(t)
	ctx := context.Background()
	if err := repo.Put(ctx, confirm.Confirm{
		Value: "peek", UserID: "u", Tool: toolWaypoint, ArgsDigest: "ab",
	}); err != nil {
		t.Fatal(err)
	}
	got, err := repo.Get(ctx, "peek")
	if err != nil || got.Tool != toolWaypoint {
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
	repo := openRepo(t)
	ctx := context.Background()
	if err := repo.Put(ctx, confirm.Confirm{
		Value: "fresh", UserID: "u", Tool: toolMail, ArgsDigest: "deadbeef",
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
		Value: "old", UserID: "u", Tool: toolWaypoint, ArgsDigest: "x",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.pool.Exec(ctx, `UPDATE confirm_tokens SET created_at = now() - interval '10 minutes' WHERE token = 'old'`); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Take(ctx, "old"); !errors.Is(err, confirm.ErrNotFound) {
		t.Fatalf("expired token honoured: %v", err)
	}
}

func TestCountAndDeleteExpired(t *testing.T) {
	t.Parallel()
	repo := openRepo(t)
	ctx := context.Background()
	if err := repo.Put(ctx, confirm.Confirm{
		Value: "a", UserID: "u", Tool: "t", ArgsDigest: "d",
	}); err != nil {
		t.Fatal(err)
	}
	n, err := repo.Count(ctx, "u")
	if err != nil || n != 1 {
		t.Fatalf("count %d %v", n, err)
	}
	if _, err := repo.pool.Exec(ctx, `UPDATE confirm_tokens SET created_at = now() - interval '10 minutes' WHERE token = 'a'`); err != nil {
		t.Fatal(err)
	}
	purged, err := repo.DeleteExpired(ctx)
	if err != nil || purged < 1 {
		t.Fatalf("purge %d %v", purged, err)
	}
}
