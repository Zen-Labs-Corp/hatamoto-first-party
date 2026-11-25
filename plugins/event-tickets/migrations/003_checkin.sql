-- +goose Up
-- Check-in: when and by which admin user a ticket was let in. NULL = not yet.
ALTER TABLE attendees
    ADD COLUMN checked_in_at timestamptz,
    ADD COLUMN checked_in_by text,
    -- Random id of the scan that checked the ticket in (NULL if it sent none).
    ADD COLUMN checked_in_nonce text;
CREATE INDEX attendees_checkin_idx ON attendees (event_id, checked_in_at);
