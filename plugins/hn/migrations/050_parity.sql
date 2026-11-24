-- +goose Up
-- Favorites, hidden stories, the search mirror and visit windows (phase: parity).

CREATE TABLE favorites (
    user_id bigint NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    kind text NOT NULL CHECK (kind IN ('story', 'comment')),
    target_id uuid NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, kind, target_id)
);

CREATE TABLE hidden (
    user_id bigint NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    story_id uuid NOT NULL REFERENCES story_meta (entity_id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, story_id)
);

-- tsv is a plain column filled by the phase, so the text search configuration stays its choice.
CREATE TABLE search_docs (
    kind text NOT NULL CHECK (kind IN ('story', 'comment')),
    target_id uuid NOT NULL,
    entity_id uuid NOT NULL,
    title text NOT NULL DEFAULT '',
    body text NOT NULL DEFAULT '',
    tsv tsvector NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (kind, target_id)
);

CREATE INDEX search_docs_tsv_idx ON search_docs USING gin (tsv);

CREATE TABLE visits (
    user_id bigint PRIMARY KEY REFERENCES users (id) ON DELETE CASCADE,
    window_start timestamptz,
    last_seen timestamptz,
    created_at timestamptz NOT NULL DEFAULT now()
);
