-- +goose Up
-- Identity is CCP's character_id. Players re-authenticate; no users rows
-- are carried forward (DB.md). The EVE grant stays on characters until T17.

ALTER TABLE characters DROP CONSTRAINT IF EXISTS characters_user_id_fkey;
DROP INDEX IF EXISTS characters_user_id;
ALTER TABLE login_states DROP CONSTRAINT IF EXISTS login_states_user_id_fkey;
ALTER TABLE auth_codes DROP CONSTRAINT IF EXISTS auth_codes_user_id_fkey;

ALTER TABLE login_states DROP COLUMN IF EXISTS kind;
ALTER TABLE login_states DROP COLUMN IF EXISTS user_id;

ALTER TABLE auth_codes DROP COLUMN IF EXISTS user_id;
ALTER TABLE auth_codes ADD COLUMN IF NOT EXISTS character_id BIGINT NOT NULL DEFAULT 0;

ALTER TABLE confirm_tokens DROP COLUMN IF EXISTS user_id;
ALTER TABLE confirm_tokens ADD COLUMN IF NOT EXISTS character_id BIGINT NOT NULL DEFAULT 0;

ALTER TABLE characters DROP COLUMN IF EXISTS user_id;
ALTER TABLE characters ADD COLUMN IF NOT EXISTS deleted_at TIMESTAMPTZ;

DROP TABLE IF EXISTS mail_log;
CREATE TABLE mail_log (
    character_id BIGINT NOT NULL,
    sent_at      TIMESTAMPTZ NOT NULL
);
CREATE INDEX mail_log_character_sent ON mail_log (character_id, sent_at);

DROP TABLE IF EXISTS users;
