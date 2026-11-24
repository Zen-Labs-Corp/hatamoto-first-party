-- +goose Up
-- Append-only log of story submissions and edits (phase: stories). Deleting a
-- story never removes its rows, so submit_per_hour cannot be dodged by
-- submitting and deleting.

CREATE TABLE story_actions (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    author_id bigint NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    kind text NOT NULL CHECK (kind IN ('submit', 'edit')),
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX story_actions_author_idx ON story_actions (author_id, kind, created_at DESC);
