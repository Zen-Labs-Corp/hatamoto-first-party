-- +goose Up
-- Why a story or comment is dead (phase: moderation): enough flags, the owner,
-- or a dead-mode ban. A vouch restores only items killed by flags.

CREATE TABLE kills (
    kind text NOT NULL CHECK (kind IN ('story', 'comment')),
    target_id uuid NOT NULL,
    cause text NOT NULL CHECK (cause IN ('flags', 'owner', 'ban')),
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (kind, target_id)
);
