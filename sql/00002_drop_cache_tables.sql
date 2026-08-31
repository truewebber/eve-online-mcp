-- +goose Up
-- Cache tables move to pod memory (SPEC §5.1). Existing volumes that
-- applied 00001 still have the rows; drop them. Fresh databases create
-- then drop in the same apply.

DROP TABLE IF EXISTS http_cache;
DROP TABLE IF EXISTS names;
DROP TABLE IF EXISTS blobs;
