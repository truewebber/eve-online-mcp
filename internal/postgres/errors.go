package postgres

import "errors"

var (
	ErrEmptyDatabaseURL = errors.New("postgres: DATABASE_URL is empty")
	errLoggerRequired   = errors.New("postgres: logger is required")
)
