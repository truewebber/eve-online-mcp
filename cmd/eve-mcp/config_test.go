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
		DatabaseURL:    testDatabase,
		HMACKey:        testHMACKeyHex,
	}
}

func TestCallbackURLFromPublicURL(t *testing.T) {
	t.Parallel()
	c := validConfig()
	c.PublicURL = "https://eve.example.com/"
	if err := c.validate(); err != nil {
		t.Fatal(err)
	}
	if c.CallbackURL != "https://eve.example.com/auth/callback" {
		t.Fatalf("callback %q", c.CallbackURL)
	}
}

func TestCallbackURLLoopback(t *testing.T) {
	t.Parallel()
	c := validConfig()
	c.Listen = "0.0.0.0:9000"
	if err := c.validate(); err != nil {
		t.Fatal(err)
	}
	if c.CallbackURL != "http://127.0.0.1:9000/auth/callback" {
		t.Fatalf("callback %q", c.CallbackURL)
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
