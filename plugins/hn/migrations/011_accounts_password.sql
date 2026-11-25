-- +goose Up
-- Password logins (phase: accounts). One row per member who has a password.
-- Kept out of users: users is read as to_jsonb(u), so a hash or email there would ride along.

CREATE TABLE user_logins (
    user_id bigint PRIMARY KEY REFERENCES users (id) ON DELETE CASCADE,
    account text NOT NULL UNIQUE,
    pw_hash text NOT NULL,
    email text NOT NULL DEFAULT '',
    pw_changed_at timestamptz NOT NULL DEFAULT now(),
    created_at timestamptz NOT NULL DEFAULT now()
);
