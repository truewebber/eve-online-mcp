package http

import (
	nhttp "net/http"
	"testing"
	"time"
)

func TestAccessCacheHitSkipsNetwork(t *testing.T) {
	t.Parallel()
	c := mustSSO(t, testSSOOptions(), &nhttp.Client{})
	c.access.put("rt", accessMem{
		AccessToken:     "at",
		AccessExpiresAt: time.Now().Add(time.Hour),
	})
	tok, err := c.AccessToken(t.Context(), "rt")
	if err != nil {
		t.Fatal(err)
	}
	if tok.AccessToken != "at" || tok.RefreshToken != "rt" {
		t.Fatalf("got %+v", tok)
	}
}

func TestAccessCacheMissEmptyRefresh(t *testing.T) {
	t.Parallel()
	c := mustSSO(t, testSSOOptions(), &nhttp.Client{})
	if _, err := c.AccessToken(t.Context(), ""); err == nil {
		t.Fatal("want error")
	}
}

func TestRevokeEmptyIsNoop(t *testing.T) {
	t.Parallel()
	c := mustSSO(t, testSSOOptions(), &nhttp.Client{})
	c.Revoke(t.Context(), "")
}
