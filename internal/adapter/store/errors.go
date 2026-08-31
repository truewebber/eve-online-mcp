package store

import "errors"

var (
	ErrNotFound         = errors.New("store: not found")
	ErrEmptyDatabaseURL = errors.New("store: DATABASE_URL is empty")
)
