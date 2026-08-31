-- +goose Up
-- Creates the current schema outright. A database from the users era is
-- dropped by hand, not transformed (DB.md). IF NOT EXISTS lets a volume
-- that the old boot-time migrator already created take a goose version
-- without a rebuild.

CREATE TABLE IF NOT EXISTS users (
    id         TEXT PRIMARY KEY,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS characters (
    character_id  BIGINT PRIMARY KEY,
    user_id       TEXT NOT NULL REFERENCES users (id),
    name          TEXT NOT NULL,
    owner_hash    TEXT NOT NULL DEFAULT '',
    refresh_token TEXT NOT NULL,
    scopes        TEXT[] NOT NULL DEFAULT '{}',
    added_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS characters_user_id ON characters (user_id);

CREATE TABLE IF NOT EXISTS oauth_clients (
    client_id     TEXT PRIMARY KEY,
    redirect_uris TEXT[] NOT NULL DEFAULT '{}',
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS login_states (
    state          TEXT PRIMARY KEY,
    pkce_verifier  TEXT NOT NULL,
    scopes         TEXT[] NOT NULL DEFAULT '{}',
    kind           TEXT NOT NULL CHECK (kind IN ('mcp', 'alt')),
    user_id        TEXT REFERENCES users (id),
    mcp_client_id  TEXT NOT NULL DEFAULT '',
    redirect_uri   TEXT NOT NULL DEFAULT '',
    mcp_state      TEXT NOT NULL DEFAULT '',
    code_challenge TEXT NOT NULL DEFAULT '',
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS auth_codes (
    code           TEXT PRIMARY KEY,
    user_id        TEXT NOT NULL REFERENCES users (id),
    mcp_client_id  TEXT NOT NULL,
    redirect_uri   TEXT NOT NULL,
    code_challenge TEXT NOT NULL,
    expires_at     TIMESTAMPTZ NOT NULL
);

CREATE TABLE IF NOT EXISTS confirm_tokens (
    token       TEXT PRIMARY KEY,
    user_id     TEXT NOT NULL,
    tool        TEXT NOT NULL,
    args_digest TEXT NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS mail_log (
    user_id TEXT NOT NULL,
    sent_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX IF NOT EXISTS mail_log_user_sent ON mail_log (user_id, sent_at);

CREATE TABLE IF NOT EXISTS http_cache (
    key        TEXT PRIMARY KEY,
    etag       TEXT NOT NULL DEFAULT '',
    expires_at TIMESTAMPTZ NOT NULL,
    stored_at  TIMESTAMPTZ NOT NULL,
    pages      INTEGER,
    body       JSONB NOT NULL
);
CREATE INDEX IF NOT EXISTS http_cache_expires ON http_cache (expires_at);

CREATE TABLE IF NOT EXISTS names (
    id       BIGINT PRIMARY KEY,
    name     TEXT NOT NULL,
    category TEXT NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS blobs (
    key       TEXT PRIMARY KEY,
    stored_at TIMESTAMPTZ NOT NULL,
    value     JSONB NOT NULL
);

CREATE TABLE IF NOT EXISTS app_secrets (
    name  TEXT PRIMARY KEY,
    value BYTEA NOT NULL
);
