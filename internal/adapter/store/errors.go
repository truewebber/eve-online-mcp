package store

import "errors"

var (
	ErrNotFound         = errors.New("store: not found")
	ErrOwned            = errors.New("store: character belongs to another user")
	ErrEmptyDatabaseURL = errors.New("store: DATABASE_URL is empty")
)
