package character

const PlayerCorpIDFloor = 98_000_000

type NotFoundError struct{ Msg string }

func (e NotFoundError) Error() string { return e.Msg }

// Refresh material stays in the SSO adapter; this is identity and grants only.
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
