package oauth

import "testing"

func TestHostAssemblesURLs(t *testing.T) {
	t.Parallel()
	h := Host{Listen: "127.0.0.1:8765", MCPPath: "/mcp"}
	if got := h.BaseURL(); got != "http://127.0.0.1:8765" {
		t.Fatalf("BaseURL %q", got)
	}
	if got := h.URL("oauth", "authorize"); got != "http://127.0.0.1:8765/oauth/authorize" {
		t.Fatalf("authorize %q", got)
	}
	s := &Server{pub: h}
	if got := s.ResourceURL(); got != "http://127.0.0.1:8765/mcp" {
		t.Fatalf("resource %q", got)
	}
	if got := s.MetadataURL(); got != "http://127.0.0.1:8765/.well-known/oauth-protected-resource" {
		t.Fatalf("metadata %q", got)
	}
	meta := s.AuthServerMeta()
	if meta.AuthorizationEndpoint != "http://127.0.0.1:8765/oauth/authorize" {
		t.Fatalf("meta authorize %q", meta.AuthorizationEndpoint)
	}
	if meta.TokenEndpoint != "http://127.0.0.1:8765/oauth/token" {
		t.Fatalf("meta token %q", meta.TokenEndpoint)
	}
	if meta.RegistrationEndpoint != "http://127.0.0.1:8765/oauth/register" {
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
