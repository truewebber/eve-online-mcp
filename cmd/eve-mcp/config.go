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
	schemeHTTPS       = "https"
)

var (
	errListen            = errors.New("LISTEN_HOST_PORT must be host:port")
	errListenRequired    = errors.New("LISTEN_HOST_PORT is required")
	errInternalListen    = errors.New("INTERNAL_LISTEN_HOST_PORT must be host:port")
	errInternalRequired  = errors.New("INTERNAL_LISTEN_HOST_PORT is required")
	errDatabase          = errors.New("DATABASE_URL is required (Postgres DSN; run make postgres)")
	errHMACRequired      = errors.New("HMAC_KEY is required (openssl rand -hex 32)")
	errHMACTooShort      = errors.New("HMAC_KEY must decode to at least 32 bytes (openssl rand -hex 32)")
	errHMACHex           = errors.New("HMAC_KEY must be hex (openssl rand -hex 32)")
	errPublicURL         = errors.New("PUBLIC_URL must be an absolute URL")
	errPublicURLRequired = errors.New("PUBLIC_URL is required")
	errPublicURLScheme   = errors.New("PUBLIC_URL must be https unless the host is loopback")
	errRedirectAbsolute  = errors.New("EXTRA_REDIRECT_URIS entries must be absolute URLs")
	errRedirectWildcard  = errors.New("EXTRA_REDIRECT_URIS entries must not contain a wildcard")
	errRedirectFragment  = errors.New("EXTRA_REDIRECT_URIS entries must not contain a fragment")
)

// The instance owns exactly one EVE application; users sign in via browser.
type config struct {
	ClientID     string `env:"CLIENT_ID,notEmpty"`
	ClientSecret string `env:"CLIENT_SECRET"`
	Contact      string `env:"CONTACT"`

	Listen         string `env:"LISTEN_HOST_PORT"`
	InternalListen string `env:"INTERNAL_LISTEN_HOST_PORT"`
	PublicURL      string `env:"PUBLIC_URL"`
	RedirectsRaw   string `env:"EXTRA_REDIRECT_URIS"`
	DatabaseURL    string `env:"DATABASE_URL"`
	HMACKey        string `env:"HMAC_KEY"`

	// Derived, not env.
	CallbackURL       string
	UserAgent         string
	ExtraRedirects    []string
	TrustConnectingIP bool
	hmacKey           []byte
}

func loadConfig() (config, error) {
	c, err := readEnv()
	if err != nil {
		return config{}, err
	}
	if err := c.validate(); err != nil {
		return config{}, err
	}
	c.derive()

	return c, nil
}

func readEnv() (config, error) {
	c := config{}
	_, err := os.Stat(dotEnvFile)
	if err == nil {
		m, err := godotenv.Read(dotEnvFile)
		if err != nil {
			return config{}, fmt.Errorf("read %s: %w", dotEnvFile, err)
		}
		if err := env.ParseWithOptions(&c, env.Options{Environment: m}); err != nil {
			return config{}, fmt.Errorf("parse %s: %w", dotEnvFile, err)
		}

		return c, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return config{}, fmt.Errorf("stat %s: %w", dotEnvFile, err)
	}
	if err := env.Parse(&c); err != nil {
		return config{}, fmt.Errorf("parse env: %w", err)
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
	if err := c.checkDatabase(); err != nil {
		return err
	}
	if err := c.checkHMAC(); err != nil {
		return err
	}
	if err := c.checkListeners(); err != nil {
		return err
	}
	if err := c.checkPublicURL(); err != nil {
		return err
	}

	return c.checkRedirects()
}

func (c *config) checkDatabase() error {
	if c.DatabaseURL == "" {
		return errDatabase
	}

	return nil
}

func (c *config) checkHMAC() error {
	key, err := decodeHMACKey(c.HMACKey)
	if err != nil {
		return err
	}
	c.hmacKey = key

	return nil
}

func (c *config) checkListeners() error {
	if c.Listen == "" {
		return errListenRequired
	}
	if _, _, err := net.SplitHostPort(c.Listen); err != nil {
		return fmt.Errorf("%w: %w", errListen, err)
	}
	if c.InternalListen == "" {
		return errInternalRequired
	}
	if _, _, err := net.SplitHostPort(c.InternalListen); err != nil {
		return fmt.Errorf("%w: %w", errInternalListen, err)
	}

	return nil
}

func (c *config) checkPublicURL() error {
	if c.PublicURL == "" {
		return errPublicURLRequired
	}
	u, err := url.Parse(c.PublicURL)
	if err != nil {
		return fmt.Errorf("PUBLIC_URL: %w", err)
	}
	if u.Host == "" || u.Scheme == "" || !u.IsAbs() {
		return errPublicURL
	}
	if u.Scheme != schemeHTTPS && !loopbackHost(u.Hostname()) {
		return errPublicURLScheme
	}
	c.PublicURL = strings.TrimRight(c.PublicURL, "/")

	return nil
}

func (c *config) checkRedirects() error {
	c.ExtraRedirects = nil
	for raw := range strings.SplitSeq(c.RedirectsRaw, ",") {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		if err := extraRedirectOK(raw); err != nil {
			return err
		}
		c.ExtraRedirects = append(c.ExtraRedirects, raw)
	}

	return nil
}

func extraRedirectOK(raw string) error {
	if strings.Contains(raw, "*") {
		return errRedirectWildcard
	}
	u, err := url.Parse(raw)
	if err != nil || !u.IsAbs() || u.Host == "" {
		return errRedirectAbsolute
	}
	if u.Fragment != "" {
		return errRedirectFragment
	}

	return nil
}

func (c *config) derive() {
	c.TrustConnectingIP = loopbackBind(c.Listen)
	c.CallbackURL = callbackURL(c.PublicURL)
	c.UserAgent = "github.com/truewebber/eve-online-mcp/" + version
	if c.Contact != "" {
		c.UserAgent += " " + c.Contact
	}
}

func callbackURL(publicURL string) string {
	u, err := url.Parse(publicURL)
	if err != nil || u.Host == "" {
		return ""
	}

	return u.JoinPath("auth", "callback").String()
}

func loopbackBind(hostPort string) bool {
	host, _, err := net.SplitHostPort(hostPort)
	if err != nil {
		return false
	}

	return loopbackHost(host)
}

func loopbackHost(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)

	return ip != nil && ip.IsLoopback()
}
