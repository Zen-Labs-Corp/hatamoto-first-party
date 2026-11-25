-- +goose Up
-- Lifecycle mails and campaigns: change markers, campaigns, per-person sends and the suppression list.
ALTER TABLE events ADD COLUMN venue_key text;   -- NULL on rows from before this migration: a change is only recorded once a key exists

CREATE TABLE event_changes (
    id         bigserial PRIMARY KEY,
    event_id   text NOT NULL,
    change_key text NOT NULL,
    what       text NOT NULL CHECK (what ~ '^(date|venue|status)(,(date|venue|status))*$'),
    created_at timestamptz NOT NULL DEFAULT now(),
    fanned_at  timestamptz,
    UNIQUE (event_id, change_key));
CREATE INDEX event_changes_todo_idx ON event_changes (id) WHERE fanned_at IS NULL;

CREATE TABLE campaigns (
    id         bigserial PRIMARY KEY,
    subject    text NOT NULL CHECK (length(subject) BETWEEN 1 AND 200),
    format     text NOT NULL CHECK (format IN ('text', 'html')),
    body       text NOT NULL CHECK (length(body) <= 8000),
    segment    jsonb NOT NULL,
    event_id   text NOT NULL DEFAULT '',
    send_at    timestamptz NOT NULL,
    status     text NOT NULL DEFAULT 'scheduled' CHECK (status IN ('scheduled', 'sending', 'sent', 'cancelled')),
    created_at timestamptz NOT NULL DEFAULT now());
CREATE INDEX campaigns_due_idx ON campaigns (send_at) WHERE status = 'scheduled';

CREATE TABLE campaign_links (
    campaign_id bigint NOT NULL REFERENCES campaigns (id) ON DELETE CASCADE,
    idx         int NOT NULL CHECK (idx BETWEEN 0 AND 19),
    url         text NOT NULL CHECK (url ~ '^https?://'),
    path        text CHECK (path IS NULL OR (path ~ '^/' AND path !~ '^//' AND path !~ '^/\\')),
    PRIMARY KEY (campaign_id, idx));

CREATE TABLE email_sends (
    id          bigserial PRIMARY KEY,
    kind        text NOT NULL CHECK (kind IN ('changed', 'reminder', 'thanks', 'campaign')),
    ref_id      bigint NOT NULL DEFAULT 0,          -- event_changes.id, campaigns.id or 0
    event_id    text NOT NULL DEFAULT '',
    order_id    bigint NOT NULL,                    -- the address is read at send time, no FK as orders may be deleted
    email_hash  text NOT NULL CHECK (email_hash ~ '^[0-9a-f]{64}$'),
    code        text NOT NULL UNIQUE CHECK (code ~ '^[0-9a-f]{32}$'),
    status      text NOT NULL DEFAULT 'queued' CHECK (status IN ('queued', 'sent', 'skipped', 'failed')),
    attempts    int NOT NULL DEFAULT 0,
    next_at     timestamptz NOT NULL DEFAULT now(),
    created_at  timestamptz NOT NULL DEFAULT now(),
    sent_at     timestamptz,
    opened_at   timestamptz,
    open_count  int NOT NULL DEFAULT 0,
    clicked_at  timestamptz,
    click_count int NOT NULL DEFAULT 0,
    UNIQUE (kind, ref_id, event_id, email_hash));
CREATE INDEX email_sends_queue_idx ON email_sends (next_at) WHERE status = 'queued';
CREATE INDEX email_sends_ref_idx ON email_sends (kind, ref_id);
CREATE INDEX email_sends_created_idx ON email_sends (created_at);

CREATE TABLE unsubscribes (
    email_hash text PRIMARY KEY CHECK (email_hash ~ '^[0-9a-f]{64}$'),
    created_at timestamptz NOT NULL DEFAULT now());
