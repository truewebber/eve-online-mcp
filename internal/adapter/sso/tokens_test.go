package sso

import "testing"

func TestMemoryTokenStore(t *testing.T) {
	t.Parallel()
	ts := newTokenStore(nil, "")
	tok := &CharacterToken{CharacterID: 1, CharacterName: "A", RefreshToken: "rt"}
	err := ts.Upsert(tok)
	if err != nil {
		t.Fatal(err)
	}
	if ts.Get(1) == nil || ts.Get(1).RefreshToken != "rt" {
		t.Fatal("get")
	}
	if ts.FindByName("a") == nil {
		t.Fatal("find")
	}
	if !ts.Remove(1) || ts.Get(1) != nil {
		t.Fatal("remove")
	}
}
