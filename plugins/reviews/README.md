# reviews: Google and Trustpilot reviews as a plugin

Shows your Google and Trustpilot reviews on your pages from a copy refreshed
daily, as a page-builder data source and a ready-made section. It replaces what
WordPress plugins such as "Widgets for Google Reviews", "Widget Google Reviews"
and "Trustpilot" do: the plugin fetches your reviews once a day, keeps a copy,
and your pages read the copy, so a visit never waits on Google or Trustpilot.

## Use

1. **Google.** In Google Cloud, enable *Places API (New)* for a project, create an
   API key and look up the place ID of your business on Google Maps.
2. **Trustpilot** (optional). Get an API key and the business unit ID of your
   Trustpilot profile.
3. Install the plugin (Admin > Settings > Plugins) and grant its permissions.
4. In the plugin's settings fill in the keys (write-only: they are never shown
   again) and the IDs. A source is on only when both its key and its ID are set.
   Set **min_rating** (default 4: show reviews rated 4 and above) and
   **max_items** (default 6, at most 48).
5. Open the **Reviews** page and press *Refresh now*, or wait for the daily refresh.

Show the reviews in one of three ways:

- **Section.** Add a section of type `reviews`: cards with stars, text, author
  (linked to the author's profile where the source gives one, with the author's
  photo for Google), date and source, with the "Reviews from Google Maps" (linked
  to the place) and "Reviews from Trustpilot" attribution. It
  renders nothing while no review is cached.
- **Page builder.** A query loop with the source **Latest reviews**
  (`ext.reviews.latest`). Each item has `title` and `author` (the author), `excerpt`
  (the text), `url`, `author_url`, `image` (author photo, Google only),
  `published_at`, `category` (Google or Trustpilot) and the attributes `rating`,
  `date`, `source` and `url`.
- **Theme.** Template functions, all returning plain text:
  `plugin_reviews_summary` (for example `4.8 from 132 Google reviews`; with both
  sources `4.8 from 132 Google reviews, 4.6 from 210 on Trustpilot`; an argument
  `"google"` or `"trustpilot"` picks one; empty when nothing is cached),
  `plugin_reviews_count` (shown reviews, optional source argument),
  `plugin_reviews_field N "key"` (the N-th shown review, counting from 0; keys
  `author`, `author_url`, `photo`, `text`, `date`, `rating`, `stars`, `source`,
  `source_label`, `url`) and `plugin_reviews_profile "google"` (the profile or
  Maps address of a source).

With no keys set, or when a source is down, everything stays empty: the section
renders nothing, the loop shows its empty state, the functions return empty text.

## Permissions

- `settings`: reads the API keys, the IDs, `min_rating` and `max_items`.
- `http.fetch`: calls the Google Places API and the Trustpilot API. The allowed
  hosts are exactly `places.googleapis.com` and `api.trustpilot.com`.
- `db`: keeps the cached reviews and the summary of each source.
- `builder.data`: answers the `ext.reviews.latest` page-builder source.
- `log`: records refresh failures. Keys never appear in a log line.

Nothing else: no key-value store, no scripts on your pages.

## Data kept

Two tables in the plugin's own schema:

- `reviews`: per review the source, its ID, the author name, the author's profile
  and photo address where given, the rating, the text, the date and a link.
- `sources`: per source the overall rating, the review count, the profile address,
  the time of the last attempt and of the last successful refresh, and the last
  error message.

API keys are never stored in these tables and never appear in an error message, a
log line, a route answer or the admin page. A refresh replaces a source's rows. The
rows of a source are deleted when its last successful refresh is older than 30
days (also when the source is no longer set up); the hourly job checks this.

Check the terms of both services before you use a cached copy: Google Maps Platform
limits how long Places content may be stored (the 30-day deletion is a compromise,
not a clearance), and Trustpilot's API terms govern how its reviews may be stored
and shown. Show the attribution the section prints.

## Build

From the repository root: `scripts/build-plugin.sh plugins/reviews`.

## Limits

- Google returns at most 5 reviews per place (Places API limit), chosen by Google;
  the plugin cannot page or sort them.
- Trustpilot needs an API key; the plugin reads the newest 20 reviews.
- The refresh job runs hourly and fetches a source when its last attempt is 23
  hours old or more, so each source is fetched about once a day even on a host that
  restarts. *Refresh now* fetches both at once. One refresh has a 10 second budget;
  a source that does not fit waits for the next run.
- The section shows up to 12 cards; the page-builder source up to 48 items.
- Pages are cached by the host (`HATAMOTO_PAGE_CACHE_TTL`, 5 minutes by default), so
  a new review can take that long to show up after a refresh.
- No review schema markup (structured data) is written: search engines ignore
  self-served review markup for local businesses.

## License

Apache-2.0, see [LICENSE](LICENSE).
