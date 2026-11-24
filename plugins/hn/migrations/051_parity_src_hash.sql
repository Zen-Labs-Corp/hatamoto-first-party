-- +goose Up
-- Hash of the comment HTML a search document was built from (phase: parity), so
-- the search cron can tell edited or anonymised comments from current ones.
-- Existing rows get '' and are re-indexed once.

ALTER TABLE search_docs ADD COLUMN src_hash text NOT NULL DEFAULT '';
