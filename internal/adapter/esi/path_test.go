package esi

import "testing"

func TestPathJoinsSegments(t *testing.T) {
	t.Parallel()
	got := Path("characters", ID(42), "mail", ID(9))
	if got != "/characters/42/mail/9" {
		t.Fatalf("got %q", got)
	}
}

func TestPathEscapesTraversal(t *testing.T) {
	t.Parallel()
	got := Path("characters", "../evil", "skills")
	if got != "/characters/..%2Fevil/skills" {
		t.Fatalf("got %q", got)
	}
	got = Path("characters", "//evil.example", "skills")
	if got != "/characters/%2F%2Fevil.example/skills" {
		t.Fatalf("got %q", got)
	}
}
