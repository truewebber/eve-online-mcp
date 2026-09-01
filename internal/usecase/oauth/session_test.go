package oauth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.uber.org/mock/gomock"

	"github.com/truewebber/eve-online-mcp/internal/adapter/sso"
	"github.com/truewebber/eve-online-mcp/internal/domain/authcode"
	"github.com/truewebber/eve-online-mcp/internal/domain/character"
	dbsession "github.com/truewebber/eve-online-mcp/internal/domain/session"
	"github.com/truewebber/eve-online-mcp/internal/mocks"
	"github.com/truewebber/eve-online-mcp/internal/postgres"
)

const (
	pkceVerifier  = "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"
	pkceChallenge = "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM"
)

func postToken(ctx context.Context, s *Server, vals url.Values) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/oauth/token", strings.NewReader(vals.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	s.ServeToken(rec, req)

	return rec
}

func tokenPayload(t *testing.T, payload map[string]any) string {
	t.Helper()
	raw, ok := payload["access_token"].(string)
	if !ok || raw == "" {
		t.Fatalf("access_token %+v", payload)
	}

	return raw
}

func exchangeOK(t *testing.T, s *Server, db *postgres.DB, characterID int64, code, refresh string) map[string]any {
	t.Helper()
	ctx := context.Background()
	if err := codes(db).Put(ctx, authcode.Code{
		Value: code, CharacterID: characterID, RefreshToken: refresh,
		Scopes: []string{scopePublic}, MCPClientID: "c",
		RedirectURI: redirect, CodeChallenge: pkceChallenge,
		ExpiresAt: time.Now().Add(2 * time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	rec := postToken(ctx, s, url.Values{
		paramGrantType:    {grantAuthCode},
		paramCode:         {code},
		paramCodeVerifier: {pkceVerifier},
		paramRedirectURI:  {redirect},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("exchange %s: %d %s", code, rec.Code, rec.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}

	return payload
}

func TestConcurrentExchangesReplace(t *testing.T) {
	t.Parallel()
	db := openDB(t)
	ctx := context.Background()
	const characterID int64 = 501
	if err := characters(t, db).Upsert(ctx, character.Character{
		ID: characterID, Name: janeDoe, OwnerHash: "h",
	}); err != nil {
		t.Fatal(err)
	}
	var revoked atomic.Int64
	ctrl := gomock.NewController(t)
	m := mocks.NewMockSSOClient(ctrl)
	m.EXPECT().PrepareLogin(gomock.Any()).Return(&sso.PreparedLogin{}, nil).AnyTimes()
	m.EXPECT().AccessToken(gomock.Any(), gomock.Any()).Return(&sso.CharacterToken{AccessToken: "at"}, nil).AnyTimes()
	m.EXPECT().Revoke(gomock.Any(), gomock.Any()).Do(func(_ context.Context, _ string) {
		revoked.Add(1)
	}).AnyTimes()
	s := testServerSSO(t, db, m)

	var wg sync.WaitGroup
	errc := make(chan int, 2)
	for i, spec := range []struct{ code, rt string }{
		{"ex-a", "rt-a"},
		{"ex-b", "rt-b"},
	} {
		wg.Add(1)
		go func(code, rt string) {
			defer wg.Done()
			if err := codes(db).Put(ctx, authcode.Code{
				Value: code, CharacterID: characterID, RefreshToken: rt,
				Scopes: []string{scopePublic}, MCPClientID: "c",
				RedirectURI: redirect, CodeChallenge: pkceChallenge,
				ExpiresAt: time.Now().Add(2 * time.Minute),
			}); err != nil {
				errc <- 0

				return
			}
			rec := postToken(ctx, s, url.Values{
				paramGrantType:    {grantAuthCode},
				paramCode:         {code},
				paramCodeVerifier: {pkceVerifier},
				paramRedirectURI:  {redirect},
			})
			errc <- rec.Code
		}(spec.code, spec.rt)
		_ = i
	}
	wg.Wait()
	close(errc)
	for code := range errc {
		if code != http.StatusOK && code != 0 {
			t.Fatalf("status %d", code)
		}
		if code == 0 {
			t.Fatal("put failed")
		}
	}
	live, err := s.runtime.Sessions.LiveByID(ctx, liveSID(t, db, characterID))
	if err != nil {
		t.Fatal(err)
	}
	if live.RefreshToken != "rt-a" && live.RefreshToken != "rt-b" {
		t.Fatalf("live grant %q", live.RefreshToken)
	}
}

func TestRevokedSIDIsUnauthorized(t *testing.T) {
	t.Parallel()
	db := openDB(t)
	ctx := context.Background()
	const characterID int64 = 502
	if err := characters(t, db).Upsert(ctx, character.Character{
		ID: characterID, Name: janeDoe, OwnerHash: "h",
	}); err != nil {
		t.Fatal(err)
	}
	s := testServer(t, db)
	payload := exchangeOK(t, s, db, characterID, "kick", "rt-kick")
	raw := tokenPayload(t, payload)
	info, ref, err := s.verifyAccess(raw)
	if err != nil || info == nil {
		t.Fatalf("verify %v", err)
	}
	if _, err := s.runtime.Sessions.Revoke(ctx, ref.SessionID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.VerifyAccess(ctx, raw, nil); err == nil {
		t.Fatal("want 401 after revoke")
	}
}

func TestExpiredUnrevokedDoesNotBlockSignIn(t *testing.T) {
	t.Parallel()
	db := openDB(t)
	ctx := context.Background()
	const characterID int64 = 503
	if err := characters(t, db).Upsert(ctx, character.Character{
		ID: characterID, Name: janeDoe, OwnerHash: "h",
	}); err != nil {
		t.Fatal(err)
	}
	old, err := sessions(t, db).Create(ctx, dbsession.Session{
		CharacterID: characterID, RefreshToken: "old-rt", Scopes: []string{},
		MCPClientID: "c",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool().Exec(ctx, `UPDATE sessions SET valid_til = now() - interval '1 day' WHERE id = $1`, old.ID); err != nil {
		t.Fatal(err)
	}
	s := testServer(t, db)
	payload := exchangeOK(t, s, db, characterID, "day31", "new-rt")
	_, ref, err := s.verifyAccess(tokenPayload(t, payload))
	if err != nil {
		t.Fatal(err)
	}
	if ref.SessionID == old.ID {
		t.Fatal("new sign-in reused expired sid")
	}
	live, err := s.runtime.Sessions.LiveByID(ctx, ref.SessionID)
	if err != nil || live.RefreshToken != "new-rt" {
		t.Fatalf("live %+v err %v", live, err)
	}
}

func TestRuntimeRebuildsGrantOnNewSID(t *testing.T) {
	t.Parallel()
	db := openDB(t)
	ctx := context.Background()
	const characterID int64 = 504
	if err := characters(t, db).Upsert(ctx, character.Character{
		ID: characterID, Name: janeDoe, OwnerHash: "h",
	}); err != nil {
		t.Fatal(err)
	}
	s := testServer(t, db)
	first := exchangeOK(t, s, db, characterID, "sid-a", "rt-a")
	_, a, err := s.verifyAccess(tokenPayload(t, first))
	if err != nil {
		t.Fatal(err)
	}
	runtimeA := s.SessionFor(int(characterID), a.SessionID)
	if runtimeA.SessionID != a.SessionID {
		t.Fatalf("sid %d", runtimeA.SessionID)
	}
	second := exchangeOK(t, s, db, characterID, "sid-b", "rt-b")
	_, b, err := s.verifyAccess(tokenPayload(t, second))
	if err != nil {
		t.Fatal(err)
	}
	runtimeB := s.SessionFor(int(characterID), b.SessionID)
	if runtimeB.SessionID != b.SessionID {
		t.Fatalf("rebuilt sid %d want %d", runtimeB.SessionID, b.SessionID)
	}
	if runtimeA.SessionID != b.SessionID {
		t.Fatal("cached runtime must rebuild in place")
	}
}

func TestCCPRevokeAfterCommit(t *testing.T) {
	t.Parallel()
	db := openDB(t)
	ctx := context.Background()
	const characterID int64 = 505
	if err := characters(t, db).Upsert(ctx, character.Character{
		ID: characterID, Name: janeDoe, OwnerHash: "h",
	}); err != nil {
		t.Fatal(err)
	}
	seen := make(chan string, 1)
	ctrl := gomock.NewController(t)
	m := mocks.NewMockSSOClient(ctrl)
	m.EXPECT().PrepareLogin(gomock.Any()).Return(&sso.PreparedLogin{}, nil).AnyTimes()
	m.EXPECT().AccessToken(gomock.Any(), gomock.Any()).Return(&sso.CharacterToken{AccessToken: "at"}, nil).AnyTimes()
	m.EXPECT().Revoke(gomock.Any(), gomock.Any()).Do(func(_ context.Context, tok string) {
		var n int
		if err := db.Pool().QueryRow(ctx, `
			SELECT count(*) FROM sessions
			WHERE character_id = $1 AND revoked_at IS NULL`, characterID).Scan(&n); err != nil || n != 1 {
			t.Errorf("revoke before commit: n=%d err=%v", n, err)
		}
		seen <- tok
	}).AnyTimes()
	s := testServerSSO(t, db, m)
	exchangeOK(t, s, db, characterID, "first", "rt-first")
	exchangeOK(t, s, db, characterID, "second", "rt-second")
	select {
	case tok := <-seen:
		if tok != "rt-first" {
			t.Fatalf("revoked %q", tok)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("CCP revoke not observed")
	}
	if _, err := s.runtime.Sessions.LiveByID(ctx, liveSID(t, db, characterID)); err != nil {
		t.Fatalf("row must stay live after a failing-looking revoke: %v", err)
	}
}

func TestFailingCCPRevokeLeavesRowRevoked(t *testing.T) {
	t.Parallel()
	db := openDB(t)
	ctx := context.Background()
	const characterID int64 = 506
	if err := characters(t, db).Upsert(ctx, character.Character{
		ID: characterID, Name: janeDoe, OwnerHash: "h",
	}); err != nil {
		t.Fatal(err)
	}
	ctrl := gomock.NewController(t)
	m := mocks.NewMockSSOClient(ctrl)
	m.EXPECT().PrepareLogin(gomock.Any()).Return(&sso.PreparedLogin{}, nil).AnyTimes()
	m.EXPECT().AccessToken(gomock.Any(), gomock.Any()).Return(&sso.CharacterToken{AccessToken: "at"}, nil).AnyTimes()
	m.EXPECT().Revoke(gomock.Any(), gomock.Any()).Do(func(context.Context, string) {
		// CCP failed; we still dropped the predecessor locally.
	}).AnyTimes()
	s := testServerSSO(t, db, m)
	first := exchangeOK(t, s, db, characterID, "keep", "rt-keep")
	_, pred, err := s.verifyAccess(tokenPayload(t, first))
	if err != nil {
		t.Fatal(err)
	}
	exchangeOK(t, s, db, characterID, "next", "rt-next")
	if _, err := s.runtime.Sessions.LiveByID(ctx, pred.SessionID); err == nil {
		t.Fatal("predecessor must be revoked even if CCP revoke is a no-op")
	}
}

func liveSID(t *testing.T, db *postgres.DB, characterID int64) int64 {
	t.Helper()
	var id int64
	err := db.Pool().QueryRow(context.Background(), `
		SELECT id FROM sessions
		WHERE character_id = $1 AND revoked_at IS NULL`, characterID).Scan(&id)
	if err != nil {
		t.Fatal(err)
	}

	return id
}
