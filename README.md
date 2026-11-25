# hatamoto-first-party

The first-party plugins and kits for [Hatamoto](https://hatamoto.dev), one folder each.
They are built from source, signed with the Hatamoto first-party key and
listed in the plugin directory at hatamoto.dev/plugins.

There are 30 plugins. `devportal` and `stats` run on hatamoto.dev only and are not
listed; every other plugin is in the directory.

| Plugin | Folder | What it does |
|---|---|---|
| `ads-conversions` | [plugins/ads-conversions](plugins/ads-conversions) | Sends a server-side conversion to Google Ads, TikTok and LinkedIn for each form submission, with hashed contact details and the ad click ID |
| `alt-text` | [plugins/alt-text](plugins/alt-text) | Suggests alt text for images in your media library that have none, using a vision model you choose, and stages each one for your review |
| `appointments` | [plugins/appointments](plugins/appointments) | Booking from your Cal or Calendly scheduling link: an embedded scheduler section and a booking button |
| `auto-translate` | [plugins/auto-translate](plugins/auto-translate) | Machine translation with DeepL or Google Cloud Translation: stages translated copies of your pages and posts for review, linked as translations |
| `bing-webmaster` | [plugins/bing-webmaster](plugins/bing-webmaster) | Bing Webmaster Tools: submit your sitemap and see Bing clicks, impressions, search queries and crawl issues, using your Bing API key |
| `cdn-purge` | [plugins/cdn-purge](plugins/cdn-purge) | Purges changed pages from Cloudflare, Bunny or Fastly when you publish, update or delete content, so a CDN in front of your site stays fresh |
| `crm-sync` | [plugins/crm-sync](plugins/crm-sync) | Sends people who submit your forms to HubSpot, Pipedrive or Salesforce as contacts or leads, with field mapping, consent and retries |
| `devportal` | [plugins/devportal](plugins/devportal) | Developer accounts for hatamoto.dev: sign in with GitHub, accept the terms, register one signing key (runs on hatamoto.dev only) |
| `downloads` | [plugins/downloads](plugins/downloads) | Download counts for files in your media library: tracked download links, daily totals per file and a list of files for the page builder |
| `event-calendar` | [plugins/event-calendar](plugins/event-calendar) | Events calendar: month, week, day, list, photo and map views, repeating events, venues and organizers, iCal feeds and imports, public event submissions and Event structured data |
| `feed-reader` | [plugins/feed-reader](plugins/feed-reader) | Pulls items from RSS and Atom feeds you choose on a schedule and shows them as a page-builder data source, linking back to each source |
| `form-alerts` | [plugins/form-alerts](plugins/form-alerts) | Posts each form submission to Slack, Microsoft Teams, Google Chat or Telegram as it arrives, with the fields you choose and a link to it in your admin |
| `google-analytics` | [plugins/google-analytics](plugins/google-analytics) | Google Analytics: adds the Google tag for your GA4 measurement ID to every public page, with a way to leave your own visits out |
| `gtm` | [plugins/gtm](plugins/gtm) | Google Tag Manager: loads your GTM container on every public page from a container ID set in the plugin's settings |
| `hn` | [plugins/hn](plugins/hn) | A Hacker News style community: stories, votes, karma, ranked threads, flags and moderation, built on core comments |
| `link-checker` | [plugins/link-checker](plugins/link-checker) | Finds broken outbound and internal links in your published content on a schedule and lists each one with the page it is on and a link to fix it |
| `meta-capi` | [plugins/meta-capi](plugins/meta-capi) | Meta Conversions API: sends a server-side Lead event to Meta for each form submission, with hashed contact details, to back up the browser pixel |
| `pagespeed` | [plugins/pagespeed](plugins/pagespeed) | PageSpeed and Core Web Vitals: tracks field data from the Chrome UX Report and Lighthouse lab scores for your key pages, with daily trends |
| `payment-links` | [plugins/payment-links](plugins/payment-links) | Take payments and donations through Stripe Payment Links: payment buttons for your pages, an owner email per payment and a payments log |
| `polls` | [plugins/polls](plugins/polls) | Turns your forms into polls, surveys and scored quizzes, with live results, per-question breakdowns and CSV export |
| `popups` | [plugins/popups](plugins/popups) | Announcement bars and popups with page targeting, start and end dates and frequency caps, built to stay accessible and keep pages cached |
| `ratings` | [plugins/ratings](plugins/ratings) | Star ratings for posts and pages: visitors rate with a plain form, the average shows on the page and you see totals per page |
| `reviews` | [plugins/reviews](plugins/reviews) | Shows your Google and Trustpilot reviews on your pages from a copy refreshed daily, as a page-builder data source and a ready-made section |
| `searchconsole` | [plugins/searchconsole](plugins/searchconsole) | Google Search Console: connect with OAuth, submit the sitemap, inspect URLs and import daily search performance |
| `share-buttons` | [plugins/share-buttons](plugins/share-buttons) | Share links for posts and pages (LinkedIn, X, Facebook, WhatsApp, Bluesky, email) as a partial and a section, with no tracking scripts |
| `social-feeds` | [plugins/social-feeds](plugins/social-feeds) | Shows your latest Instagram posts and YouTube videos from a copy refreshed every few hours, as page-builder data sources |
| `stats` | [plugins/stats](plugins/stats) | Receives the opt-in install stats ping and shows the owner how many installs are active, on which versions and channels (runs on hatamoto.dev only) |
| `stock-photos` | [plugins/stock-photos](plugins/stock-photos) | Search Pexels and Openverse from the admin and stage the photos you pick into your media library with alt text and credit |
| `subscriber-sync` | [plugins/subscriber-sync](plugins/subscriber-sync) | Adds people who submit your forms to Mailchimp, Brevo, MailerLite or Kit, with field mapping, an optional consent field and retries |
| `uptime-monitor` | [plugins/uptime-monitor](plugins/uptime-monitor) | Checks web addresses every few minutes, emails you when one goes down or comes back, warns before an SSL certificate expires and can ping an outside heartbeat |

Each plugin is its own Go module, built for `wasip1` against the public
[SDK](https://github.com/Zen-Labs-Corp/hatamoto-sdk) (`hatamoto.dev/sdk`). The
plugin contract is
[PLUGIN_API.md](https://github.com/Zen-Labs-Corp/hatamoto/blob/main/docs/PLUGIN_API.md)
in the core repository.

## Kits

A kit sets a site up in one step: plugins, a theme, content types, pages, menus and
comment settings, reviewed and ticked by the owner.

| Kit | Folder | What it sets up |
|---|---|---|
| `hn` | [kits/hn](kits/hn) | Hacker News style community: the hn plugin, the hn-classic theme, pages, menu and comment settings |

## Layout

```
plugins/<name>/        one plugin; <name> = plugin.json "name" = route prefix /p/<name>/
template/              what scripts/new-plugin.sh copies
scripts/build-plugin.sh   vet, test and build one plugin exactly as CI and the registry do
scripts/load-check.sh     load plugin builds through the core loader at .hatamoto-core
scripts/test-load-check.sh prove load-check.sh refuses a file the loader refuses
scripts/new-plugin.sh     create plugins/<name> from the template
scripts/changed-plugins.sh  which plugins a change touches (CI)
kits/<name>/           one kit: kit.json (+ README.md)
scripts/check-kit.sh      check a kit (core's checker + parity with the plugins)
scripts/build-kit.sh      check a kit and pack it for local tests
scripts/test-check-kit.sh prove check-kit.sh refuses broken kits
scripts/changed-kits.sh   which kits a change touches (CI)
scripts/hatamoto-bin.sh   the hatamoto CLI built from the core commit in .hatamoto-core
.hatamoto-core         the core commit that checks kits and plugin loads
```

## Build

```sh
scripts/build-plugin.sh plugins/gtm      # -> plugins/gtm/build/
```

```sh
scripts/load-check.sh plugins/gtm        # after build-plugin.sh: the core loader reads build/
```

CI runs it for every plugin it builds. The first run fetches the pinned core
source and caches it; `HATAMOTO_CORE_SRC` overrides it.

Needs Go (the `toolchain` line of each `go.mod` pins it) and `jq`; `node` runs
any `*_test.js`. No `go.work`: every plugin builds alone with `GOWORK=off`, as
the registry build does.

```sh
scripts/check-kit.sh kits/hn             # core's checker, plugin parity, pack round trip
scripts/build-kit.sh kits/hn             # -> kits/hn/build/hn-<version>.kit.hmp
```

The first kit run builds the pinned core CLI with Go and caches it; `HATAMOTO_BIN`
overrides it.

## Releases

A release of one plugin is the tag `<name>/v<version>` on the commit that sets
that version in `plugins/<name>/plugin.json`. The registry lists it once
`manifests/<name>.json` in
[hatamoto-plugins](https://github.com/Zen-Labs-Corp/hatamoto-plugins) pins that
commit with `"subdir": "plugins/<name>"`.

A kit is tagged `kit-<name>/v<version>` and listed in the registry under
`manifests/kits/<name>.json` with `"subdir": "kits/<name>"`. It is never mirrored to
a per-name repository.

## History

`gtm`, `searchconsole` and `stats` were imported with their full history from
their former repositories (`hatamoto-plugin-<name>`, now archived).

## License

Apache-2.0, see [LICENSE](LICENSE). Contributions: see
[CONTRIBUTING.md](CONTRIBUTING.md).
