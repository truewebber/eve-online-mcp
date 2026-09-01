package oauth

import (
	"net/http"
	"testing"
)

func TestClientIPTrustsCFOnlyWhenAsked(t *testing.T) {
	t.Parallel()
	r := &http.Request{RemoteAddr: "127.0.0.1:54321", Header: http.Header{}}
	r.Header.Set(headerConnectingIP, "203.0.113.9")
	if got := ClientIP(r, true); got != "203.0.113.9" {
		t.Fatalf("trusted %q", got)
	}
	if got := ClientIP(r, false); got != "127.0.0.1" {
		t.Fatalf("socket %q", got)
	}
}

func TestClientIPFallsBackToSocket(t *testing.T) {
	t.Parallel()
	r := &http.Request{RemoteAddr: "198.51.100.4:9"}
	if got := ClientIP(r, true); got != "198.51.100.4" {
		t.Fatalf("got %q", got)
	}
}
