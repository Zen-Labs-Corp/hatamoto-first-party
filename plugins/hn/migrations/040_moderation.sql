-- +goose Up
-- Flags, vouches, bans, rate events and the moderation log (phase: moderation).

CREATE TABLE flags (
    kind text NOT NULL CHECK (kind IN ('story', 'comment')),
    target_id uuid NOT NULL,
    user_id bigint NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (kind, target_id, user_id)
);

CREATE TABLE vouches (
    kind text NOT NULL CHECK (kind IN ('story', 'comment')),
    target_id uuid NOT NULL,
    user_id bigint NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (kind, target_id, user_id)
);

CREATE TABLE bans (
    user_id bigint PRIMARY KEY REFERENCES users (id) ON DELETE CASCADE,
    mode text NOT NULL CHECK (mode IN ('dead', 'blocked')),
    reason text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE banned_domains (
    domain text PRIMARY KEY,
    reason text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE net_bans (
    net_key text PRIMARY KEY,
    reason text NOT NULL DEFAULT '',
    expires_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE rate_events (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    kind text NOT NULL,
    key text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX rate_events_lookup_idx ON rate_events (kind, key, created_at);

CREATE TABLE mod_log (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    actor text NOT NULL,
    action text NOT NULL,
    target text NOT NULL DEFAULT '',
    detail text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now()
);
