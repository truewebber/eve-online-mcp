package pgx

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"go.uber.org/mock/gomock"

	"github.com/truewebber/eve-online-mcp/internal/domain/mutation"
	"github.com/truewebber/eve-online-mcp/internal/mocks"
	"github.com/truewebber/eve-online-mcp/internal/postgres/pgtest"
)

const (
	characterID int64 = 2112000019
	capMailSend       = "mail_send"
	ageSQL            = `UPDATE mutations SET created_at = now() - interval '2 hours' WHERE id = $1`
	listSQL           = `
		SELECT tool, summary, outcome, esi_status, error
		FROM mutations WHERE character_id = $1 ORDER BY id`
	firstIDSQL = `SELECT id FROM mutations WHERE character_id = $1 ORDER BY id LIMIT 1`
)

var errOverCap = errors.New("mail cap")

func openRepo(t *testing.T) *Repo {
	t.Helper()
	db := pgtest.Open(t, mocks.QuietLogger(gomock.NewController(t)))
	ctx := t.Context()
	if _, err := db.Pool().Exec(ctx, `
		INSERT INTO characters (character_id, name, owner_hash) VALUES ($1, 'P', 'h')`, characterID); err != nil {
		t.Fatal(err)
	}

	return New(db.Pool())
}

func appendOK(t *testing.T, repo *Repo, summary string) {
	t.Helper()
	if err := repo.Append(t.Context(), mutation.Mutation{
		CharacterID: characterID, Tool: mutation.ToolMailSend, Capability: capMailSend,
		ArgsDigest: "deadbeefdeadbeef", Summary: summary, Outcome: mutation.OutcomeOK,
	}); err != nil {
		t.Fatal(err)
	}
}

func TestMailCapCountsOnlyRecentOK(t *testing.T) {
	t.Parallel()
	repo := openRepo(t)
	ctx := t.Context()
	appendOK(t, repo, "mail to 1 recipients, subject 'now'")
	if err := repo.Append(ctx, mutation.Mutation{
		CharacterID: characterID, Tool: mutation.ToolMailSend, Capability: capMailSend,
		ArgsDigest: "errdigest0000000", Summary: "mail to 1 recipients, subject 'fail'",
		Outcome: mutation.OutcomeError, ESIStatus: 520, Error: "ESI 520 on /mail",
	}); err != nil {
		t.Fatal(err)
	}
	appendOK(t, repo, "mail to 1 recipients, subject 'old'")
	var firstID int64
	if err := repo.pool.QueryRow(ctx, firstIDSQL, characterID).Scan(&firstID); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.pool.Exec(ctx, ageSQL, firstID); err != nil {
		t.Fatal(err)
	}
	n, err := repo.CountMailCap(ctx, characterID)
	if err != nil || n != 1 {
		t.Fatalf("count %d %v", n, err)
	}
}

func TestAppendDoesNotStoreBody(t *testing.T) {
	t.Parallel()
	repo := openRepo(t)
	ctx := t.Context()
	const body = "do not store this body"
	if err := repo.Append(ctx, mutation.Mutation{
		CharacterID: characterID, Tool: mutation.ToolMailSend, Capability: capMailSend,
		ArgsDigest: "digest16bytesxxx", Summary: "mail to 2 recipients, subject 'Fleet tonight'",
		Outcome: mutation.OutcomeOK,
	}); err != nil {
		t.Fatal(err)
	}
	var tool, summary, outcome string
	var status *int
	var errText *string
	if err := repo.pool.QueryRow(ctx, listSQL, characterID).Scan(&tool, &summary, &outcome, &status, &errText); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(summary, body) || (errText != nil && strings.Contains(*errText, body)) {
		t.Fatalf("body leaked summary=%q error=%v", summary, errText)
	}
	if !strings.Contains(summary, "Fleet tonight") {
		t.Fatalf("summary %q", summary)
	}
	if tool != mutation.ToolMailSend || outcome != mutation.OutcomeOK || status != nil {
		t.Fatalf("row %s %s status=%v", tool, outcome, status)
	}
}

func TestHoldSerializesFifthMail(t *testing.T) {
	t.Parallel()
	repo := openRepo(t)
	ctx := t.Context()
	for range 4 {
		appendOK(t, repo, "mail to 1 recipients, subject 'prior'")
	}
	var wg sync.WaitGroup
	errc := make(chan error, 2)
	for range 2 {
		wg.Go(func() {
			hold, err := repo.HoldMailCap(ctx, characterID)
			if err != nil {
				errc <- err

				return
			}
			if hold.Count >= 5 {
				if relErr := hold.Release(errOverCap); relErr != nil {
					errc <- relErr

					return
				}
				errc <- errOverCap

				return
			}
			err = hold.Do(func(ctx context.Context) error {
				return repo.Append(ctx, mutation.Mutation{
					CharacterID: characterID, Tool: mutation.ToolMailSend, Capability: capMailSend,
					ArgsDigest: "race16bytesxxxxx", Summary: "mail to 1 recipients, subject 'fifth'",
					Outcome: mutation.OutcomeOK,
				})
			})
			if relErr := hold.Release(err); err == nil {
				err = relErr
			}
			errc <- err
		})
	}
	wg.Wait()
	close(errc)
	var sent, blocked int
	for err := range errc {
		switch {
		case err == nil:
			sent++
		case errors.Is(err, errOverCap):
			blocked++
		default:
			t.Fatal(err)
		}
	}
	if sent != 1 || blocked != 1 {
		t.Fatalf("sent %d blocked %d", sent, blocked)
	}
	n, err := repo.CountMailCap(ctx, characterID)
	if err != nil || n != 5 {
		t.Fatalf("after %d %v", n, err)
	}
}
