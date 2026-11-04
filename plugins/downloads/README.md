# downloads

Download counts for files in your media library: tracked download links, daily totals per file and a list of files for the page builder.

It does the everyday job of a download manager or download monitor plugin: link to a file through the plugin, and every click is counted. The visitor is sent straight on to the file, with no form, sign-in or wait. Nothing about the visitor is stored.

## Use

1. Install the plugin from Admin > Settings > Plugins and grant the permissions it asks for.
2. Link to a file with `/p/downloads/f/<file id>`. The visitor is redirected to the file; the click is counted for the current UTC day. An unknown or malformed id answers `404`.
3. In the page builder, add a loop over the data source **Downloadable files** (`ext.downloads.files`). Each item has the title (file name), the tracked link as its URL and the attributes `count` (all-time downloads), `size` (bytes), `size_label` (for example `2.4 MB`) and `mime_type`.
4. In a theme, `{{ plugin_downloads_count "<file id>" }}` prints the all-time count as plain text (`0` for an unknown id).
5. Setting **Count bots** (off by default): when on, crawlers, link previewers and scripts, recognised by their User-Agent, are counted too. They always get the redirect either way.
6. The plugin's **Settings** tab shows the owner a table of the library's files (50 per page) with total, the last 30 days, a 30-day trend and a **Copy link** button, plus site-wide totals.

## Permissions

- `media.read`: look up a file's path to redirect to it, and list the files for the builder source and the admin page.
- `db`: store the download counts in the plugin's own tables.
- `builder.data`: answer the **Downloadable files** page-builder source.
- `settings`: read the **Count bots** setting.

No network access, no site scripts, no key-value storage.

## Data kept

- One row per file and UTC day with the number of downloads, kept for 13 months. After that a background job folds each month into one row per file and month, which is kept until the plugin is uninstalled.
- No IP address, User-Agent, referrer or cookie is stored or logged. The User-Agent is only looked at, in memory, to tell bots from browsers.
- Hatamoto itself logs requests as it does for every page; the plugin cannot change that.

## Build

From the repository root: `scripts/build-plugin.sh plugins/downloads`.

## Limits

- Days are UTC days, so a day's count rolls over at 00:00 UTC.
- No per-visitor de-duplication: the plugin gets no address or cookie, so a repeated click is counted again.
- Bots are recognised by User-Agent only, with a simple word list; an unusual script can be counted and an odd browser can be skipped. A `HEAD` request is never counted.
- Files are redirected to their same-site path (for example `/media/...`). If the media library serves files from an absolute or CDN URL, the tracked link answers `404` instead of redirecting and nothing is counted.
- Public routes are rate limited by Hatamoto (about 10 requests per second per client, burst 30); a burst of downloads from one address can be refused before the plugin sees it.
- The builder source and the theme function are cached with the page, so their numbers are as fresh as the page cache.
- Versioned files and gated downloads are not supported.

## License

Apache-2.0.
