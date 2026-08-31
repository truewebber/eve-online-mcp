package sso

import (
	"testing"

	"github.com/truewebber/eve-online-mcp/internal/logtest"
)

func TestMemoryTokenStore(t *testing.T) {
	t.Parallel()
	ts := newTokenStore(nil, "", logtest.Silent{})
	tok := &CharacterToken{CharacterID: 1, CharacterName: "A", RefreshToken: "rt"}
	err := ts.Upsert(t.Context(), tok)
	if err != nil {
		t.Fatal(err)
	}
	if ts.Get(t.Context(), 1) == nil || ts.Get(t.Context(), 1).RefreshToken != "rt" {
		t.Fatal("get")
	}
	if ts.FindByName(t.Context(), "a") == nil {
		t.Fatal("find")
	}
	if !ts.Remove(t.Context(), 1) || ts.Get(t.Context(), 1) != nil {
		t.Fatal("remove")
	}
}
