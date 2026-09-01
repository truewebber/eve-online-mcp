package main

import (
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"

	"github.com/caarlos0/env/v11"
	"github.com/joho/godotenv"
)

const (
	version           = "0.3.0"
	defaultCompatDate = "2026-08-18"
	dotEnvFile        = ".env"
	hmacMinBytes      = 32
)

var (
	errListen       = errors.New("LISTEN must be host:port")
	errDatabase     = errors.New("DATABASE_URL is required (Postgres DSN; run make postgres)")
	errHMACRequired = errors.New("HMAC_KEY is required (openssl rand -hex 32)")
	errHMACTooShort = errors.New("HMAC_KEY must decode to at least 32 bytes (openssl rand -hex 32)")
	errHMACHex      = errors.New("HMAC_KEY must be hex (openssl rand -hex 32)")
	errPublicURL    = errors.New("PUBLIC_URL must be an absolute URL")
)

// The instance owns exactly one EVE application; users sign in via browser.
type config struct {
	ClientID     string `env:"CLIENT_ID,notEmpty"`
	ClientSecret string `env:"CLIENT_SECRET"`
	Contact      string `env:"CONTACT"`

	Listen         string `env:"LISTEN"`
	InternalListen string `env:"INTERNAL_LISTEN"`
	PublicURL      string `env:"PUBLIC_URL"`
	DatabaseURL    string `env:"DATABASE_URL"`
	HMACKey        string `env:"HMAC_KEY"`

	// Derived, not env.
	CallbackURL string
	UserAgent   string
	hmacKey     []byte
}

func loadConfig() (config, error) {
	c := config{
		Listen:         "127.0.0.1:8765",
		InternalListen: "127.0.0.1:8766",
	}

	if _, err := os.Stat(dotEnvFile); err == nil {
		m, err := godotenv.Read(dotEnvFile)
		if err != nil {
			return config{}, fmt.Errorf("read %s: %w", dotEnvFile, err)
		}
		if err := env.ParseWithOptions(&c, env.Options{Environment: m}); err != nil {
			return config{}, fmt.Errorf("parse %s: %w", dotEnvFile, err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return config{}, fmt.Errorf("stat %s: %w", dotEnvFile, err)
	} else if err := env.Parse(&c); err != nil {
		return config{}, fmt.Errorf("parse env: %w", err)
	}

	if err := c.validate(); err != nil {
		return config{}, err
	}

	return c, nil
}

func decodeHMACKey(raw string) ([]byte, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, errHMACRequired
	}
	key, err := hex.DecodeString(raw)
	if err != nil {
		return nil, errHMACHex
	}
	if len(key) < hmacMinBytes {
		return nil, errHMACTooShort
	}

	return key, nil
}

func (c *config) validate() error {
	if c.DatabaseURL == "" {
		return errDatabase
	}
	key, err := decodeHMACKey(c.HMACKey)
	if err != nil {
		return err
	}
	c.hmacKey = key
	_, port, err := net.SplitHostPort(c.Listen)
	if err != nil {
		return fmt.Errorf("%w: %w", errListen, err)
	}
	if _, _, err := net.SplitHostPort(c.InternalListen); err != nil {
		return fmt.Errorf("INTERNAL_LISTEN must be host:port: %w", err)
	}

	var public url.URL
	if c.PublicURL != "" {
		u, err := url.Parse(c.PublicURL)
		if err != nil {
			return fmt.Errorf("PUBLIC_URL: %w", err)
		}
		if u.Host == "" || u.Scheme == "" {
			return errPublicURL
		}
		public = *u
		c.PublicURL = strings.TrimRight(c.PublicURL, "/")
	} else {
		public = url.URL{Scheme: "http", Host: net.JoinHostPort("127.0.0.1", port)}
	}
	c.CallbackURL = public.JoinPath("auth", "callback").String()

	c.UserAgent = "github.com/truewebber/eve-online-mcp/" + version
	if c.Contact != "" {
		c.UserAgent += " " + c.Contact
	}

	return nil
}
