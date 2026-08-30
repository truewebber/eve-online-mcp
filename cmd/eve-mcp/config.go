package main

import (
	"errors"
	"fmt"
	"net"
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

// config is the process config for cmd/eve-mcp only.
// The instance owns exactly one EVE application; users sign in via browser.
type config struct {
	// ClientID is the EVE application from developers.eveonline.com.
	ClientID     string `env:"CLIENT_ID,notEmpty"`
	ClientSecret string `env:"CLIENT_SECRET"`
	// Contact identifies the operator to CCP in the User-Agent.
	Contact string `env:"CONTACT"`

	Listen         string `env:"LISTEN"`
	InternalListen string `env:"INTERNAL_LISTEN"`
	PublicURL      string `env:"PUBLIC_URL"`
	DatabaseURL    string `env:"DATABASE_URL"`

	// Derived, not env.
	CallbackURL string
	UserAgent   string
}

// loadConfig reads ./.env if present (else the OS environment), then validates.
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
		return config{}, err
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
		return fmt.Errorf("%w: %v", errListen, err)
	}
	if _, _, err := net.SplitHostPort(c.InternalListen); err != nil {
		return fmt.Errorf("INTERNAL_LISTEN must be host:port: %v", err)
	}

	c.PublicURL = strings.TrimRight(c.PublicURL, "/")
	if c.PublicURL != "" {
		c.CallbackURL = c.PublicURL + "/auth/callback"
	} else {
		c.CallbackURL = fmt.Sprintf("http://127.0.0.1:%s/auth/callback", port)
	}

	c.UserAgent = "github.com/truewebber/eve-online-mcp/" + version
	if c.Contact != "" {
		c.UserAgent += " " + c.Contact
	}
	return nil
}
