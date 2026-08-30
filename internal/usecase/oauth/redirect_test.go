package oauth

import "testing"

func TestRedirectOK(t *testing.T) {
	ok := []string{
		"http://localhost:8787/callback",
		"http://127.0.0.1:3118/callback",
		"https://www.cursor.com/agents/mcp/oauth/callback",
		"https://claude.ai/api/mcp/auth_callback",
	}
	for _, u := range ok {
		if !redirectOK(u) {
			t.Errorf("want allow %s", u)
		}
	}
	bad := []string{
		"https://evil.example/callback",
		"http://example.com/callback",
		"javascript:alert(1)",
	}
	for _, u := range bad {
		if redirectOK(u) {
			t.Errorf("want deny %s", u)
		}
	}
}

func TestPKCE(t *testing.T) {
	verifier := "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"
	// RFC 7636 appendix B
	challenge := "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM"
	if !pkceOK(challenge, verifier) {
		t.Fatal("rfc example should verify")
	}
	if pkceOK(challenge, "nope") {
		t.Fatal("wrong verifier")
	}
}
