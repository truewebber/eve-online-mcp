-- +goose Up
-- One append-only row per in-game change the server attempted. The mail
-- cap counts successful eve_mail_send rows from this log (DB.md, SPEC §5.4).
-- Players re-authenticate; mail_log is not carried forward.

CREATE TABLE mutations (
    id           BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    character_id BIGINT NOT NULL REFERENCES characters (character_id),
    session_id   BIGINT REFERENCES sessions (id) ON DELETE SET NULL,
    tool         TEXT NOT NULL,
    capability   TEXT NOT NULL,
    args_digest  TEXT NOT NULL,
    summary      TEXT NOT NULL,
    outcome      TEXT NOT NULL,
    esi_status   INT,
    error        TEXT,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX mutations_character_created ON mutations (character_id, created_at DESC);
CREATE INDEX mutations_mail_cap ON mutations (character_id, created_at)
    WHERE tool = 'eve_mail_send' AND outcome = 'ok';

DROP TABLE IF EXISTS mail_log;
