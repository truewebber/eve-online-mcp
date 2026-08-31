package http

import (
	"context"
	"maps"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/truewebber/gopkg/log"

	"github.com/truewebber/eve-online-mcp/internal/adapter/sso"
	"github.com/truewebber/eve-online-mcp/internal/domain/character"
)

type accessMem struct {
	AccessToken     string
	AccessExpiresAt time.Time
}

// Refresh tokens live on the character row when bound to a userID; access
// tokens stay in process memory (SPEC §3.4). An empty userID is the MCP
// login broker: memory only, until FinishEVE upserts into a user store.
type tokenStore struct {
	chars  character.Repository
	userID string
	mu     sync.Mutex
	logger log.Logger
	tokens map[int]*sso.CharacterToken
	access map[int]accessMem
}

func newTokenStore(chars character.Repository, userID string, logger log.Logger) *tokenStore {
	return &tokenStore{
		chars:  chars,
		userID: userID,
		logger: logger,
		tokens: map[int]*sso.CharacterToken{},
		access: map[int]accessMem{},
	}
}

func (s *tokenStore) Upsert(ctx context.Context, token *sso.CharacterToken) error {
	if token == nil || token.CharacterID == 0 {
		return sso.ErrMissingCharacterID
	}
	if !s.durable() {
		return s.upsertMemory(token)
	}
	row := character.Character{
		ID:           int64(token.CharacterID),
		UserID:       s.userID,
		Name:         token.CharacterName,
		OwnerHash:    token.OwnerHash,
		RefreshToken: token.RefreshToken,
		Scopes:       token.Scopes,
	}
	if token.AddedAt != 0 {
		row.CreatedAt = time.Unix(int64(token.AddedAt), 0).UTC()
	}
	err := s.chars.Upsert(ctx, row)
	if err != nil {
		return wrap("Upsert", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if token.AccessToken != "" {
		s.access[token.CharacterID] = accessMem{
			AccessToken:     token.AccessToken,
			AccessExpiresAt: token.AccessExpiresAt,
		}
	}

	return nil
}

func (s *tokenStore) Remove(ctx context.Context, id int) bool {
	if !s.durable() {
		s.mu.Lock()
		defer s.mu.Unlock()
		if _, ok := s.tokens[id]; !ok {
			return false
		}
		delete(s.tokens, id)

		return true
	}
	row, err := s.chars.Get(ctx, int64(id))
	if err != nil || row.UserID != s.userID {
		return false
	}
	if err := s.chars.Delete(ctx, int64(id)); err != nil {
		return false
	}
	s.mu.Lock()
	delete(s.access, id)
	s.mu.Unlock()

	return true
}

func (s *tokenStore) Get(ctx context.Context, id int) *sso.CharacterToken {
	if !s.durable() {
		s.mu.Lock()
		defer s.mu.Unlock()

		return s.tokens[id]
	}
	row, err := s.chars.Get(ctx, int64(id))
	if err != nil || row.UserID != s.userID {
		return nil
	}
	s.mu.Lock()
	acc := s.access[id]
	s.mu.Unlock()

	return tokenFromCharacter(row, acc)
}

func (s *tokenStore) All(ctx context.Context) []*sso.CharacterToken {
	if !s.durable() {
		s.mu.Lock()
		defer s.mu.Unlock()
		out := make([]*sso.CharacterToken, 0, len(s.tokens))
		for _, t := range s.tokens {
			out = append(out, t)
		}
		sortTokens(out)

		return out
	}
	rows, err := s.chars.ListByUser(ctx, s.userID)
	if err != nil {
		s.logger.Error("sso: list characters", "err", err)

		return nil
	}
	s.mu.Lock()
	access := make(map[int]accessMem, len(s.access))
	maps.Copy(access, s.access)
	s.mu.Unlock()
	out := make([]*sso.CharacterToken, 0, len(rows))
	for i := range rows {
		out = append(out, tokenFromCharacter(&rows[i], access[int(rows[i].ID)]))
	}
	sortTokens(out)

	return out
}

func (s *tokenStore) FindByName(ctx context.Context, name string) *sso.CharacterToken {
	lowered := strings.ToLower(strings.TrimSpace(name))
	tokens := s.All(ctx)
	for _, t := range tokens {
		if strings.ToLower(t.CharacterName) == lowered {
			return t
		}
	}
	for _, t := range tokens {
		if lowered != "" && strings.Contains(strings.ToLower(t.CharacterName), lowered) {
			return t
		}
	}

	return nil
}

func (s *tokenStore) durable() bool {
	return s != nil && s.chars != nil && s.userID != ""
}

func (s *tokenStore) upsertMemory(token *sso.CharacterToken) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if existing := s.tokens[token.CharacterID]; existing != nil {
		if token.AccessToken == "" {
			token.AccessToken = existing.AccessToken
			token.AccessExpiresAt = existing.AccessExpiresAt
		}
		if token.AddedAt == 0 {
			token.AddedAt = existing.AddedAt
		}
	}
	if token.AddedAt == 0 {
		token.AddedAt = float64(time.Now().Unix())
	}
	s.tokens[token.CharacterID] = token

	return nil
}

func (s *tokenStore) setAccess(token *sso.CharacterToken) {
	if token == nil || token.AccessToken == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.durable() {
		s.access[token.CharacterID] = accessMem{
			AccessToken:     token.AccessToken,
			AccessExpiresAt: token.AccessExpiresAt,
		}

		return
	}
	s.tokens[token.CharacterID] = token
}

func tokenFromCharacter(row *character.Character, acc accessMem) *sso.CharacterToken {
	scopes := row.Scopes
	if scopes == nil {
		scopes = []string{}
	}

	return &sso.CharacterToken{
		CharacterID:     int(row.ID),
		CharacterName:   row.Name,
		RefreshToken:    row.RefreshToken,
		Scopes:          scopes,
		OwnerHash:       row.OwnerHash,
		AddedAt:         float64(row.CreatedAt.Unix()),
		AccessToken:     acc.AccessToken,
		AccessExpiresAt: acc.AccessExpiresAt,
	}
}

func sortTokens(out []*sso.CharacterToken) {
	sort.Slice(out, func(i, j int) bool {
		return strings.ToLower(out[i].CharacterName) < strings.ToLower(out[j].CharacterName)
	})
}
