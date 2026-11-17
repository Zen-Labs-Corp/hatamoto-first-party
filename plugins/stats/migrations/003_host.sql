-- +goose Up
-- Site address a Detailed report may carry, kept per install and day only.
-- The CHECK repeats the guest's validation. Not copied to monthly rows.
ALTER TABLE detail_daily ADD COLUMN host text
    CHECK (host IS NULL OR (length(host) <= 253 AND host ~ '^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?(\.[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?)+$'));
