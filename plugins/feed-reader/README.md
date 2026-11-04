# feed-reader

Pulls items from RSS and Atom feeds you choose on a schedule and shows them as a page-builder data source, linking back to each source.

It replaces WordPress plugins such as WP RSS Aggregator and Feedzy: you list the feeds, the plugin keeps the latest items, and a page-builder loop shows them with a link back to the original article.

## Use

1. Install the plugin from Admin > Settings > Plugins and grant the permissions it asks for (see Permissions).
2. Open its **Settings** tab and add feeds: a name, the feed URL (http or https), optionally the number of items to keep (1 to 200, default 50) and a group (1 to 7).
3. Wait up to 30 minutes, or open the **Feeds** page and press **Fetch now**. The page lists every feed with its last fetch time, status and item count, and the latest items.
4. In the page builder, add a query loop and pick the source **Feed items** (every feed) or **Feed group 1** to **Feed group 7** (the feeds whose Group setting is that number).
5. Inside the loop use `loop.item.title`, `loop.item.url` (the original article), `loop.item.excerpt`, `loop.item.published_at`, `loop.item.category` (the feed name) and `loop.item.attributes.source` / `loop.item.attributes.source_url` (the feed name and its site).

Items are newest first. With no feeds, or before the first fetch, the loop shows its empty state, never an error.

## Permissions

- `settings`: reads the list of feeds you entered.
- `http.fetch`: downloads the feeds.
- `http.fetch.any`: feeds can live on any public host you type in, so the plugin is allowed to reach any host (`allowed_hosts` is `*`). The install screen warns about this. The host still refuses private and internal addresses, and the plugin only fetches the URLs in its own settings.
- `db`: keeps the fetched items and the per-feed state in the plugin's own tables.
- `builder.data`: answers the page-builder sources.
- `log`: records a short, fixed message when a feed fails.

## Data kept

- Per feed: name, URL, group, time and result of the last attempt, the ETag and Last-Modified values for conditional requests, the site address and the item count. The result is a short fixed phrase (for example `ok`, `not modified`, `HTTP 404`, `invalid feed`); response bodies and raw error text are never stored or logged.
- Per item: id, title, link, a text-only excerpt, publication date and fetch time. No HTML, images or full content.
- Retention: the newest items of each feed up to its Max items setting (never more than 200), and nothing older than 90 days.
- A feed removed from Settings loses its state and items at the next run.

## Build

From the repository root: `scripts/build-plugin.sh plugins/feed-reader`.

## Limits

- 20 feeds, 1 MiB per feed response, 200 items per feed, items kept for 90 days.
- Each feed is refreshed 30 minutes after its last attempt; the job runs every 5 minutes and stops starting new fetches after about 7 seconds, so with many slow feeds some wait for the next run.
- Groups 1 to 7; a builder query returns at most 48 items.
- Only ports 80 and 443 are reachable.
- RSS 2.0, RSS 1.0 and Atom, in UTF-8, US-ASCII, ISO-8859-1 or windows-1252. No JSON Feed, no OPML import, no feeds that need a login.
- Excerpts are plain text of about 280 characters; links must be absolute http or https.

## License

Apache-2.0.
