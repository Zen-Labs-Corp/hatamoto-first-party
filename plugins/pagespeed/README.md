# pagespeed

PageSpeed and Core Web Vitals: tracks field data from the Chrome UX Report and Lighthouse lab scores for your key pages, with daily trends.

It covers what the Site Kit PageSpeed module and Performance Lab give WordPress sites: real-user Core Web Vitals (LCP, INP, CLS) with the web.dev Good / Needs improvement / Poor verdict, the Lighthouse performance score, a 90-day sparkline per metric, and a whole-site summary.

## Use

1. Install the plugin from Admin > Settings > Plugins and grant the permissions below.
2. In Google Cloud, create an API key and enable the **PageSpeed Insights API** and the **Chrome UX Report API** for it.
3. Open the plugin's **Settings** tab and paste the key (it is stored write-only and never shown again).
4. Optionally list the pages to track, one path per line (for example `/pricing`, at most 25), and pick the device: mobile, desktop or both. With an empty list the plugin tracks the home page and the 24 most recently published pages.
5. If the site address is not set in Settings > General, fill in **Public address of this site** (for example `https://example.com`).
6. Open the **PageSpeed** page. The first measurements arrive within minutes; **Measure now** on a row measures that page right away.

A page without enough real-user traffic shows "Not enough field data" instead of an error. The thresholds are web.dev's: LCP 2.5 s / 4 s, INP 200 ms / 500 ms, CLS 0.1 / 0.25.

## Permissions

- `settings`: reads the API key, the page list, the device and the public address.
- `http.fetch`: calls the PageSpeed Insights API (`www.googleapis.com`) and the Chrome UX Report API (`chromeuxreport.googleapis.com`), the only two hosts it may reach.
- `db`: stores the measurements, the day's progress and applies the retention.
- `entities.read`: lists published pages when no page list is set, and reads the site address core knows.
- `log`: writes one line per failed measurement (page path and device; never the key or a response).

It asks for no `kv` and no `site.scripts`: nothing is added to your public pages.

## Data kept

- Per page, day (UTC) and device: the LCP, INP and CLS 75th percentile, their category, the Lighthouse performance score and its status, and when it was measured.
- Per day and device: the same field values for the whole origin.
- The day's progress (which steps are done), deleted after a week.
- Retention: measurements older than 13 months are deleted by the cron job.
- Sent to Google: the page URLs (and the site origin) and the API key, nothing else. The key is kept as a write-only setting and is never logged or returned by a route.

## Build

From the repository root: `scripts/build-plugin.sh plugins/pagespeed`.

## Limits

- At most 25 tracked pages. The whole-site summary comes from the Chrome UX Report origin record, which covers every page; measuring every page of a large site is not a goal.
- The cron job runs every 5 minutes and each call works for about 8 seconds (the host allows 10 s): several field lookups, then at most one PageSpeed Insights run. Each page and device is measured once per UTC day; 25 pages on both devices take a few hours.
- Lab scores are best-effort. The host caps each plugin call at 10 s and a Lighthouse run can take longer, so a run often times out. Each page and device gets up to 3 lab tries per UTC day (field data is always kept), and the table shows the newest finished score with its day until a new one arrives.
- The Chrome UX Report has no data for low-traffic pages.
- **Measure now** works within the same 10 second cap and may not finish its lab run either.

## License

Apache-2.0.
