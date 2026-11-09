-- +goose Up
-- Detailed reports: one row per (install, UTC day) for environment, theme and
-- health, and one row per (install, day, plugin). The CHECKs repeat the guest's
-- validation. No IP, header or raw body is stored.
CREATE TABLE detail_daily (
    install             text NOT NULL CHECK (install ~ '^[0-9a-f]{32}$'),
    day                 date NOT NULL,
    core_version        text NOT NULL CHECK (length(core_version) <= 64),
    os                  text NOT NULL CHECK (os ~ '^[a-z0-9]{1,16}$'),
    arch                text NOT NULL CHECK (arch ~ '^[a-z0-9]{1,16}$'),
    postgres            int NOT NULL CHECK (postgres BETWEEN 9 AND 99),
    age_months          int NOT NULL CHECK (age_months IN (0, 1, 3, 6, 12, 24, 36)),
    worker              boolean NOT NULL,
    theme_name          text NOT NULL CHECK (theme_name ~ '^[a-z0-9][a-z0-9-]{0,63}$'),
    theme_version       text NOT NULL CHECK (length(theme_version) <= 64),
    other_plugins       int NOT NULL CHECK (other_plugins BETWEEN 0 AND 1000),
    since_hours         int NOT NULL CHECK (since_hours BETWEEN 0 AND 10000),
    panics              int NOT NULL CHECK (panics BETWEEN 0 AND 1000000),
    http_5xx            int NOT NULL CHECK (http_5xx BETWEEN 0 AND 1000000),
    render_errors       int NOT NULL CHECK (render_errors BETWEEN 0 AND 1000000),
    other_plugin_errors int NOT NULL CHECK (other_plugin_errors BETWEEN 0 AND 1000000),
    update_failed       int NOT NULL CHECK (update_failed BETWEEN 0 AND 1000000),
    update_rolled_back  int NOT NULL CHECK (update_rolled_back BETWEEN 0 AND 1000000),
    migration_failed    int NOT NULL CHECK (migration_failed BETWEEN 0 AND 1000000),
    PRIMARY KEY (install, day)
);
CREATE INDEX detail_daily_day ON detail_daily (day, install);

CREATE TABLE plugins_daily (
    install text NOT NULL CHECK (install ~ '^[0-9a-f]{32}$'),
    day     date NOT NULL,
    name    text NOT NULL CHECK (name ~ '^[a-z0-9][a-z0-9-]{0,63}$'),
    version text NOT NULL CHECK (length(version) <= 64),
    enabled boolean NOT NULL,
    errors  int NOT NULL CHECK (errors BETWEEN 0 AND 1000000),
    PRIMARY KEY (install, day, name)
);
CREATE INDEX plugins_daily_day ON plugins_daily (day, install);

-- Folded months: environment, theme and versions as last seen that month,
-- counts summed. Install IDs are kept.
CREATE TABLE detail_monthly (
    install             text NOT NULL CHECK (install ~ '^[0-9a-f]{32}$'),
    month               date NOT NULL,
    core_version        text NOT NULL CHECK (length(core_version) <= 64),
    os                  text NOT NULL CHECK (os ~ '^[a-z0-9]{1,16}$'),
    arch                text NOT NULL CHECK (arch ~ '^[a-z0-9]{1,16}$'),
    postgres            int NOT NULL CHECK (postgres BETWEEN 9 AND 99),
    age_months          int NOT NULL CHECK (age_months IN (0, 1, 3, 6, 12, 24, 36)),
    worker              boolean NOT NULL,
    theme_name          text NOT NULL CHECK (theme_name ~ '^[a-z0-9][a-z0-9-]{0,63}$'),
    theme_version       text NOT NULL CHECK (length(theme_version) <= 64),
    other_plugins       int NOT NULL CHECK (other_plugins BETWEEN 0 AND 1000),
    report_days         int NOT NULL CHECK (report_days BETWEEN 1 AND 31),
    panics              bigint NOT NULL,
    http_5xx            bigint NOT NULL,
    render_errors       bigint NOT NULL,
    other_plugin_errors bigint NOT NULL,
    update_failed       bigint NOT NULL,
    update_rolled_back  bigint NOT NULL,
    migration_failed    bigint NOT NULL,
    PRIMARY KEY (install, month)
);
CREATE INDEX detail_monthly_month ON detail_monthly (month);

CREATE TABLE plugins_monthly (
    install text NOT NULL CHECK (install ~ '^[0-9a-f]{32}$'),
    month   date NOT NULL,
    name    text NOT NULL CHECK (name ~ '^[a-z0-9][a-z0-9-]{0,63}$'),
    version text NOT NULL CHECK (length(version) <= 64),
    enabled boolean NOT NULL,
    errors  bigint NOT NULL,
    PRIMARY KEY (install, month, name)
);
CREATE INDEX plugins_monthly_month ON plugins_monthly (month);
