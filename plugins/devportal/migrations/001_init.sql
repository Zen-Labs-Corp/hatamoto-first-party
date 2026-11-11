-- +goose Up
CREATE TABLE accounts (
  id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  github_id bigint NOT NULL UNIQUE,
  login text NOT NULL,
  display_name text NOT NULL,
  email text NOT NULL,
  plan text NOT NULL DEFAULT 'free',
  status text NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'suspended')),
  terms_accepted_at timestamptz NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE keys (
  id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  account bigint NOT NULL UNIQUE REFERENCES accounts (id) ON DELETE CASCADE,
  pubkey text NOT NULL UNIQUE,
  key_id text NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now()
);

-- id is the sha256 hex of the cookie token; account stays NULL until the
-- terms are accepted, the gh_* columns carry the GitHub identity until then.
CREATE TABLE sessions (
  id text PRIMARY KEY,
  account bigint REFERENCES accounts (id) ON DELETE CASCADE,
  csrf text NOT NULL,
  gh_id bigint,
  gh_login text,
  gh_emails jsonb,
  created_at timestamptz NOT NULL DEFAULT now(),
  expires_at timestamptz NOT NULL
);

CREATE INDEX sessions_expires_idx ON sessions (expires_at);
CREATE INDEX sessions_account_idx ON sessions (account);
CREATE INDEX accounts_login_idx ON accounts (lower(login));
