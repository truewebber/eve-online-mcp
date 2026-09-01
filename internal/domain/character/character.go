package character

import (
	"context"
	"errors"
	"time"
)

const PlayerCorpIDFloor = 98_000_000

var ErrNotFound = errors.New("character: not found")

type Character struct {
	ID        int64
	Name      string
	OwnerHash string
	CreatedAt time.Time
	DeletedAt *time.Time
}

func (c Character) Live() bool { return c.DeletedAt == nil }

//go:generate go tool go.uber.org/mock/mockgen -destination=../../mocks/character.go -package=mocks -mock_names=Repository=MockCharacterRepository github.com/truewebber/eve-online-mcp/internal/domain/character Repository
type Repository interface {
	Upsert(ctx context.Context, c Character) error
	Get(ctx context.Context, id int64) (*Character, error)
	Delete(ctx context.Context, id int64) error
}

type Token struct {
	CharacterID   int
	CharacterName string
	Scopes        []string
	OwnerHash     string
}

type Corporation struct {
	Token           *Token
	CorporationID   int
	CorporationName string
	Ticker          string
	Public          map[string]any
	Roles           map[string]struct{}
	RolesAtHQ       map[string]struct{}
	RolesAtBase     map[string]struct{}
	RolesAtOther    map[string]struct{}
}

func (c Corporation) CharacterID() int {
	if c.Token == nil {
		return 0
	}

	return c.Token.CharacterID
}

func (c Corporation) CharacterName() string {
	if c.Token == nil {
		return ""
	}

	return c.Token.CharacterName
}

func (c Corporation) IsNPC() bool { return c.CorporationID < PlayerCorpIDFloor }

func (c Corporation) HasRole(needed ...string) bool {
	if _, ok := c.Roles["Director"]; ok {
		return true
	}
	for _, role := range needed {
		if _, ok := c.Roles[role]; ok {
			return true
		}
	}

	return false
}
