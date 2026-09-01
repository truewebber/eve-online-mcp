-- +goose Up
-- Registration is anonymous, so oauth_clients is the table an untrusted
-- caller can grow. Soft-delete is not enough: the sweep hard-deletes
-- rows past the second 30-day window (DB.md Sweeps).

ALTER TABLE oauth_clients ADD COLUMN IF NOT EXISTS deleted_at TIMESTAMPTZ;
CREATE INDEX oauth_clients_deleted_at ON oauth_clients (deleted_at)
    WHERE deleted_at IS NOT NULL;
