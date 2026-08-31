package sso

import (
	"context"
	"errors"
	"time"

	"github.com/truewebber/eve-online-mcp/internal/domain/character"
)

var ErrMissingCharacterID = errors.New("sso: missing character id")

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

// Persist State + Verifier in login_states so any replica can finish the handshake.
type PreparedLogin struct {
	URL      string
	State    string
	Verifier string
	Scopes   []string
}

type Client interface {
	PrepareLogin(scopes []string) (*PreparedLogin, error)
	ExchangeCode(ctx context.Context, code, verifier string) (*CharacterToken, error)
	AccessToken(ctx context.Context, characterID int) (*CharacterToken, error)
	Revoke(ctx context.Context, characterID int)
	Upsert(ctx context.Context, token *CharacterToken) error
	Remove(ctx context.Context, id int) bool
	Get(ctx context.Context, id int) *CharacterToken
	All(ctx context.Context) []*CharacterToken
	FindByName(ctx context.Context, name string) *CharacterToken
	ForUser(userID string, chars character.Repository) Client
}
