-- +goose Up
-- A story killed because its site was banned (phase: moderation). Removing the
-- site ban brings these stories back; no vouch can.

ALTER TABLE kills DROP CONSTRAINT kills_cause_check;
ALTER TABLE kills ADD CONSTRAINT kills_cause_check CHECK (cause IN ('flags', 'owner', 'ban', 'domain'));
