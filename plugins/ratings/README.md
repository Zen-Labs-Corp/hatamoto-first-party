# ratings

Star ratings for posts and pages: visitors rate from 1 to 5 with a plain form, the average shows on the page, and you see totals per page in the admin. No JavaScript is involved. It does what plugins such as kk Star Ratings and WP ULike do on WordPress.

## Use

1. Install the plugin from Admin > Settings > Plugins and grant the permissions it asks for.
2. Open its **Settings** tab:
   - **Entity types** (`schemas`): the entity types that can be rated. The default is `post`.
   - **Allow changing a vote** (`allow_change`): when on, a visitor with a voter cookie can change their vote. When off, the second vote is ignored.
3. In the theme template of those pages add `{{ template "plugin/ratings/form" . }}`. Call it with `.`: the partial reads `.EntityID`, `.SchemaSlug` and `.Path` of the page. It shows five submit buttons (1 to 5 stars) and, below them, the average and the number of ratings. It renders nothing without an entity (for example on a listing) or for an entity type that is not enabled.
4. Admin > Plugins > Star ratings lists the rated pages with the average, the number of ratings and how many votes each star count got (5 down to 1), most rated first. The table shows at most the 200 most rated pages.

The summary text, for example `4.3 out of 5 (27 ratings)`, can also be placed alone with `{{ plugin_ratings_summary .EntityID }}`. It is empty while there are no ratings.

## Permissions

- `db`: store the votes per entity and voter, and read averages and distributions, in the plugin's own schema.
- `settings`: read `schemas` and `allow_change`. Templates cannot read settings, so the plugin answers `plugin_ratings_enabled` for the partial.
- Cookie `voter` (browser name `hmp_ratings_voter`): remembers which visitor voted, so one visitor gives one vote.
- Request header `sec-fetch-site`: a vote whose browser says it came from another site is ignored.

The plugin makes no outbound requests, adds no script to your site and does not read your entities.

## Data kept

- One row per entity and voter: the entity id, the voter key, the stars (1 to 5) and the created and updated times.
- The first page path posted for each entity, for the admin table.

The voter key is a random 128-bit value kept in the `hmp_ratings_voter` cookie (HttpOnly, one year from the first vote, not renewed). It is never derived from an IP address or a header, and no IP address, header or User-Agent is stored. The rows of an entity are deleted when the entity is deleted. Uninstalling the plugin drops its schema.

## Build

From the repository root: `scripts/build-plugin.sh plugins/ratings`.

## Limits

- Needs Hatamoto 1.0.8 or later. Older cores refuse a plugin that asks for SDK `^0.18`.
- The average reaches cached pages within the page cache TTL (`HATAMOTO_PAGE_CACHE_TTL`, default 5 minutes), or earlier when a settings save or an entity change clears the cache. A vote does not clear it.
- The plugin cannot check that an entity id exists (it has no `entities.read`). A forged id adds rows that stay until you clean them by hand or uninstall.
- The entity type and the page path come from the form and can be forged. They are display data only: the admin page shows the path as plain text.
- A client that sends no cookie can add votes. Core rate limits public routes per client; the plugin stores no IP address, by design.
- A cross-site post is ignored only when the browser sends `sec-fetch-site: cross-site`.
- The admin table lists at most 200 pages, the most rated first; further rated pages are stored but not shown.
- The voter cookie expires one year after the first vote and is not renewed. After that the browser counts as a new visitor and can vote again.
- A second vote is ignored unless **Allow changing a vote** is on.
- Whole stars only: no half stars, no review text, and no schema.org AggregateRating markup.

## License

Apache-2.0.
