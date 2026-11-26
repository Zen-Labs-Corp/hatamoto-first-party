-- +goose Up
-- The generated day (YYYY-MM-DD) each date of a repeating event comes from: a
-- changed date keeps it when it moves, so its calendar UID stays its own.
-- Rows written before this migration keep '' until the next reconcile.
ALTER TABLE occurrences ADD COLUMN day text NOT NULL DEFAULT '';
