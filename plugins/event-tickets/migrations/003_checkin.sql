-- +goose Up
-- Check-in: when and by which admin user a ticket was let in. NULL = not yet.
ALTER TABLE attendees
    ADD COLUMN checked_in_at timestamptz,
    ADD COLUMN checked_in_by text;
CREATE INDEX attendees_checkin_idx ON attendees (event_id, checked_in_at);
