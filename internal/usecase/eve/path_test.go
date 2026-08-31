package eve

import "testing"

func TestESIPathJoinsSegments(t *testing.T) {
	t.Parallel()
	got := esiPath("characters", esiID(7), "mail", esiID(3))
	if got != "/characters/7/mail/3" {
		t.Fatalf("got %q", got)
	}
}

func TestZkillURL(t *testing.T) {
	t.Parallel()
	got := zkillURL(122648105)
	if got != "https://zkillboard.com/kill/122648105" {
		t.Fatalf("got %q", got)
	}
}
