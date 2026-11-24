-- +goose Up
-- Stories and their votes (phase: stories).

-- Reserve-only: a row is inserted first and its number becomes the story slug.
CREATE TABLE item_numbers (
    item_no bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE story_meta (
    entity_id uuid PRIMARY KEY,
    item_no bigint NOT NULL UNIQUE,
    author_id bigint NOT NULL REFERENCES users (id),
    title text NOT NULL,
    url text NOT NULL DEFAULT '',
    url_key text NOT NULL DEFAULT '',
    domain text NOT NULL DEFAULT '',
    body text NOT NULL DEFAULT '',
    kind text NOT NULL DEFAULT 'story' CHECK (kind IN ('story', 'ask', 'show', 'job')),
    points integer NOT NULL DEFAULT 1,
    comment_count integer NOT NULL DEFAULT 0,
    score double precision NOT NULL DEFAULT 0,
    dead boolean NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX story_meta_score_idx ON story_meta (score DESC) WHERE NOT dead;
CREATE INDEX story_meta_created_idx ON story_meta (created_at DESC);
CREATE INDEX story_meta_kind_score_idx ON story_meta (kind, score DESC);
CREATE INDEX story_meta_author_idx ON story_meta (author_id, created_at DESC);
CREATE INDEX story_meta_url_key_idx ON story_meta (url_key);
CREATE INDEX story_meta_domain_idx ON story_meta (domain, created_at DESC);

CREATE TABLE story_votes (
    story_id uuid NOT NULL REFERENCES story_meta (entity_id) ON DELETE CASCADE,
    user_id bigint NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (story_id, user_id)
);
