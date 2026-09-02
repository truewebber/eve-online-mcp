package eve

import "testing"

func TestESIPathJoinsSegments(t *testing.T) {
	t.Parallel()
	got := esiPath("characters", esiID(7), "mail", esiID(3))
	if got.String() != "/characters/7/mail/3" {
		t.Fatalf("raw %q", got.String())
	}
	if got.Pattern() != "/characters/{id}/mail/{id}" {
		t.Fatalf("pattern %q", got.Pattern())
	}
}

func TestZkillURL(t *testing.T) {
	t.Parallel()
	got := zkillURL(122648105)
	if got != "https://zkillboard.com/kill/122648105" {
		t.Fatalf("got %q", got)
	}
}
