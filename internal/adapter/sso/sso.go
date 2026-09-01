package sso

import (
	"context"
	"errors"
	"time"
)

var (
	ErrMissingCharacterID = errors.New("sso: missing character id")
	ErrInvalidGrant       = errors.New("sso: invalid grant")
)

const TokenAudience = "EVE Online"

type Options struct {
	ClientID     string
	ClientSecret string
	CallbackURL  string
	UserAgent    string
	Scopes       []string
}

type Error struct{ Msg string }

func (e Error) Error() string { return e.Msg }

func Err(msg string) error { return Error{Msg: msg} }

type CharacterToken struct {
	CharacterID     int      `json:"character_id"`
	CharacterName   string   `json:"character_name"`
	RefreshToken    string   `json:"refresh_token"`
	Scopes          []string `json:"scopes"`
	OwnerHash       string   `json:"owner_hash"`
	AddedAt         float64  `json:"added_at"`
	AccessToken     string   `json:"-"`
	AccessExpiresAt time.Time
}

type PreparedLogin struct {
	URL      string
	State    string
	Verifier string
	Scopes   []string
}

//go:generate go tool go.uber.org/mock/mockgen -destination=../../mocks/sso.go -package=mocks -mock_names=Client=MockSSOClient github.com/truewebber/eve-online-mcp/internal/adapter/sso Client
type Client interface {
	PrepareLogin(scopes []string) (*PreparedLogin, error)
	ExchangeCode(ctx context.Context, code, verifier string) (*CharacterToken, error)
	AccessToken(ctx context.Context, refreshToken string) (*CharacterToken, error)
	Revoke(ctx context.Context, refreshToken string)
}
