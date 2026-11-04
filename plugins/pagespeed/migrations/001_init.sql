-- +goose Up
-- One row per (page, day, device): CrUX field p75 values with their web.dev
-- category, and the Lighthouse performance score.
CREATE TABLE metrics (
    url         text NOT NULL,
    day         date NOT NULL,
    strategy    text NOT NULL,
    lcp_ms      double precision,
    inp_ms      double precision,
    cls         double precision,
    lcp_cat     text NOT NULL DEFAULT '',
    inp_cat     text NOT NULL DEFAULT '',
    cls_cat     text NOT NULL DEFAULT '',
    field_status text NOT NULL DEFAULT 'none',   -- ok | none | error
    lab_score   integer,
    lab_status  text NOT NULL DEFAULT 'pending', -- ok | timeout | error | pending
    measured_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (url, day, strategy)
);
CREATE INDEX metrics_day ON metrics (day);

-- CrUX origin record: the whole-site summary per day and device.
CREATE TABLE origin_summary (
    day      date NOT NULL,
    strategy text NOT NULL,
    lcp_ms   double precision,
    inp_ms   double precision,
    cls      double precision,
    lcp_cat  text NOT NULL DEFAULT '',
    inp_cat  text NOT NULL DEFAULT '',
    cls_cat  text NOT NULL DEFAULT '',
    status   text NOT NULL DEFAULT 'none',       -- ok | none | error
    PRIMARY KEY (day, strategy)
);

-- Checkpoint of the current UTC day: which steps are done, so every call
-- continues where the last one stopped.
CREATE TABLE progress (
    day          date NOT NULL,
    url          text NOT NULL,
    strategy     text NOT NULL,
    crux_done    boolean NOT NULL DEFAULT false,
    lab_state    text NOT NULL DEFAULT 'pending', -- pending | ok | timeout | error
    lab_attempts integer NOT NULL DEFAULT 0,
    PRIMARY KEY (day, url, strategy)
);
