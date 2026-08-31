package http

import (
	"testing"

	"go.uber.org/mock/gomock"

	"github.com/truewebber/eve-online-mcp/internal/adapter/sso"
	"github.com/truewebber/eve-online-mcp/internal/mocks"
)

func TestMemoryTokenStore(t *testing.T) {
	t.Parallel()
	c := New(sso.Options{}, nil, mocks.QuietLogger(gomock.NewController(t)))
	tok := &sso.CharacterToken{CharacterID: 1, CharacterName: "A", RefreshToken: "rt"}
	err := c.Upsert(t.Context(), tok)
	if err != nil {
		t.Fatal(err)
	}
	if c.Get(t.Context(), 1) == nil || c.Get(t.Context(), 1).RefreshToken != "rt" {
		t.Fatal("get")
	}
	if c.FindByName(t.Context(), "a") == nil {
		t.Fatal("find")
	}
	if !c.Remove(t.Context(), 1) || c.Get(t.Context(), 1) != nil {
		t.Fatal("remove")
	}
}
