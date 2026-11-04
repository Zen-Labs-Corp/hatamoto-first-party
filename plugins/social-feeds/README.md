# social-feeds

Shows your latest Instagram posts and YouTube videos on your site, from a copy
that is refreshed every few hours, as page-builder data sources. It does what
WordPress plugins such as Smash Balloon Instagram Feed, Feeds for YouTube and
Spotlight do, without calling Instagram or YouTube while a visitor loads a page.

## Use

Instagram:

1. In the Meta developer dashboard, create an app with Instagram API with
   Instagram Login. The Instagram account must be a business or creator account.
2. Add `https://<your site>/p/social-feeds/oauth/callback` as the redirect
   address of that app. The plugin's admin page shows the exact address.
3. Install the plugin and grant the permissions it asks for.
4. In the plugin's **Settings**, fill in the Instagram app ID, the Instagram app
   secret and the site address (for example `https://example.com`).
5. Open the plugin's admin page and choose **Connect Instagram**. The page shows
   an address: open it in a new tab, approve access, and you land back on the
   admin page, now connected.

YouTube:

1. Create a YouTube Data API v3 key in the Google Cloud console.
2. In **Settings**, fill in the API key and the channel ID (it starts with `UC`).

On a page, add a query loop and pick the source **Instagram posts** or
**YouTube videos**. Inside the loop use `loop.item.title`, `loop.item.image`,
`loop.item.url` and `loop.item.published_at`. Each item also carries the
attributes `id`, `type` and `source`. A source that is not set up gives an
empty loop, never an error.

**Refresh now** on the admin page fetches both sources at once. The setting
**Items** (1 to 48, default 9) caps how many items a loop receives.

## Permissions

- `settings`: reads the app ID and secret, the API key, the channel ID, the
  item count and the site address.
- `http.fetch`: talks to Instagram and YouTube. The allowed hosts are
  `api.instagram.com` (code exchange), `graph.instagram.com` (posts and token
  renewal) and `www.googleapis.com` (YouTube Data API). Nothing else.
- `db`: keeps the copy of the latest items and the outcome of the last refresh.
- `kv`: keeps the one-time state of a connection attempt and the Instagram
  access token.
- `builder.data`: answers the two page-builder sources from the copy.
- `log`: records refresh and renewal failures, without secrets.

The plugin asks for no other permission and adds no script to your pages.

## Data kept

- The latest 50 items of each source: id, caption or title, address, thumbnail
  address, date and type. A refresh replaces them.
- Per source: the time of the last refresh and the last error text.
- The Instagram access token, the account name and the token's expiry, and the
  state of a connection attempt for 10 minutes, in the plugin's key-value store.
  The token is never written to the database.
- **Disconnect** deletes the token and the Instagram items. Secrets are never
  logged, stored in an error text, returned by a route or shown on the admin
  page.

## Build

From the repository root: `scripts/build-plugin.sh plugins/social-feeds`.

## Limits

- Instagram needs a business or creator account. Personal accounts are not
  supported.
- The copy is refreshed every 3 hours. The first run happens 3 hours after the
  plugin starts, so use **Refresh now** after connecting.
- The Instagram token lasts about 60 days. A daily check renews it once it is a
  day old or close to expiry. If a renewal fails, the old token stays and the
  admin page shows the error.
- Pages are cached for about 5 minutes, so a new post can take that long to
  show after a refresh.
- At most 50 items per source are kept and 48 per loop.
- The admin preview is text only, because the plugin admin page blocks remote
  images. Thumbnails show on your public site.
- Not affiliated with Instagram, Meta or YouTube.

## License

Apache-2.0.
