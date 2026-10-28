# stats: install stats receiver as a plugin

Receives the opt-in install stats ping that Hatamoto sends once a day
(`POST /p/stats/v1/ping` with exactly `install`, `version` and `channel`) and shows
the totals to the site owner on the plugin's admin page. It is only useful on the
site that receives the pings (hatamoto.dev).

## Privacy: what hatamoto.dev keeps

- **Stored**: the install ID (a random 32-character hex string the install made
  for itself), the Hatamoto version, the update channel (`stable`, `edge` or
  `canary`) and the UTC day the ping arrived. Nothing else.
- **Never stored**: an IP address (the plugin host never passes one to a plugin),
  request headers (including the User-Agent), cookies, or the request body. The
  plugin reads no header except `Content-Type`, and logs nothing.
- **Retention**: one row per install and day for the current month and the 13
  full months before it (up to about 14 months). After that the daily
  rows of a month are folded into one row per install and month holding the last
  version and channel seen that month, and the daily rows are deleted. The install
  IDs are kept in those monthly rows with no end date.
- **Who sees it**: only the site owner, as totals on the plugin's admin page. There
  is no public stats page, no per-install page, no export.
- **Request log**: Hatamoto itself logs every request, including these. That log
  keeps method, path, time, status, size, duration, the address of the proxy in
  front of Hatamoto and the user agent, never the body, and is rotated by the
  system journal. The plugin cannot change that.
- **Reset**: an install that resets its ID shows up as a new install; the old ID is
  not linked to the new one.

What an install sends, and how to turn it off, is described in Hatamoto's install
stats documentation on https://hatamoto.dev/docs.

## Set up

1. Install the plugin (Admin > Settings > Plugins) and grant its one permission,
   `db`. Until `db` is granted the ping route answers `503`.
2. Open the **Install stats** page (owner only).

## How it works

- **Ping**: `POST /p/stats/v1/ping` is a public route. It accepts a JSON body (at
  most 1 KiB, `Content-Type: application/json`) that is an object with exactly the
  three keys `install` (32 lowercase hex), `version` (a release version such as
  `v1.8.2` or `v1.8.2-rc.1`, up to 64 characters, no build metadata) and `channel`
  (`stable`, `edge`, `canary`). Extra, missing, repeated or differently-cased keys,
  non-string values and trailing data are rejected (`400`, `413`, `415` or `422`)
  without touching the database. A valid ping answers `204` with an empty body.
  A second ping the same UTC day replaces that day's version and channel.
- **Admin page**: `GET /api/v1/p/stats/overview?days=7|30|90` (owner only, default
  30): active installs (seen in the window), installs seen today, new installs,
  split by version (top 3, then minor groups, then other) and by channel, and a
  13-month trend.
- **Rollup**: the `rollup` cron job (hourly) folds months older than the
  retention window, oldest first, up to 3 months per run. It is a cron job, so it
  only runs while Hatamoto's background worker is running; the clock restarts
  when the server or the plugin restarts. Each month is one atomic
  statement, so a partial run loses nothing and re-running changes nothing.
  Reads are correct before the rollup has run.
- **Permissions**: `db` only.
- **Tables**: `installs_daily` (install, day, version, channel) and
  `installs_monthly` (install, month, version, channel).

## Build, test, install

Needs Go 1.27.3 and the public SDK (`hatamoto.dev/sdk`).

```sh
go vet ./... && go test ./...
rm -rf build && mkdir -p build
CGO_ENABLED=0 GOOS=wasip1 GOARCH=wasm go build -buildmode=c-shared -o build/plugin.wasm .
cp plugin.json build/ && cp -r migrations ui build/
```

Release and install, by the site owner:

1. Merge to `main` with CI green, bump `version` in `plugin.json` (start `1.0.0`)
   and tag `vX.Y.Z` on the merge commit.
2. Build into `build/` as above.
3. Pack and sign with the first-party key, using the CLI built from a Hatamoto
   checkout: `go run ./cmd/hatamoto plugin pack <path>/build --key <key file> --out stats-X.Y.Z.hmp`.
   The key stays with the owner, never in the repo or CI.
4. Install on the site: Admin > Settings > Plugins > upload the `.hmp` and tick
   `db`, or `hatamoto package install stats-X.Y.Z.hmp --grant db` on the host.
   Migrations run on the grant. To update, bump the version and repeat; grants are kept.
5. Check it live: send a ping with `curl -X POST -H 'Content-Type: application/json'
   -d '{"install":"6f1c0a9e5b7d4e28a3c1f09d7b2e4a65","version":"v1.8.2","channel":"edge"}' https://hatamoto.dev/p/stats/v1/ping`
   (expect `204`), then open the admin page.

The plugin must be live before a Hatamoto release with install stats reaches the
stable channel. It is not listed in the public plugin registry.

## Limits

- The overview is four queries (headline, channels, versions, trend) and builds
  the splits in the plugin. Measured on a local PostgreSQL with 10,000 installs
  that pinged every day for 13 months (about 4 million daily rows, the worst
  case), the four queries together take under 1 second, for a 7, 30 or 90 day
  window. More installs scale it about linearly; it has not been measured
  beyond 10,000.
- The version split shows at most 1,000 versions by name; any further versions
  are summed into "other", so the bars always add up to the active installs.
- The public route is rate limited by Hatamoto (10 requests per second, burst 30).
- Days are UTC days, computed in the plugin, not by the database.

## License

Apache License 2.0, see [LICENSE](LICENSE).
