package cache

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

type CachedResponse struct {
	Body      any
	ETag      string
	ExpiresAt float64
	StoredAt  float64
	Pages     *int
}

func (c *CachedResponse) Fresh() bool {
	return time.Now().Unix() < int64(c.ExpiresAt)
}

func (c *CachedResponse) AgeSeconds() float64 {
	age := time.Now().Sub(time.Unix(int64(c.StoredAt), 0)).Seconds()
	if age < 0 {
		return 0
	}
	return age
}

type Store struct {
	db *sql.DB
	mu sync.Mutex
}

func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	if _, err := db.Exec(`PRAGMA journal_mode=WAL`); err != nil {
		db.Close()
		return nil, err
	}
	if _, err := db.Exec(`PRAGMA synchronous=NORMAL`); err != nil {
		db.Close()
		return nil, err
	}
	if _, err := db.Exec(`
CREATE TABLE IF NOT EXISTS http_cache (
    key        TEXT PRIMARY KEY,
    etag       TEXT,
    expires_at REAL NOT NULL,
    stored_at  REAL NOT NULL,
    pages      INTEGER,
    body       TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS http_cache_expires ON http_cache (expires_at);
CREATE TABLE IF NOT EXISTS names (
    id       INTEGER PRIMARY KEY,
    name     TEXT NOT NULL,
    category TEXT
);
CREATE TABLE IF NOT EXISTS blobs (
    key       TEXT PRIMARY KEY,
    stored_at REAL NOT NULL,
    value     TEXT NOT NULL
);`); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) GetHTTP(key string) (*CachedResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var etag sql.NullString
	var expires, stored float64
	var pages sql.NullInt64
	var body string
	err := s.db.QueryRow(
		`SELECT etag, expires_at, stored_at, pages, body FROM http_cache WHERE key = ?`, key,
	).Scan(&etag, &expires, &stored, &pages, &body)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var decoded any
	if err := json.Unmarshal([]byte(body), &decoded); err != nil {
		return nil, nil
	}
	c := &CachedResponse{Body: decoded, ExpiresAt: expires, StoredAt: stored}
	if etag.Valid {
		c.ETag = etag.String
	}
	if pages.Valid {
		p := int(pages.Int64)
		c.Pages = &p
	}
	return c, nil
}

func (s *Store) PutHTTP(key string, body any, etag string, expiresAt float64, pages *int) error {
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	var pagesVal any
	if pages != nil {
		pagesVal = *pages
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err = s.db.Exec(
		`INSERT INTO http_cache (key, etag, expires_at, stored_at, pages, body)
		 VALUES (?, ?, ?, ?, ?, ?)
		 ON CONFLICT(key) DO UPDATE SET etag=excluded.etag, expires_at=excluded.expires_at,
		 stored_at=excluded.stored_at, pages=excluded.pages, body=excluded.body`,
		key, nullIfEmpty(etag), expiresAt, float64(time.Now().Unix()), pagesVal, string(raw),
	)
	return err
}

func (s *Store) TouchHTTP(key string, expiresAt float64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(
		`UPDATE http_cache SET expires_at = ?, stored_at = ? WHERE key = ?`,
		expiresAt, float64(time.Now().Unix()), key,
	)
	return err
}

func (s *Store) PurgeExpired(olderThanDays float64) (int64, error) {
	cutoff := float64(time.Now().Unix()) - olderThanDays*86400
	s.mu.Lock()
	defer s.mu.Unlock()
	res, err := s.db.Exec(`DELETE FROM http_cache WHERE stored_at < ?`, cutoff)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func (s *Store) GetNames(ids []int) (map[int]NameRow, error) {
	out := map[int]NameRow{}
	if len(ids) == 0 {
		return out, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for start := 0; start < len(ids); start += 500 {
		end := start + 500
		if end > len(ids) {
			end = len(ids)
		}
		chunk := ids[start:end]
		query := `SELECT id, name, category FROM names WHERE id IN (` + placeholders(len(chunk)) + `)`
		args := make([]any, len(chunk))
		for i, id := range chunk {
			args[i] = id
		}
		rows, err := s.db.Query(query, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var id int
			var name string
			var cat sql.NullString
			if err := rows.Scan(&id, &name, &cat); err != nil {
				rows.Close()
				return nil, err
			}
			out[id] = NameRow{Name: name, Category: cat.String}
		}
		rows.Close()
	}
	return out, nil
}

type NameRow struct {
	Name     string
	Category string
}

func (s *Store) PutNames(entries [][3]any) error {
	if len(entries) == 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	stmt, err := tx.Prepare(
		`INSERT INTO names (id, name, category) VALUES (?, ?, ?)
		 ON CONFLICT(id) DO UPDATE SET name=excluded.name, category=excluded.category`,
	)
	if err != nil {
		tx.Rollback()
		return err
	}
	for _, e := range entries {
		if _, err := stmt.Exec(e[0], e[1], e[2]); err != nil {
			stmt.Close()
			tx.Rollback()
			return err
		}
	}
	stmt.Close()
	return tx.Commit()
}

func (s *Store) GetBlob(key string, maxAge *float64) (any, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var stored float64
	var value string
	err := s.db.QueryRow(`SELECT stored_at, value FROM blobs WHERE key = ?`, key).Scan(&stored, &value)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if maxAge != nil && float64(time.Now().Unix())-stored > *maxAge {
		return nil, nil
	}
	var decoded any
	if err := json.Unmarshal([]byte(value), &decoded); err != nil {
		return nil, nil
	}
	return decoded, nil
}

func (s *Store) PutBlob(key string, value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err = s.db.Exec(
		`INSERT INTO blobs (key, stored_at, value) VALUES (?, ?, ?)
		 ON CONFLICT(key) DO UPDATE SET stored_at=excluded.stored_at, value=excluded.value`,
		key, float64(time.Now().Unix()), string(raw),
	)
	return err
}

func placeholders(n int) string {
	if n <= 0 {
		return ""
	}
	b := make([]byte, 0, n*2)
	for i := 0; i < n; i++ {
		if i > 0 {
			b = append(b, ',')
		}
		b = append(b, '?')
	}
	return string(b)
}

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}
