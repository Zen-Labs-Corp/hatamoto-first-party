# stock-photos

Search Pexels and Openverse from the admin and stage the photos you pick into your media library with alt text and credit.

It covers what WordPress sites use Instant Images for: find a free photo, bring it into the library with its alt text and credit line, without leaving the admin.

## Use

1. Install the plugin from Admin > Settings > Plugins.
2. Grant the permissions it asks for.
3. To use Pexels, open its **Settings** tab and add your Pexels API key. Openverse needs no key.
4. Open the **Runs** tab, choose **Import stock photos** and fill in the page: the query, the provider, how many photos (1-30) and the orientation.
5. Tick the hosts the run may reach (see below), then press **Start**.
6. Review the changeset: each photo is listed with its alt text and credit. **Publish** it or **Discard** it. Nothing is live until the changeset is published.

Running the same query again updates the same media instead of adding copies; the photos are downloaded again.

## Permissions

- `settings`: reads the Pexels API key you saved in the Settings tab.
- `http.fetch`: calls the search APIs and downloads the images, only from the hosts you tick when you start a run.

## Data kept

Nothing: the plugin has no database tables and no stored keys of its own. Results live in the run buffer until the changeset is published or discarded. The Pexels key is stored by the core as a write-only setting.

## Hosts you tick

The start form offers only the hosts the plugin declares; you cannot type others. Each one is there for a reason:

- `api.pexels.com`: the Pexels search API. It is always ticked in the form.
- `images.pexels.com`: where Pexels photos are downloaded from.
- `api.openverse.org`: the Openverse search API.
- `live.staticflickr.com`: where Openverse photos from Flickr are downloaded from.
- `upload.wikimedia.org`: where Openverse photos from Wikimedia Commons are downloaded from.

For Pexels tick `api.pexels.com` and `images.pexels.com`. For Openverse tick `api.openverse.org` plus the image hosts you want: `live.staticflickr.com` for Flickr photos, `upload.wikimedia.org` for Wikimedia Commons photos. Openverse searches are limited to those two sources, the ones whose image hosts the plugin declares. A photo from a declared host you did not tick is skipped and named in the run summary; if no photo could be used, the summary lists the hosts to tick.

## Licences

- Pexels photos come under the Pexels License. The credit line reads `Photo by <name> on Pexels (Pexels License) <photo page>`.
- Openverse results are limited to licences that allow commercial use and modification: CC0, Public Domain Mark, CC BY and CC BY-SA. Licences with NC or ND are never staged. The credit line reads `<title> by <author>, <licence>, <source page>`.
- The credit line and a short caption are saved with each media item.
- Unsplash is not supported.

## Limits

- 1 to 30 photos per run (12 by default).
- Pexels allows about 200 requests an hour per key; Openverse caps anonymous use. The run stops with a message when a provider rate limits it.
- Up to 3 images are downloaded per run step, and a search is read at most 5 pages deep, so a query with many unusable results can stage fewer photos than asked for.
- Needs a Hatamoto host with plugin SDK 0.18.

## Build

From the repository root: `scripts/build-plugin.sh plugins/stock-photos`.

## License

Apache-2.0.
