package esi

import "testing"

func TestPathJoinsSegments(t *testing.T) {
	t.Parallel()
	got := Path("characters", ID(42), "mail", ID(9))
	if got.String() != "/characters/42/mail/9" {
		t.Fatalf("raw %q", got.String())
	}
	if got.Pattern() != "/characters/{id}/mail/{id}" {
		t.Fatalf("pattern %q", got.Pattern())
	}
}

func TestPathMarksNonNumericParam(t *testing.T) {
	t.Parallel()
	got := Path("killmails", ID(1), Param("2cc55ac01ceb43dd78fe73bec8c593073781360a"))
	if got.String() != "/killmails/1/2cc55ac01ceb43dd78fe73bec8c593073781360a" {
		t.Fatalf("raw %q", got.String())
	}
	if got.Pattern() != "/killmails/{id}/{id}" {
		t.Fatalf("pattern %q", got.Pattern())
	}
}

func TestPathStaticKeepsLiteral(t *testing.T) {
	t.Parallel()
	got := Path("/status")
	if got.String() != "/status" || got.Pattern() != "/status" {
		t.Fatalf("got %+v", got)
	}
}

func TestPathEscapesTraversal(t *testing.T) {
	t.Parallel()
	got := Path("characters", "../evil", "skills")
	if got.String() != "/characters/..%2Fevil/skills" {
		t.Fatalf("got %q", got.String())
	}
	if got.Pattern() != got.String() {
		t.Fatalf("literal pattern %q", got.Pattern())
	}
	got = Path("characters", "//evil.example", "skills")
	if got.String() != "/characters/%2F%2Fevil.example/skills" {
		t.Fatalf("got %q", got.String())
	}
	if got.Pattern() != got.String() {
		t.Fatalf("literal pattern %q", got.Pattern())
	}
}

func TestPathParamIsNeverALabel(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		value, raw string
	}{
		{"../evil", "/killmails/1/..%2Fevil"},
		{"//evil.example", "/killmails/1/%2F%2Fevil.example"},
	} {
		got := Path("killmails", ID(1), Param(c.value))
		if got.String() != c.raw {
			t.Fatalf("value %q raw %q", c.value, got.String())
		}
		if got.Pattern() != "/killmails/{id}/{id}" {
			t.Fatalf("value %q pattern %q", c.value, got.Pattern())
		}
	}
}
