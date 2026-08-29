package tenant

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/pelletier/go-toml/v2"
)

type Tenant struct {
	ID              string `toml:"id" json:"id"`
	EveClientID     string `toml:"eve_client_id" json:"eve_client_id"`
	EveClientSecret string `toml:"eve_client_secret,omitempty" json:"-"`
	Contact         string `toml:"contact" json:"contact"`
	CreatedAt       string `toml:"created_at" json:"created_at"`
	Dir             string `toml:"-" json:"-"`
}

func IDFor(eveClientID string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(eveClientID)))
	return hex.EncodeToString(sum[:])[:16]
}

type Store struct {
	root string
	mu   sync.Mutex
}

func Open(root string) (*Store, error) {
	if err := os.MkdirAll(filepath.Join(root, "tenants"), 0o700); err != nil {
		return nil, err
	}
	return &Store{root: root}, nil
}

func (s *Store) Dir(id string) string {
	return filepath.Join(s.root, "tenants", id)
}

func (s *Store) Upsert(eveClientID, eveSecret, contact string) (*Tenant, error) {
	eveClientID = strings.TrimSpace(eveClientID)
	if eveClientID == "" {
		return nil, fmt.Errorf("eve client_id is required")
	}
	id := IDFor(eveClientID)
	dir := s.Dir(id)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	t := &Tenant{
		ID:              id,
		EveClientID:     eveClientID,
		EveClientSecret: strings.TrimSpace(eveSecret),
		Contact:         strings.TrimSpace(contact),
		CreatedAt:       time.Now().UTC().Format(time.RFC3339),
		Dir:             dir,
	}
	if existing, err := s.Get(id); err == nil && existing != nil {
		if t.EveClientSecret == "" {
			t.EveClientSecret = existing.EveClientSecret
		}
		if t.Contact == "" {
			t.Contact = existing.Contact
		}
		t.CreatedAt = existing.CreatedAt
	}
	raw, err := toml.Marshal(t)
	if err != nil {
		return nil, err
	}
	path := filepath.Join(dir, "tenant.toml")
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return nil, err
	}
	if err := os.Rename(tmp, path); err != nil {
		return nil, err
	}
	return t, nil
}

func (s *Store) Get(id string) (*Tenant, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	path := filepath.Join(s.Dir(id), "tenant.toml")
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var t Tenant
	if err := toml.Unmarshal(raw, &t); err != nil {
		return nil, err
	}
	t.Dir = s.Dir(id)
	return &t, nil
}

func (s *Store) List() ([]Tenant, error) {
	entries, err := os.ReadDir(filepath.Join(s.root, "tenants"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []Tenant
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		t, err := s.Get(e.Name())
		if err != nil {
			continue
		}
		out = append(out, *t)
	}
	return out, nil
}
