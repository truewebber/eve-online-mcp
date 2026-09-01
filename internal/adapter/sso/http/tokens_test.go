package http

import (
	"testing"
	"time"

	"go.uber.org/mock/gomock"

	"github.com/truewebber/eve-online-mcp/internal/adapter/sso"
	"github.com/truewebber/eve-online-mcp/internal/mocks"
)

func TestAccessCacheHitSkipsNetwork(t *testing.T) {
	t.Parallel()
	c := New(sso.Options{}, nil, mocks.QuietLogger(gomock.NewController(t)))
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
	c := New(sso.Options{}, nil, mocks.QuietLogger(gomock.NewController(t)))
	if _, err := c.AccessToken(t.Context(), ""); err == nil {
		t.Fatal("want error")
	}
}

func TestRevokeEmptyIsNoop(t *testing.T) {
	t.Parallel()
	c := New(sso.Options{}, nil, mocks.QuietLogger(gomock.NewController(t)))
	c.Revoke(t.Context(), "")
}
