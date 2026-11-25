-- +goose Up
-- Votes stored without counting them (phase: stories): the voting-ring check marks
-- a vote uncounted; it shows as voted to its voter but adds no point, score or karma.

ALTER TABLE story_votes ADD COLUMN counted boolean NOT NULL DEFAULT true;
