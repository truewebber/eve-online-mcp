package write

import (
	"context"
	"sync"
	"time"
)

type memPersist struct {
	mu      sync.Mutex
	confirm map[string]Confirm
	mail    []mailRow
}

type mailRow struct {
	userID string
	at     time.Time
}

func newMemPersist() *memPersist {
	return &memPersist{confirm: map[string]Confirm{}}
}

func (m *memPersist) PutConfirm(_ context.Context, c Confirm) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.confirm[c.Token] = c
	return nil
}

func (m *memPersist) GetConfirm(_ context.Context, token string) (*Confirm, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.confirm[token]
	if !ok {
		return nil, false, nil
	}
	cp := c
	return &cp, true, nil
}

func (m *memPersist) DeleteConfirm(_ context.Context, token string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.confirm, token)
	return nil
}

func (m *memPersist) CountConfirm(_ context.Context, userID string) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, c := range m.confirm {
		if c.UserID == userID {
			n++
		}
	}
	return n, nil
}

func (m *memPersist) CountMailSince(_ context.Context, userID string, since time.Time) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, row := range m.mail {
		if row.userID == userID && !row.at.Before(since) {
			n++
		}
	}
	return n, nil
}

func (m *memPersist) InsertMail(_ context.Context, userID string, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.mail = append(m.mail, mailRow{userID: userID, at: at})
	return nil
}
