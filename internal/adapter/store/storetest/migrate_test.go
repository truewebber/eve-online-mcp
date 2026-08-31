package storetest

import (
	"context"
	"fmt"
	"slices"
	"testing"

	"github.com/jackc/pgx/v5"
	"go.uber.org/mock/gomock"

	"github.com/truewebber/eve-online-mcp/internal/adapter/store"
	"github.com/truewebber/eve-online-mcp/internal/mocks"
)

const listPublicTables = `
	SELECT tablename
	FROM pg_tables
	WHERE schemaname = 'public'
	ORDER BY 1`

func TestOpenDoesNotMigrate(t *testing.T) {
	t.Parallel()
	dsn := EmptyDatabase(t)
	ctx := context.Background()
	s, err := store.Open(ctx, dsn, mocks.QuietLogger(gomock.NewController(t)))
	if err != nil {
		t.Fatal(err)
	}
	s.Close()

	got, err := publicTables(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("Open applied schema: %v", got)
	}
}

func TestGooseAppliesFromEmpty(t *testing.T) {
	t.Parallel()
	dsn := EmptyDatabase(t)
	ctx := context.Background()
	if err := apply(ctx, dsn); err != nil {
		t.Fatal(err)
	}
	if err := apply(ctx, dsn); err != nil {
		t.Fatal(err)
	}

	got, err := publicTables(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"auth_codes",
		"characters",
		"confirm_tokens",
		"goose_db_version",
		"login_states",
		"mail_log",
		"oauth_clients",
		"users",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("tables %v", got)
	}
}

func publicTables(ctx context.Context, databaseURL string) ([]string, error) {
	conn, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		return nil, fmt.Errorf("storetest: open: %w", err)
	}
	names, err := collectPublicTables(ctx, conn)
	if cerr := conn.Close(ctx); cerr != nil && err == nil {
		return nil, fmt.Errorf("storetest: close: %w", cerr)
	}

	return names, err
}

func collectPublicTables(ctx context.Context, conn *pgx.Conn) ([]string, error) {
	rows, err := conn.Query(ctx, listPublicTables)
	if err != nil {
		return nil, fmt.Errorf("storetest: tables: %w", err)
	}
	names, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, fmt.Errorf("storetest: tables: %w", err)
	}

	return names, nil
}
