package store

import (
	"encoding/json"
	"time"
)

const (
	LoginStateTTL   = 15 * time.Minute
	ConfirmTokenTTL = 300 * time.Second
	SecretBytes     = 32
)

type LoginKind string

const (
	LoginMCP LoginKind = "mcp"
	LoginAlt LoginKind = "alt"
)

type CharacterRow struct {
	CharacterID  int64
	UserID       string
	Name         string
	OwnerHash    string
	RefreshToken string
	Scopes       []string
	AddedAt      time.Time
}

type Client struct {
	ID           string
	RedirectURIs []string
	CreatedAt    time.Time
}

type LoginState struct {
	State         string
	PKCEVerifier  string
	Scopes        []string
	Kind          LoginKind
	UserID        string // empty when kind=mcp and no user yet
	MCPClientID   string
	RedirectURI   string
	MCPState      string
	CodeChallenge string
	CreatedAt     time.Time
}

type AuthCode struct {
	Code          string
	UserID        string
	MCPClientID   string
	RedirectURI   string
	CodeChallenge string
	ExpiresAt     time.Time
}

type ConfirmToken struct {
	Token      string
	UserID     string
	Tool       string
	ArgsDigest string
	CreatedAt  time.Time
}

type CachedResponse struct {
	Body      json.RawMessage
	ETag      string
	ExpiresAt time.Time
	StoredAt  time.Time
	Pages     *int
}

func (c *CachedResponse) Fresh() bool {
	return time.Now().Before(c.ExpiresAt)
}

func (c *CachedResponse) AgeSeconds() float64 {
	age := time.Since(c.StoredAt).Seconds()
	if age < 0 {
		return 0
	}

	return age
}

func (c *CachedResponse) ExpiresUnix() float64 {
	return float64(c.ExpiresAt.Unix())
}

func (c *CachedResponse) Data() any {
	if c == nil || len(c.Body) == 0 {
		return nil
	}
	var v any
	err := json.Unmarshal(c.Body, &v)
	if err != nil {
		return nil
	}

	return v
}

type NameRow struct {
	ID       int64
	Name     string
	Category string
}
