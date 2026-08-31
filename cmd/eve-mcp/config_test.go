package main

import "testing"

func TestCallbackURLFromPublicURL(t *testing.T) {
	t.Parallel()
	c := config{
		Listen:         "127.0.0.1:8765",
		InternalListen: "127.0.0.1:8766",
		DatabaseURL:    "postgres://127.0.0.1:5432/eve_mcp",
		PublicURL:      "https://eve.example.com/",
	}
	if err := c.validate(); err != nil {
		t.Fatal(err)
	}
	if c.CallbackURL != "https://eve.example.com/auth/callback" {
		t.Fatalf("callback %q", c.CallbackURL)
	}
}

func TestCallbackURLLoopback(t *testing.T) {
	t.Parallel()
	c := config{
		Listen:         "0.0.0.0:9000",
		InternalListen: "127.0.0.1:8766",
		DatabaseURL:    "postgres://127.0.0.1:5432/eve_mcp",
	}
	if err := c.validate(); err != nil {
		t.Fatal(err)
	}
	if c.CallbackURL != "http://127.0.0.1:9000/auth/callback" {
		t.Fatalf("callback %q", c.CallbackURL)
	}
}
