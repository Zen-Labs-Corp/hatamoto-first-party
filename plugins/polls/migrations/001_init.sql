-- +goose Up

-- One row per counted submission. The key makes the live hook idempotent
-- (a redelivered submission is counted once) and gives the response total.
-- score and max_score are only set for quizzes.
CREATE TABLE poll_response (
    form          text        NOT NULL,
    submission_id text        NOT NULL,
    score         integer,
    max_score     integer,
    created_at    timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (form, submission_id)
);

-- One row per form, question field and option: how many times it was chosen.
CREATE TABLE poll_tally (
    form   text    NOT NULL,
    field  text    NOT NULL,
    option text    NOT NULL,
    n      integer NOT NULL DEFAULT 0,
    PRIMARY KEY (form, field, option)
);
