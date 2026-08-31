package storetest

import (
	"context"
	"crypto/rand"
	"fmt"
	"net/url"
	"os"
	"path"
	"testing"

	"github.com/jackc/pgx/v5"
)

func EmptyDatabase(tb testing.TB) string {
	tb.Helper()
	src := os.Getenv("DATABASE_URL")
	if src == "" {
		tb.Skip("DATABASE_URL is unset; run `make postgres` then `make migrate`")
	}

	u, err := url.Parse(src)
	if err != nil {
		tb.Fatalf("DATABASE_URL: %v", err)
	}
	if u.Host == "" {
		tb.Fatal("DATABASE_URL must be a URL with a host")
	}

	var id [8]byte
	if _, err := rand.Read(id[:]); err != nil {
		tb.Fatal(err)
	}
	name := fmt.Sprintf("eve_mcp_mig_%x", id)
	ident := pgx.Identifier{name}.Sanitize()

	ctx := context.Background()
	admin, err := pgx.Connect(ctx, src)
	if err != nil {
		tb.Fatal(err)
	}

	if _, err := admin.Exec(ctx, "CREATE DATABASE "+ident); err != nil {
		if cerr := admin.Close(ctx); cerr != nil {
			tb.Errorf("close admin: %v", cerr)
		}
		tb.Fatalf("create %s: %v", name, err)
	}

	next := *u
	next.Path = path.Join("/", name)

	tb.Cleanup(func() {
		dropCtx := context.Background()
		if _, err := admin.Exec(dropCtx, "DROP DATABASE "+ident+" WITH (FORCE)"); err != nil {
			tb.Errorf("drop %s: %v", name, err)
		}
		if err := admin.Close(dropCtx); err != nil {
			tb.Errorf("close admin: %v", err)
		}
	})

	return next.String()
}
