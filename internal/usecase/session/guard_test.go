package session

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/truewebber/eve-online-mcp/internal/adapter/store"
	"github.com/truewebber/eve-online-mcp/internal/domain/write"
	"github.com/truewebber/eve-online-mcp/internal/logtest"
)

func TestGuardMailCapUsesStore(t *testing.T) {
	t.Parallel()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL is unset; run `make postgres`")
	}
	ctx := context.Background()
	db, err := store.Open(ctx, dsn, logtest.Silent{})
	if err != nil {
		t.Fatal(err)
	}
	release, err := db.HoldTestLock(ctx)
	if err != nil {
		db.Close()
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	t.Cleanup(release)
	if err := db.ResetTables(ctx); err != nil {
		t.Fatal(err)
	}
	runtime, err := Open(Options{
		Store:  db,
		Logger: logtest.Silent{},
	})
	if err != nil {
		t.Fatal(err)
	}
	sess := runtime.ForUser("nobody")
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
