package main

import (
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
)

var (
	errListen   = errors.New("LISTEN must be host:port")
	errDatabase = errors.New("DATABASE_URL is required (Postgres DSN; run make postgres)")
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

	// Derived, not env.
	CallbackURL string
	UserAgent   string
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

func (c *config) validate() error {
	if c.DatabaseURL == "" {
		return errDatabase
	}
	_, port, err := net.SplitHostPort(c.Listen)
	if err != nil {
		return fmt.Errorf("%w: %w", errListen, err)
	}
	if _, _, err := net.SplitHostPort(c.InternalListen); err != nil {
		return fmt.Errorf("INTERNAL_LISTEN must be host:port: %w", err)
	}

	c.PublicURL = strings.TrimRight(c.PublicURL, "/")
	if c.PublicURL != "" {
		u, err := url.Parse(c.PublicURL)
		if err != nil {
			return fmt.Errorf("PUBLIC_URL: %w", err)
		}
		c.CallbackURL = u.JoinPath("auth", "callback").String()
	} else {
		c.CallbackURL = (&url.URL{
			Scheme: "http",
			Host:   net.JoinHostPort("127.0.0.1", port),
			Path:   "/auth/callback",
		}).String()
	}

	c.UserAgent = "github.com/truewebber/eve-online-mcp/" + version
	if c.Contact != "" {
		c.UserAgent += " " + c.Contact
	}

	return nil
}
