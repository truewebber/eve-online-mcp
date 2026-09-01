package oauth

import "testing"

func TestHostAssemblesURLs(t *testing.T) {
	t.Parallel()
	h := Host{Listen: testListen, MCPPath: "/mcp"}
	if got := h.BaseURL(); got != "http://"+testListen {
		t.Fatalf("BaseURL %q", got)
	}
	if got := h.URL("oauth", "authorize"); got != "http://"+testListen+"/oauth/authorize" {
		t.Fatalf("authorize %q", got)
	}
	s := &Server{pub: h}
	if got := s.ResourceURL(); got != "http://"+testListen+"/mcp" {
		t.Fatalf("resource %q", got)
	}
	if got := s.MetadataURL(); got != "http://"+testListen+"/.well-known/oauth-protected-resource" {
		t.Fatalf("metadata %q", got)
	}
	meta := s.AuthServerMeta()
	if meta.AuthorizationEndpoint != "http://"+testListen+"/oauth/authorize" {
		t.Fatalf("meta authorize %q", meta.AuthorizationEndpoint)
	}
	if meta.TokenEndpoint != "http://"+testListen+"/oauth/token" {
		t.Fatalf("meta token %q", meta.TokenEndpoint)
	}
	if meta.RegistrationEndpoint != "http://"+testListen+"/oauth/register" {
		t.Fatalf("meta register %q", meta.RegistrationEndpoint)
	}
}

func TestHostAssemblesPublicURL(t *testing.T) {
	t.Parallel()
	h := Host{PublicURL: "https://eve.example.com/", MCPPath: "/mcp"}
	if got := h.BaseURL(); got != "https://eve.example.com" {
		t.Fatalf("BaseURL %q", got)
	}
	if got := h.URL("oauth", "token"); got != "https://eve.example.com/oauth/token" {
		t.Fatalf("token %q", got)
	}
}

func TestHostRewritesWildcardListen(t *testing.T) {
	t.Parallel()
	h := Host{Listen: "0.0.0.0:9000"}
	if got := h.BaseURL(); got != "http://127.0.0.1:9000" {
		t.Fatalf("BaseURL %q", got)
	}
}
