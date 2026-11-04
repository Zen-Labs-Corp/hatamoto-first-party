# bing-webmaster: Bing Webmaster Tools as a plugin

Replaces the WordPress "Bing Webmaster Tools" plugin. Paste your Bing API key and
see Bing clicks, impressions, top queries, top pages, crawl issues and sitemap
status on one admin page, refreshed every 6 hours, and submit your sitemap with
one click.

## Use

1. In Bing Webmaster Tools open **Settings > API access** and create an API key.
   Your site must already be verified there.
2. Install the plugin (Admin > Settings > Plugins) and grant its permissions.
3. In the plugin's settings paste the **Bing API key** (write-only: it is never
   shown again). Optionally fill in **Site URL**, exactly as the site is verified
   in Bing (a trailing slash matters). Left empty, the plugin uses the one
   verified site of your Bing account; with none or several it asks you to fill
   the field in.
4. Open the **Bing Webmaster Tools** page. *Test key* checks the key and the
   site, *Import now* fetches the data right away, *Submit sitemap* sends
   `<your site>/sitemap.xml` to Bing. Without an API key the page only asks you
   to add one in Settings.

## Permissions

| Permission | Why |
|---|---|
| `settings` | Read the API key and the site URL. |
| `http.fetch` (host `ssl.bing.com` only) | Call the Bing Webmaster JSON API; nothing else is reachable. |
| `db` | Keep the stats, the crawl issues, the sitemap list, the sitemap submissions and the import log in the plugin's own tables. |
| `kv` | Remember how far a partial import got and the site resolved from your Bing account (10 minutes). |
| `log` | Log import problems. The key and request addresses are never logged. |

All routes (`GET /overview`, `POST /test`, `POST /submit-sitemap`,
`POST /import-now`) are owner only; there is no public route and no render hook.

## Data kept

Everything is in the plugin's own tables; nothing is sent anywhere but Bing.

- `traffic_daily`, `query_stats`, `page_stats`: Bing's daily site totals and weekly
  query and page rows, kept 16 months, then deleted (retention runs after every import, even
  when one of its lists failed).
- `crawl_issues`, `feeds`: the latest snapshot only. A successful fetch upserts
  every row, then removes the rows Bing no longer lists; a failed one keeps the
  old snapshot. Addresses over 2000 bytes are skipped.
- `import_runs`, `sitemap_submissions`: kept 30 days.
- `kv`: the import cursor and the resolved site.

The API key is stored by Hatamoto as a write-only setting. It travels in the
query string of Bing's API, so the plugin never logs or returns a request
address, and error texts are built from Bing's status and message only.

## Build

```sh
scripts/build-plugin.sh plugins/bing-webmaster    # vet, tests, wasm into plugins/bing-webmaster/build/
```

Needs Go (see `go.mod`) and the public SDK (`hatamoto.dev/sdk`). The Bing address
is a constant in `bing.go`; the manifest `config` key `bing_base` overrides it for
tests or a proxy you package yourself. It is not a setting, because the API key is
sent to it, and `allowed_hosts` still decides what the plugin can reach. The
tests use a fake host and never call Bing.

## Limits

- Bing reports queries and pages by week, so the top lists cover the last 4 weeks
  of stored weekly rows; only the 28-day totals are daily. Bing refreshes its
  numbers about once a week.
- A plugin may receive 1 MiB per answer. A site whose query or page list is
  larger gets an error for that list (shown on the page); the other cards still
  work.
- One site per install; the JSON API only.
- The first import runs within 6 hours of installing, or press *Import now*. One
  run has 10 seconds, so a first import may take a few runs; it continues where it
  stopped.
