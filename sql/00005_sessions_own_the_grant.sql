-- +goose Up
-- The EVE grant moves from the character row to the session of that
-- sign-in (DB.md, SPEC §3.1–3.3). Players re-authenticate; parked
-- confirm tokens die with the old binding.

CREATE TABLE sessions (
    id            BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    character_id  BIGINT NOT NULL REFERENCES characters (character_id),
    refresh_token TEXT,
    scopes        TEXT[] NOT NULL,
    mcp_client_id TEXT NOT NULL,
    client_name   TEXT NOT NULL DEFAULT '',
    ip            TEXT NOT NULL DEFAULT '',
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    valid_til     TIMESTAMPTZ NOT NULL,
    revoked_at    TIMESTAMPTZ
);
CREATE UNIQUE INDEX sessions_one_live ON sessions (character_id) WHERE revoked_at IS NULL;
CREATE INDEX sessions_character ON sessions (character_id);

ALTER TABLE auth_codes ADD COLUMN IF NOT EXISTS refresh_token TEXT NOT NULL DEFAULT '';
ALTER TABLE auth_codes ADD COLUMN IF NOT EXISTS scopes TEXT[] NOT NULL DEFAULT '{}';

ALTER TABLE oauth_clients ADD COLUMN IF NOT EXISTS client_name TEXT NOT NULL DEFAULT '';

DELETE FROM confirm_tokens;
ALTER TABLE confirm_tokens ADD COLUMN session_id BIGINT NOT NULL REFERENCES sessions (id) ON DELETE CASCADE;
ALTER TABLE confirm_tokens ADD COLUMN expires_at TIMESTAMPTZ NOT NULL DEFAULT now() + interval '300 seconds';
ALTER TABLE confirm_tokens DROP COLUMN IF EXISTS character_id;

ALTER TABLE characters DROP COLUMN IF EXISTS refresh_token;
ALTER TABLE characters DROP COLUMN IF EXISTS scopes;
