package esi

import "testing"

func TestPathJoinsSegments(t *testing.T) {
	t.Parallel()
	got := Path("characters", ID(42), "mail", ID(9))
	if got != "/characters/42/mail/9" {
		t.Fatalf("got %q", got)
	}
}
