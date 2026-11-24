-- +goose Up
-- Comment mirror and comment votes (phase: comments).

CREATE TABLE comment_meta (
    comment_id uuid PRIMARY KEY,
    entity_id uuid NOT NULL,
    parent_id uuid,
    author_id bigint REFERENCES users (id),
    author_name text NOT NULL DEFAULT '',
    body_html text NOT NULL DEFAULT '',
    live boolean NOT NULL DEFAULT true,
    points integer NOT NULL DEFAULT 1,
    rank double precision NOT NULL DEFAULT 0,
    dirty boolean NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX comment_meta_entity_idx ON comment_meta (entity_id);
CREATE INDEX comment_meta_author_idx ON comment_meta (author_id, created_at DESC);
CREATE INDEX comment_meta_dirty_idx ON comment_meta (dirty) WHERE dirty;
CREATE INDEX comment_meta_created_idx ON comment_meta (created_at DESC);

CREATE TABLE comment_votes (
    comment_id uuid NOT NULL REFERENCES comment_meta (comment_id) ON DELETE CASCADE,
    user_id bigint NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    value smallint NOT NULL CHECK (value IN (1, -1)),
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (comment_id, user_id)
);
