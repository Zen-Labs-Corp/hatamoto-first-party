-- +goose Up
-- The site address may now be a public IP literal; the CHECK repeats the guest's
-- validation (validHost, same prefix list). Feature flags a Detailed report may carry,
-- NULL when not sent; monthly rows keep the newest value of the month. Host stays daily only.
ALTER TABLE detail_daily DROP CONSTRAINT detail_daily_host_check,
    ADD CONSTRAINT detail_daily_host_check CHECK (host IS NULL OR (length(host) <= 253 AND CASE
        WHEN host ~ '^[0-9.]+$' THEN host ~ '^(25[0-5]|2[0-4][0-9]|1[0-9]{2}|[1-9]?[0-9])(\.(25[0-5]|2[0-4][0-9]|1[0-9]{2}|[1-9]?[0-9])){3}$'
            AND NOT host::inet <<= ANY ('{0.0.0.0/8,10.0.0.0/8,100.64.0.0/10,127.0.0.0/8,169.254.0.0/16,172.16.0.0/12,192.0.0.0/24,192.0.2.0/24,192.168.0.0/16,198.18.0.0/15,198.51.100.0/24,203.0.113.0/24,224.0.0.0/4,240.0.0.0/4}'::inet[])
        WHEN host ~ ':' THEN host ~ '^[0-9a-f:]{2,39}$' AND host = host(host::inet)
            AND host::inet <<= '2000::/3'::inet AND NOT host::inet <<= ANY ('{2001::/23,2001:db8::/32,2002::/16}'::inet[])
        ELSE host ~ '^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?(\.[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?)+$' AND host !~ '\.[0-9]+$'
    END)),
    ADD COLUMN comments_on boolean,
    ADD COLUMN static_mode boolean,
    ADD COLUMN sso_on boolean,
    ADD COLUMN locales_bucket text CHECK (locales_bucket IS NULL OR locales_bucket IN ('1', '2-3', '4+')),
    ADD COLUMN forms_in_use boolean,
    ADD COLUMN offsite_backups_on boolean,
    ADD COLUMN csp_enforced boolean,
    ADD COLUMN wp_imported boolean;
ALTER TABLE detail_monthly
    ADD COLUMN comments_on boolean,
    ADD COLUMN static_mode boolean,
    ADD COLUMN sso_on boolean,
    ADD COLUMN locales_bucket text CHECK (locales_bucket IS NULL OR locales_bucket IN ('1', '2-3', '4+')),
    ADD COLUMN forms_in_use boolean,
    ADD COLUMN offsite_backups_on boolean,
    ADD COLUMN csp_enforced boolean,
    ADD COLUMN wp_imported boolean;
