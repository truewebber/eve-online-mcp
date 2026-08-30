CREATE TABLE users (
    id         TEXT PRIMARY KEY,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE characters (
    character_id  BIGINT PRIMARY KEY,
    user_id       TEXT NOT NULL REFERENCES users (id),
    name          TEXT NOT NULL,
    owner_hash    TEXT NOT NULL DEFAULT '',
    refresh_token TEXT NOT NULL,
    scopes        TEXT[] NOT NULL DEFAULT '{}',
    added_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX characters_user_id ON characters (user_id);

CREATE TABLE oauth_clients (
    client_id     TEXT PRIMARY KEY,
    redirect_uris TEXT[] NOT NULL DEFAULT '{}',
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE login_states (
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

CREATE TABLE auth_codes (
    code           TEXT PRIMARY KEY,
    user_id        TEXT NOT NULL REFERENCES users (id),
    mcp_client_id  TEXT NOT NULL,
    redirect_uri   TEXT NOT NULL,
    code_challenge TEXT NOT NULL,
    expires_at     TIMESTAMPTZ NOT NULL
);

CREATE TABLE confirm_tokens (
    token       TEXT PRIMARY KEY,
    user_id     TEXT NOT NULL,
    tool        TEXT NOT NULL,
    args_digest TEXT NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE mail_log (
    user_id TEXT NOT NULL,
    sent_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX mail_log_user_sent ON mail_log (user_id, sent_at);

CREATE TABLE http_cache (
    key        TEXT PRIMARY KEY,
    etag       TEXT NOT NULL DEFAULT '',
    expires_at TIMESTAMPTZ NOT NULL,
    stored_at  TIMESTAMPTZ NOT NULL,
    pages      INTEGER,
    body       JSONB NOT NULL
);
CREATE INDEX http_cache_expires ON http_cache (expires_at);

CREATE TABLE names (
    id       BIGINT PRIMARY KEY,
    name     TEXT NOT NULL,
    category TEXT NOT NULL DEFAULT ''
);

CREATE TABLE blobs (
    key       TEXT PRIMARY KEY,
    stored_at TIMESTAMPTZ NOT NULL,
    value     JSONB NOT NULL
);

CREATE TABLE app_secrets (
    name  TEXT PRIMARY KEY,
    value BYTEA NOT NULL
);
