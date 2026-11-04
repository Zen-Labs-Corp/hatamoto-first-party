# cdn-purge

Purges changed pages from Cloudflare, Bunny or Fastly when you publish, update or delete content, so a CDN in front of your site stays fresh. It does the job of the common WordPress cache-purge plugins (Cloudflare, Nginx Helper, Varnish HTTP Purge) for a Hatamoto site.

## Use

1. Install the plugin from Admin > Settings > Plugins and grant the permissions it asks for.
2. Open its **Settings** tab and fill in:
   - **Provider**: Cloudflare, Bunny or Fastly.
   - The id for that provider: **Cloudflare zone ID**, **Bunny pull zone ID** or **Fastly service ID**.
   - **API token** (write-only: it is never shown again). Cloudflare: an API token that may purge the cache of the zone. Bunny: an account API key. Fastly: an API token that may purge.
   - **Also purge these paths**: one path per line (up to 20), purged together with the changed page. Default: `/`, `/blog`, `/sitemap.xml`, `/feed.xml`.
   - **Purge everything on every change**: drop the whole CDN cache on each change instead of single URLs. Off by default.
   - **Site URL**: an origin such as `https://example.com`. Used only when the host gives a page path without an origin (when the site's public address is not set).
3. Open the **CDN purge** page. **Test connection** makes one read-only call to the provider and shows whether the token is accepted.
4. Publish, update or delete a page: the page's URL and the extra paths are purged, and a row appears under **Recent purges**.

**Purge everything** on the page drops the whole cache of the zone or service after a confirmation in the page.

What each change purges:

- **Published**: the page URL and the extra paths.
- **Updated**: while published, the current URL (and the previous URL when the page moved) and the extra paths. When a page stops being published, its last public URL and the extra paths. When no public URL was remembered but the page was published in the past (before the plugin was installed), the page's current URL and the extra paths are purged once. Saving a draft that was never published, or is scheduled, purges nothing, and neither do further saves after an unpublish purge.
- **Deleted**: the last public URL the plugin remembered, and the extra paths. A page published before the plugin was installed has no remembered URL, so only the extra paths are purged.

## Permissions

- `entities.read`: reads the changed entity by id to learn its public path and URL (the event itself carries none).
- `settings`: reads provider, id, API token, extra paths and Site URL.
- `http.fetch`: calls the provider's purge API. `allowed_hosts` are exactly `api.cloudflare.com`, `api.bunny.net` and `api.fastly.com`.
- `kv`: remembers the last public URL of each entity, so a deleted, moved or unpublished page can still be purged.
- `db`: keeps the purge log.
- `log`: one log line per event with a failed purge, and one when no origin is known. The token is never logged.

## Data kept

- `kv` key `url:<entity id>`: the last public absolute URL. Deleted when the entity is deleted. After an unpublish purge it is replaced by an empty marker that expires after 30 days, so later draft saves do not purge again; the next publish stores the URL again.
- Table `purge_log`: time, trigger, provider, URLs, HTTP status and a short error text of each purge request. Rows older than 14 days are deleted on each insert.
- The API token lives in the plugin settings only. It is sent as a request header, and never written to the log, the database, `kv` or a route answer (the admin page only learns whether a token is set).

## Build

From the repository root: `scripts/build-plugin.sh plugins/cdn-purge`.

## Limits

- One attempt per request: no retries and no queue. Each failed request is shown in the purge log, and an event writes one server log line however many requests failed. When Bunny or Fastly answers 401 or 403, the remaining URLs of the event are not tried.
- No coalescing: each event purges on its own, so several events for the same page purge it several times. URLs within one event are de-duplicated.
- Cloudflare gets one request per 30 URLs. Bunny and Fastly purge one URL per request, so an event costs up to about 22 requests (the page, its old URL and 20 extra paths), and each request is one row in the purge log.
- The plugin spends at most 6 seconds of the host's 10 second action timeout on provider calls, with 3 seconds per call; the timeout also covers waiting for a free plugin instance. A bulk publish of many pages at once can therefore leave later events waiting for an instance and purge nothing for them. When time runs out, the remaining URLs are logged as one "skipped" row and not purged; use **Purge everything** or save again.
- When the entity has no path although the origin is known, only the extra paths are purged and a warning is logged.
- Without an origin (the entity's URL is a bare path and Site URL is empty) nothing is purged and "set Site URL" is logged.
- Imports and other bulk writes fire no entity events, so they are not purged; use **Purge everything** afterwards.
- Hatamoto's own page cache is not touched; core handles that.
- Provider endpoints follow the providers' public API documentation from memory and were not exercised against live accounts:
  - Cloudflare: `POST /client/v4/zones/{zone}/purge_cache` with `{"files":[...]}` (at most 30) or `{"purge_everything":true}`, `Authorization: Bearer`. **Test connection** uses `GET /client/v4/user/tokens/verify`, which accepts user API tokens only; an account-owned token may be reported as not accepted while purging works.
  - Bunny: `POST /purge?url=` and `POST /pullzone/{id}/purgeCache`, header `AccessKey` (the account API key). **Test connection** reads the pull zone.
  - Fastly: `POST /purge/<host+path>` and `POST /service/{id}/purge_all`, header `Fastly-Key`. **Test connection** reads the service. A single-URL purge works for hosts the service serves.

## License

Apache-2.0.
