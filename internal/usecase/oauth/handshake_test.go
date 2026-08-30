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

	"github.com/truewebber/eve-online-mcp/internal/adapter/sso"
	"github.com/truewebber/eve-online-mcp/internal/adapter/store"
)

func TestHMACStableAcrossOpen(t *testing.T) {
	db := openDB(t)
	s1 := testServer(t, db)
	s2 := testServer(t, db)
	u, err := db.CreateUser(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	tok, err := s1.issueAccess(u.ID)
	if err != nil {
		t.Fatal(err)
	}
	info, err := s2.verifyAccess(tok)
	if err != nil || info.UserID != u.ID {
		t.Fatalf("cross-process jwt: %+v err %v", info, err)
	}
}

func TestStartAltLoginRecordsUserID(t *testing.T) {
	db := openDB(t)
	ctx := context.Background()
	u, err := db.CreateUser(ctx)
	if err != nil {
		t.Fatal(err)
	}
	s := testServer(t, db)
	loginURL, state, err := s.SessionFor(u.ID).StartAltLogin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(loginURL, "login.eveonline.com") || state == "" {
		t.Fatalf("url %s state %s", loginURL, state)
	}
	st, ok, err := db.GetLoginState(ctx, state)
	if err != nil || !ok {
		t.Fatalf("ok %v err %v", ok, err)
	}
	if st.Kind != store.LoginAlt || st.UserID != u.ID || st.PKCEVerifier == "" {
		t.Fatalf("login state %+v", st)
	}
}

func TestAuthorizePersistsMCPLoginState(t *testing.T) {
	db := openDB(t)
	s := testServer(t, db)
	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/oauth/authorize?"+url.Values{
		"client_id":      {"mcp-client"},
		"redirect_uri":   {"http://localhost:1/cb"},
		"state":          {"mcp-state"},
		"code_challenge": {"E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM"},
	}.Encode(), nil)
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
	st, ok, err := db.GetLoginState(context.Background(), eveState)
	if err != nil || !ok {
		t.Fatalf("ok %v err %v", ok, err)
	}
	if st.Kind != store.LoginMCP || st.UserID != "" || st.MCPClientID != "mcp-client" ||
		st.RedirectURI != "http://localhost:1/cb" || st.MCPState != "mcp-state" ||
		st.CodeChallenge == "" || st.PKCEVerifier == "" {
		t.Fatalf("login state %+v", st)
	}
}

func TestCompleteCallbackUnknownState(t *testing.T) {
	s := testServer(t, openDB(t))
	_, _, err := s.CompleteCallback(context.Background(), "code", "missing")
	if !errors.Is(err, ErrUnknownLogin) {
		t.Fatalf("got %v", err)
	}
}

func TestFinishAltUsesRecordedUser(t *testing.T) {
	db := openDB(t)
	ctx := context.Background()
	u, err := db.CreateUser(ctx)
	if err != nil {
		t.Fatal(err)
	}
	s := testServer(t, db)
	tok := &sso.CharacterToken{CharacterID: 7, CharacterName: "Alt", RefreshToken: "rt"}
	if err := s.finishAlt(ctx, &store.LoginState{UserID: u.ID}, tok); err != nil {
		t.Fatal(err)
	}
	owner, ok, err := db.OwnerOf(ctx, 7)
	if err != nil || !ok || owner != u.ID {
		t.Fatalf("owner %s ok %v err %v", owner, ok, err)
	}
}

func TestExchangeAuthCodeAgainstStore(t *testing.T) {
	const (
		verifier  = "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"
		challenge = "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM"
		redirect  = "http://localhost:1/cb"
	)
	db := openDB(t)
	ctx := context.Background()
	u, err := db.CreateUser(ctx)
	if err != nil {
		t.Fatal(err)
	}
	s := testServer(t, db)

	put := func(t *testing.T, code string, expires time.Time) {
		t.Helper()
		err := db.PutAuthCode(ctx, store.AuthCode{
			Code: code, UserID: u.ID, MCPClientID: "c",
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
					"grant_type":    {"authorization_code"},
					"code":          {"good"},
					"code_verifier": {verifier},
					"redirect_uri":  {redirect},
				}
			},
			status: 200,
			check: func(t *testing.T, rec *httptest.ResponseRecorder) {
				t.Helper()
				var payload map[string]any
				if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
					t.Fatal(err)
				}
				raw, _ := payload["access_token"].(string)
				info, err := s.verifyAccess(raw)
				if err != nil || info.UserID != u.ID {
					t.Fatalf("jwt %+v err %v", info, err)
				}
			},
		},
		{
			name: "bad verifier",
			setup: func(t *testing.T) url.Values {
				t.Helper()
				put(t, "bad-v", time.Now().Add(2*time.Minute))

				return url.Values{
					"grant_type":    {"authorization_code"},
					"code":          {"bad-v"},
					"code_verifier": {"nope"},
					"redirect_uri":  {redirect},
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
					"grant_type":    {"authorization_code"},
					"code":          {"bad-r"},
					"code_verifier": {verifier},
					"redirect_uri":  {"http://localhost:9/other"},
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
					"grant_type":    {"authorization_code"},
					"code":          {"old"},
					"code_verifier": {verifier},
					"redirect_uri":  {redirect},
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
					"grant_type":    {"authorization_code"},
					"code":          {"once"},
					"code_verifier": {verifier},
					"redirect_uri":  {redirect},
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
