-- +goose Up
CREATE TABLE names (
  name text PRIMARY KEY,
  owner bigint NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
  created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE submissions (
  id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  account bigint NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
  kind text NOT NULL CHECK (kind IN ('plugin','theme')),
  name text NOT NULL,
  version text NOT NULL DEFAULT '',
  url text NOT NULL,
  source text NOT NULL DEFAULT '',
  title text NOT NULL DEFAULT '',
  about text NOT NULL DEFAULT '',
  category text NOT NULL DEFAULT '',
  site_type text NOT NULL DEFAULT '',
  screenshot text NOT NULL DEFAULT '',
  sha256 text NOT NULL DEFAULT '',
  size bigint NOT NULL DEFAULT 0,
  permissions jsonb NOT NULL DEFAULT '[]',
  allowed_hosts jsonb NOT NULL DEFAULT '[]',
  description text NOT NULL DEFAULT '',
  signer_key text NOT NULL DEFAULT '',
  checks jsonb NOT NULL DEFAULT '[]',
  status text NOT NULL CHECK (status IN ('refused','waiting','listed','rejected','superseded','revoked')),
  reason text NOT NULL DEFAULT '',
  created_at timestamptz NOT NULL DEFAULT now(),
  decided_at timestamptz
);
CREATE INDEX submissions_account_idx ON submissions (account, created_at DESC);
CREATE INDEX submissions_status_idx ON submissions (status, created_at);

CREATE TABLE listings (
  id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  kind text NOT NULL CHECK (kind IN ('plugin','theme')),
  name text NOT NULL,
  account bigint NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
  submission bigint NOT NULL REFERENCES submissions(id),
  version text NOT NULL,
  entry jsonb NOT NULL,
  status text NOT NULL CHECK (status IN ('active','suspended','revoked','dropped')),
  reason text NOT NULL DEFAULT '',
  check_failures int NOT NULL DEFAULT 0,
  checked_at timestamptz NOT NULL DEFAULT now(),
  listed_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (kind, name)
);

CREATE TABLE revocations (
  id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  signer_key text NOT NULL DEFAULT '',
  name text NOT NULL DEFAULT '',
  version text NOT NULL DEFAULT '',
  reason text NOT NULL DEFAULT '',
  created_at timestamptz NOT NULL DEFAULT now(),
  CHECK (signer_key <> '' OR name <> '')
);

CREATE TABLE mail_queue (
  id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  to_addr text NOT NULL,
  subject text NOT NULL,
  body text NOT NULL,
  attempts int NOT NULL DEFAULT 0,
  created_at timestamptz NOT NULL DEFAULT now(),
  sent_at timestamptz
);
CREATE INDEX mail_queue_pending_idx ON mail_queue (created_at) WHERE sent_at IS NULL;

CREATE TABLE published (
  id int PRIMARY KEY CHECK (id = 1),
  body text NOT NULL,
  issued_at timestamptz NOT NULL,
  expires_at timestamptz NOT NULL
);
