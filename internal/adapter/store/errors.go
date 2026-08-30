package store

import "errors"

var (
	ErrNotFound = errors.New("store: not found")
	ErrOwned    = errors.New("store: character belongs to another user")
)
