package sso

import (
	"context"
	"errors"
	"log"
	"maps"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/truewebber/eve-online-mcp/internal/adapter/store"
)

type accessMem struct {
	AccessToken     string
	AccessExpiresAt time.Time
}

// Refresh tokens live in Postgres when bound to a userID; access tokens
// stay in process memory (SPEC §3.4). An empty userID is the MCP login
// broker: memory only, until FinishEVE upserts into a user store.
type TokenStore struct {
	db     *store.Store
	userID string
	mu     sync.Mutex
	tokens map[int]*CharacterToken // broker / unbound
	access map[int]accessMem       // durable overlay
}

var ErrMissingCharacterID = errors.New("sso: missing character id")

func newTokenStore(db *store.Store, userID string) *TokenStore {
	return &TokenStore{
		db:     db,
		userID: userID,
		tokens: map[int]*CharacterToken{},
		access: map[int]accessMem{},
	}
}

func (s *TokenStore) Upsert(ctx context.Context, token *CharacterToken) error {
	if token == nil || token.CharacterID == 0 {
		return ErrMissingCharacterID
	}
	if !s.durable() {
		return s.upsertMemory(token)
	}
	row := store.CharacterRow{
		CharacterID:  int64(token.CharacterID),
		Name:         token.CharacterName,
		OwnerHash:    token.OwnerHash,
		RefreshToken: token.RefreshToken,
		Scopes:       token.Scopes,
	}
	if token.AddedAt != 0 {
		row.AddedAt = time.Unix(int64(token.AddedAt), 0).UTC()
	}
	err := s.db.UpsertCharacter(ctx, s.userID, row)
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

func (s *TokenStore) Remove(ctx context.Context, id int) bool {
	if !s.durable() {
		s.mu.Lock()
		defer s.mu.Unlock()
		if _, ok := s.tokens[id]; !ok {
			return false
		}
		delete(s.tokens, id)

		return true
	}
	row, err := s.db.GetCharacter(ctx, int64(id))
	if err != nil || row.UserID != s.userID {
		return false
	}
	if err := s.db.DeleteCharacter(ctx, int64(id)); err != nil {
		return false
	}
	s.mu.Lock()
	delete(s.access, id)
	s.mu.Unlock()

	return true
}

func (s *TokenStore) Get(ctx context.Context, id int) *CharacterToken {
	if !s.durable() {
		s.mu.Lock()
		defer s.mu.Unlock()

		return s.tokens[id]
	}
	row, err := s.db.GetCharacter(ctx, int64(id))
	if err != nil || row.UserID != s.userID {
		return nil
	}
	s.mu.Lock()
	acc := s.access[id]
	s.mu.Unlock()

	return tokenFromRow(row, acc)
}

func (s *TokenStore) All(ctx context.Context) []*CharacterToken {
	if !s.durable() {
		s.mu.Lock()
		defer s.mu.Unlock()
		out := make([]*CharacterToken, 0, len(s.tokens))
		for _, t := range s.tokens {
			out = append(out, t)
		}
		sortTokens(out)

		return out
	}
	rows, err := s.db.ListCharacters(ctx, s.userID)
	if err != nil {
		log.Printf("sso: list characters: %v", err)

		return nil
	}
	s.mu.Lock()
	access := make(map[int]accessMem, len(s.access))
	maps.Copy(access, s.access)
	s.mu.Unlock()
	out := make([]*CharacterToken, 0, len(rows))
	for i := range rows {
		out = append(out, tokenFromRow(&rows[i], access[int(rows[i].CharacterID)]))
	}
	sortTokens(out)

	return out
}

func (s *TokenStore) FindByName(ctx context.Context, name string) *CharacterToken {
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

func (s *TokenStore) durable() bool {
	return s != nil && s.db != nil && s.userID != ""
}

func (s *TokenStore) upsertMemory(token *CharacterToken) error {
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

func (s *TokenStore) setAccess(token *CharacterToken) {
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

func tokenFromRow(row *store.CharacterRow, acc accessMem) *CharacterToken {
	scopes := row.Scopes
	if scopes == nil {
		scopes = []string{}
	}

	return &CharacterToken{
		CharacterID:     int(row.CharacterID),
		CharacterName:   row.Name,
		RefreshToken:    row.RefreshToken,
		Scopes:          scopes,
		OwnerHash:       row.OwnerHash,
		AddedAt:         float64(row.AddedAt.Unix()),
		AccessToken:     acc.AccessToken,
		AccessExpiresAt: acc.AccessExpiresAt,
	}
}

func sortTokens(out []*CharacterToken) {
	sort.Slice(out, func(i, j int) bool {
		return strings.ToLower(out[i].CharacterName) < strings.ToLower(out[j].CharacterName)
	})
}
