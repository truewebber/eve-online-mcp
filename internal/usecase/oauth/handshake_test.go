package oauth

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/truewebber/eve-online-mcp/internal/domain/authcode"
	"github.com/truewebber/eve-online-mcp/internal/domain/character"
	dbsession "github.com/truewebber/eve-online-mcp/internal/domain/session"
)

func TestOpenHMACTooShort(t *testing.T) {
	t.Parallel()
	_, err := Open(Host{}, nil, Options{HMACKey: make([]byte, 16)}, nil)
	if !errors.Is(err, ErrHMACTooShort) {
		t.Fatalf("got %v", err)
	}
}

func TestHMACStableAcrossOpen(t *testing.T) {
	t.Parallel()
	db := openDB(t)
	s1 := testServer(t, db)
	s2 := testServer(t, db)
	const characterID int64 = 2112625428
	if err := characters(t, db).Upsert(context.Background(), character.Character{
		ID: characterID, Name: janeDoe, OwnerHash: "h",
	}); err != nil {
		t.Fatal(err)
	}
	row, err := sessions(t, db).Create(context.Background(), dbsession.Session{
		CharacterID: characterID, RefreshToken: "rt", Scopes: []string{},
		MCPClientID: "c",
	})
	if err != nil {
		t.Fatal(err)
	}
	tok, err := s1.issueAccess(int(characterID), row.ID)
	if err != nil {
		t.Fatal(err)
	}
	info, _, err := s2.verifyAccess(tok)
	if err != nil || info.UserID != "2112625428" {
		t.Fatalf("cross-process jwt: %+v err %v", info, err)
	}
}

func TestAuthorizePersistsMCPLoginState(t *testing.T) {
	t.Parallel()
	db := openDB(t)
	s := testServer(t, db)
	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, (&url.URL{
		Path: "/oauth/authorize",
		RawQuery: url.Values{
			paramClientID:    {"mcp-client"},
			paramRedirectURI: {redirect},
			"state":          {"mcp-state"},
			"code_challenge": {"E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM"},
		}.Encode(),
	}).String(), nil)
	s.ServeAuthorize(rec, req)
	if rec.Code != http.StatusFound {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	loc, err := rec.Result().Location()
	if err != nil {
		t.Fatal(err)
	}
	eveState := loc.Query().Get("state")
	if eveState == "" {
		t.Fatalf("location %s", loc)
	}
	st, err := logins(db).Get(context.Background(), eveState)
	if err != nil {
		t.Fatal(err)
	}
	if st.MCPClientID != "mcp-client" ||
		st.RedirectURI != redirect || st.MCPState != "mcp-state" ||
		st.CodeChallenge == "" || st.PKCEVerifier == "" {
		t.Fatalf("login state %+v", st)
	}
}

func TestCompleteCallbackUnknownState(t *testing.T) {
	t.Parallel()
	s := testServer(t, openDB(t))
	_, err := s.CompleteCallback(context.Background(), paramCode, "missing")
	if !errors.Is(err, ErrUnknownLogin) {
		t.Fatalf("got %v", err)
	}
}

func TestExchangeAuthCodeAgainstStore(t *testing.T) {
	t.Parallel()
	const (
		verifier  = "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"
		challenge = "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM"
	)
	db := openDB(t)
	ctx := context.Background()
	const characterID int64 = 2112625428
	if err := characters(t, db).Upsert(ctx, character.Character{
		ID: characterID, Name: janeDoe, OwnerHash: "h",
	}); err != nil {
		t.Fatal(err)
	}
	s := testServer(t, db)

	put := func(t *testing.T, code string, expires time.Time) {
		t.Helper()
		err := codes(db).Put(ctx, authcode.Code{
			Value: code, CharacterID: characterID, RefreshToken: "parked-rt",
			Scopes: []string{scopePublic}, MCPClientID: "c",
			RedirectURI: redirect, CodeChallenge: challenge, ExpiresAt: expires,
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	post := func(vals url.Values) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/oauth/token", strings.NewReader(vals.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		s.ServeToken(rec, req)

		return rec
	}

	cases := []struct {
		name   string
		setup  func(t *testing.T) url.Values
		status int
		check  func(t *testing.T, rec *httptest.ResponseRecorder)
	}{
		{
			name: "good pkce",
			setup: func(t *testing.T) url.Values {
				t.Helper()
				put(t, "good", time.Now().Add(2*time.Minute))

				return url.Values{
					paramGrantType:    {grantAuthCode},
					paramCode:         {"good"},
					paramCodeVerifier: {verifier},
					paramRedirectURI:  {redirect},
				}
			},
			status: 200,
			check: func(t *testing.T, rec *httptest.ResponseRecorder) {
				t.Helper()
				var payload map[string]any
				if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
					t.Fatal(err)
				}
				raw, ok := payload["access_token"].(string)
				if !ok || raw == "" {
					t.Fatalf("access_token %+v", payload)
				}
				info, ref, err := s.verifyAccess(raw)
				if err != nil || info.UserID != "2112625428" || ref.SessionID == 0 {
					t.Fatalf("jwt %+v ref %+v err %v", info, ref, err)
				}
			},
		},
		{
			name: "bad verifier",
			setup: func(t *testing.T) url.Values {
				t.Helper()
				put(t, "bad-v", time.Now().Add(2*time.Minute))

				return url.Values{
					paramGrantType:    {grantAuthCode},
					paramCode:         {"bad-v"},
					paramCodeVerifier: {"nope"},
					paramRedirectURI:  {redirect},
				}
			},
			status: 400,
		},
		{
			name: "wrong redirect",
			setup: func(t *testing.T) url.Values {
				t.Helper()
				put(t, "bad-r", time.Now().Add(2*time.Minute))

				return url.Values{
					paramGrantType:    {grantAuthCode},
					paramCode:         {"bad-r"},
					paramCodeVerifier: {verifier},
					paramRedirectURI:  {"http://localhost:9/other"},
				}
			},
			status: 400,
		},
		{
			name: "expired",
			setup: func(t *testing.T) url.Values {
				t.Helper()
				put(t, "old", time.Now().Add(-time.Minute))

				return url.Values{
					paramGrantType:    {grantAuthCode},
					paramCode:         {"old"},
					paramCodeVerifier: {verifier},
					paramRedirectURI:  {redirect},
				}
			},
			status: 400,
		},
		{
			name: "replay after take",
			setup: func(t *testing.T) url.Values {
				t.Helper()
				put(t, "once", time.Now().Add(2*time.Minute))
				ok := url.Values{
					paramGrantType:    {grantAuthCode},
					paramCode:         {"once"},
					paramCodeVerifier: {verifier},
					paramRedirectURI:  {redirect},
				}
				rec := post(ok)
				if rec.Code != 200 {
					t.Fatalf("first take %d %s", rec.Code, rec.Body.String())
				}

				return ok
			},
			status: 400,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			rec := post(tc.setup(t))
			if rec.Code != tc.status {
				t.Fatalf("status %d want %d body %s", rec.Code, tc.status, rec.Body.String())
			}
			if tc.check != nil {
				tc.check(t, rec)
			}
		})
	}
}
