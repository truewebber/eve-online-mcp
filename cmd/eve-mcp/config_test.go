package main

import (
	"encoding/hex"
	"errors"
	"testing"
)

const (
	testHMACKeyHex = "00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff"
	testListen     = "127.0.0.1:8765"
	testInternal   = "127.0.0.1:8766"
	testDatabase   = "postgres://127.0.0.1:5432/eve_mcp"
)

func validConfig() config {
	return config{
		Listen:         testListen,
		InternalListen: testInternal,
		PublicURL:      "http://" + testListen,
		DatabaseURL:    testDatabase,
		HMACKey:        testHMACKeyHex,
	}
}

func ready(t *testing.T, c *config) {
	t.Helper()
	if err := c.validate(); err != nil {
		t.Fatal(err)
	}
	c.derive()
}

func TestCallbackURLFromPublicURL(t *testing.T) {
	t.Parallel()
	c := validConfig()
	c.PublicURL = "https://eve.example.com/"
	ready(t, &c)
	if c.CallbackURL != "https://eve.example.com/auth/callback" {
		t.Fatalf("callback %q", c.CallbackURL)
	}
}

func TestCallbackURLFromListenIsNotInvented(t *testing.T) {
	t.Parallel()
	c := validConfig()
	c.Listen = "127.0.0.1:9000"
	c.PublicURL = "http://127.0.0.1:9000"
	ready(t, &c)
	if c.CallbackURL != "http://127.0.0.1:9000/auth/callback" {
		t.Fatalf("callback %q", c.CallbackURL)
	}
}

func TestPublicURLRequired(t *testing.T) {
	t.Parallel()
	c := validConfig()
	c.PublicURL = ""
	if err := c.validate(); !errors.Is(err, errPublicURLRequired) {
		t.Fatalf("got %v", err)
	}
}

func TestListenRequired(t *testing.T) {
	t.Parallel()
	c := validConfig()
	c.Listen = ""
	if err := c.validate(); !errors.Is(err, errListenRequired) {
		t.Fatalf("got %v", err)
	}
}

func TestPublicURLHTTPOnPublicHost(t *testing.T) {
	t.Parallel()
	c := validConfig()
	c.PublicURL = "http://eve.example.com"
	if err := c.validate(); !errors.Is(err, errPublicURLScheme) {
		t.Fatalf("got %v", err)
	}
}

func TestPublicURLHTTPOnLoopback(t *testing.T) {
	t.Parallel()
	c := validConfig()
	c.PublicURL = "http://127.0.0.1:8765"
	ready(t, &c)
	if !c.TrustConnectingIP {
		t.Fatal("loopback bind should trust CF-Connecting-IP")
	}
}

func TestWildcardRedirectRefused(t *testing.T) {
	t.Parallel()
	c := validConfig()
	c.RedirectsRaw = "https://*.example.com/cb"
	if err := c.validate(); !errors.Is(err, errRedirectWildcard) {
		t.Fatalf("got %v", err)
	}
}

func TestFragmentRedirectRefused(t *testing.T) {
	t.Parallel()
	c := validConfig()
	c.RedirectsRaw = "https://app.example.com/cb#frag"
	if err := c.validate(); !errors.Is(err, errRedirectFragment) {
		t.Fatalf("got %v", err)
	}
}

func TestRelativeRedirectRefused(t *testing.T) {
	t.Parallel()
	c := validConfig()
	c.RedirectsRaw = "/callback"
	if err := c.validate(); !errors.Is(err, errRedirectAbsolute) {
		t.Fatalf("got %v", err)
	}
}

func TestExtraRedirectAccepted(t *testing.T) {
	t.Parallel()
	c := validConfig()
	c.RedirectsRaw = "https://app.example.com/oauth/callback"
	ready(t, &c)
	if len(c.ExtraRedirects) != 1 || c.ExtraRedirects[0] != "https://app.example.com/oauth/callback" {
		t.Fatalf("redirects %v", c.ExtraRedirects)
	}
}

func TestHMACKeyRequired(t *testing.T) {
	t.Parallel()
	c := validConfig()
	c.HMACKey = ""
	if err := c.validate(); !errors.Is(err, errHMACRequired) {
		t.Fatalf("got %v", err)
	}
}

func TestHMACKeyTooShort(t *testing.T) {
	t.Parallel()
	c := validConfig()
	c.HMACKey = hex.EncodeToString(make([]byte, 16))
	if err := c.validate(); !errors.Is(err, errHMACTooShort) {
		t.Fatalf("got %v", err)
	}
}

func TestHMACKeyNotHex(t *testing.T) {
	t.Parallel()
	c := validConfig()
	c.HMACKey = "not-hex"
	if err := c.validate(); !errors.Is(err, errHMACHex) {
		t.Fatalf("got %v", err)
	}
}

func TestNonLoopbackDoesNotTrustCF(t *testing.T) {
	t.Parallel()
	c := validConfig()
	c.Listen = "0.0.0.0:8765"
	c.PublicURL = "https://eve.example.com"
	ready(t, &c)
	if c.TrustConnectingIP {
		t.Fatal("wildcard bind must not trust CF-Connecting-IP")
	}
}
