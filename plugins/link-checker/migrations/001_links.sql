-- +goose Up
-- One row per distinct link (normalised address). Internal links are site
-- paths, outbound links absolute http(s) addresses.
CREATE TABLE links (
    id           bigserial PRIMARY KEY,
    url          text NOT NULL UNIQUE,
    kind         text NOT NULL CHECK (kind IN ('internal', 'outbound')),
    status       text NOT NULL DEFAULT 'pending'
                 CHECK (status IN ('pending', 'ok', 'broken', 'redirected', 'unknown', 'ignored')),
    http_status  integer,
    error        text,
    last_checked timestamptz,
    first_broken timestamptz,
    next_check   timestamptz NOT NULL DEFAULT now(),
    fail_count   integer NOT NULL DEFAULT 0,
    last_seen    timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX links_status ON links (status);
CREATE INDEX links_next_check ON links (next_check);

-- Where a link is used. Name and path are copied so the admin table needs no
-- entity lookups.
CREATE TABLE link_pages (
    link_id     bigint NOT NULL REFERENCES links (id) ON DELETE CASCADE,
    entity_id   text NOT NULL,
    entity_name text NOT NULL DEFAULT '',
    entity_path text NOT NULL DEFAULT '',
    last_seen   timestamptz NOT NULL DEFAULT now(),
    UNIQUE (link_id, entity_id)
);
CREATE INDEX link_pages_entity ON link_pages (entity_id);

-- Paths of published pages seen by the scan, so a listing page that is the
-- parent of published paths is not reported as missing.
CREATE TABLE known_paths (
    path      text PRIMARY KEY,
    last_seen timestamptz NOT NULL DEFAULT now()
);
