// Package user persists users under {data}/users/{id}/.
package user

import (
	"os"
	"path/filepath"
	"time"

	domuser "eve-mcp/internal/domain/user"

	"github.com/pelletier/go-toml/v2"
)

type fileUser struct {
	ID        string `toml:"id"`
	CreatedAt string `toml:"created_at"`
}

type Store struct {
	root string
}

func Open(root string) (*Store, error) {
	if err := os.MkdirAll(filepath.Join(root, "users"), 0o700); err != nil {
		return nil, err
	}
	return &Store{root: root}, nil
}

func (s *Store) Dir(id string) string {
	return filepath.Join(s.root, "users", id)
}

func (s *Store) Create() (*domuser.User, error) {
	u := &domuser.User{
		ID:        domuser.NewID(),
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
	}
	u.Dir = s.Dir(u.ID)
	if err := os.MkdirAll(u.Dir, 0o700); err != nil {
		return nil, err
	}
	raw, err := toml.Marshal(fileUser{ID: u.ID, CreatedAt: u.CreatedAt})
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(u.Dir, "user.toml"), raw, 0o600); err != nil {
		return nil, err
	}
	return u, nil
}

func (s *Store) Get(id string) (*domuser.User, error) {
	raw, err := os.ReadFile(filepath.Join(s.Dir(id), "user.toml"))
	if err != nil {
		return nil, err
	}
	var f fileUser
	if err := toml.Unmarshal(raw, &f); err != nil {
		return nil, err
	}
	return &domuser.User{ID: f.ID, CreatedAt: f.CreatedAt, Dir: s.Dir(id)}, nil
}

func (s *Store) List() ([]domuser.User, error) {
	entries, err := os.ReadDir(filepath.Join(s.root, "users"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []domuser.User
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		u, err := s.Get(e.Name())
		if err != nil {
			continue
		}
		out = append(out, *u)
	}
	return out, nil
}
