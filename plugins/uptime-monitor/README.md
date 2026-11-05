# uptime-monitor: an uptime and SSL expiry monitor for Hatamoto

Checks your site and other web addresses every few minutes, emails you when one goes down or comes back, warns before an SSL certificate expires and can ping an outside heartbeat.

It replaces WordPress add-ons such as Jetpack Monitor, ManageWP uptime, WP Umbrella and the SSL expiry notices of Really Simple SSL.

## Use

1. Install the plugin (Admin > Settings > Plugins) and grant its permissions.
   `http.fetch.any` lets it request any address, so the install screen shows it
   as a warning: that is how the addresses you monitor get checked.
2. Open **Uptime monitor** (the plugin's Settings tab). Add your own site as the
   first monitor and tick **This site**. Add up to 19 more addresses. Each
   monitor has a name, an address (`http://` or `https://`, no user name or
   password), an interval of 1, 5 or 15 minutes, an optional keyword the page
   must contain, and an optional **Slow above** limit in milliseconds.
3. In the plugin settings, add at least one alert email (up to 5). Until then the page shows
   a setup banner and nothing is mailed.
4. Press **Send test alert** to see that mail arrives.

Settings:

- **Alert emails**: where every mail goes, all in one message.
- **Failed checks before a down alert**: 1 to 10, default 2.
- **SSL warning days**: up to 5 values from 1 to 90, default 21, 7 and 1.
- **Heartbeat URL**: optional, secret, never shown again. Create a check at an
  outside heartbeat service, paste the address it gives you here, and the plugin
  requests it after each good check of the monitor marked **This site**, at most
  once a minute. If your server stops, the outside service notices the silence
  and tells you; the plugin cannot, because it runs on the same server.

How a check is judged:

- A request goes out when the monitor is due. Without a keyword it is a HEAD
  request (a GET when the server answers 405 or 501); with a keyword it is a GET
  and the first megabyte of the page must contain the keyword. Redirects are
  followed and the final status counts.
- 2xx and 3xx are **UP**. Above the **Slow above** limit it is **SLOW**, which is
  shown but never mailed. 4xx and 5xx, a missing keyword and a timeout are
  **DOWN** with the cause (`HTTP 503`, `keyword missing`, `timeout after 8000 ms`).
- A monitor that cannot be checked as configured (a private address, a port
  other than 80 or 443, a name that does not resolve, a permission not granted)
  is **CONFIG** with the reason. It is never DOWN, never mailed and not counted in
  uptime.
- **PENDING** is a monitor not checked yet.
- A down mail is sent once, when the failed checks in a row reach the threshold.
  A recovery mail follows once, with the downtime and the cause. A failure that
  recovers before the threshold sends nothing. When a check fails to connect and
  the known certificate has already expired, the cause says so
  (`certificate expired on 2026-03-01`) and the mail goes out on the first failure.
- Certificates of `https://` monitors are looked up on crt.sh at most once every
  12 hours per host. For each warning day you set, one mail is sent when the days
  left reach it, once per certificate; a renewed certificate starts the warnings
  again. The SSL column shows the days left, amber from 21 and red from 7, or
  "unknown" when the lookup failed and "-" when there is nothing to look up yet.

The page lists each monitor with its status, uptime over 24 hours and 30 days,
average response time, last check, SSL days and a 90-day bar (green from 99.9 %,
amber from 99 %, red below, grey without data). **Check now** runs one check at
once (a result younger than 10 seconds is shown as is). Below are the latest 50
incidents.

Mails: `[Down] <name> is down`, `[Up] <name> is back up`,
`[SSL] <host> certificate expires in N days` and `[Test] Uptime monitor alert test`.
Mail goes out at most 6 recipients per minute; the rest waits in a queue for the
next minute.

## Permissions

| Permission | Why |
|---|---|
| `settings` | Read the alert emails, failure threshold, SSL warning days and heartbeat URL. |
| `http.fetch` | Check the monitored addresses, request the heartbeat URL and look up certificates. |
| `http.fetch.any` | Monitors and the heartbeat are addresses you enter, on any host, so the allowed hosts are `*` and `crt.sh`. Shown as a warning at install. Without it every monitor shows CONFIG. |
| `db` | Keep the monitors, checks, incidents, daily rollups, certificate state and mail queue. |
| `kv` | Keep where the check batch stopped, the mail send window, the heartbeat time and the rollup day. |
| `mail.send` | Send down, recovery, certificate and test mails. |
| `log` | One warning line when a step fails, never a full address or the heartbeat URL (a certificate warning names the host). |

No request sends cookies or credentials. The plugin adds nothing to public pages
and has no public route.

## Data kept

Tables in the plugin's own schema:

- `monitors`: your monitors with their current state.
- `checks`: one row per check (time, HTTP status, milliseconds, result, cause),
  kept 30 days.
- `incidents` and `daily` (uptime and average time per day, UTC): kept 13 months.
- `certs`: expiry date, issuer and last lookup per host, until no monitor uses
  the host.
- `outbox`: mail waiting to be sent, until it is sent or 24 hours old.

Deleting a monitor deletes its checks, incidents and daily rows. Page bodies are
never stored: the first megabyte is read to find the keyword and dropped. The
heartbeat URL lives only in the plugin settings. `kv` holds small markers: the
check cursor, the mail window, the heartbeat time, the rollup day and whether
mail is available.

## Build

```sh
scripts/build-plugin.sh plugins/uptime-monitor
```

This runs gofmt, vet and the tests, then builds `build/plugin.wasm` and
collects the package in `plugins/uptime-monitor/build/`.

## Limits

- Checks run inside this site, so if the whole server is down nothing can send
  the alert. Set a heartbeat URL with an outside service for that case.
- Certificate dates come from public certificate transparency logs (crt.sh), not
  from the connection. They can show a newer certificate than the one served,
  lookups fail now and then (the page shows "unknown"), and a failed check still
  catches an expired one.
- A failed DNS lookup, a refused connection and a TLS error cannot be told apart:
  all show as `could not connect (DNS, certificate or refused)`.
- Only ports 80 and 443 are checked.
- Only public addresses are checked; a private address shows as CONFIG.
- A keyword is searched in the first megabyte of the page only. A page over that
  size without the keyword in it shows `keyword missing (page over 1 MB)`.
- Redirects are followed and the final status counts; a redirect itself is not
  reported.
- Mail is limited to 6 recipients per minute, and a mail not sent within 24 hours
  is dropped.
- A check tick uses about 8 seconds of the 10 the host allows, so a very slow
  set of monitors is checked a few at a time; a missed tick is not caught up.
- There is no status page, no SMS or chat alert, no checks from several regions,
  and no TCP, ping or DNS monitors.

## License

Apache-2.0.
