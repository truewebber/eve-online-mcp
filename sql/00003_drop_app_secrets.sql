-- +goose Up
-- The JWT signing key is HMAC_KEY env, not data (DB.md). Existing
-- volumes that applied 00001 still have the rows; drop them.

DROP TABLE IF EXISTS app_secrets;
