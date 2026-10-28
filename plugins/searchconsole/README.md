# searchconsole: Google Search Console as a plugin

Connect your site to Google Search Console from the admin: pick the property,
submit the sitemap, inspect URLs and keep a daily copy of search performance
(queries and pages) in the plugin's own tables.

## Set up

1. In Google Cloud, create an OAuth client of type **Web application** and enable
   the *Google Search Console API*. Add the redirect address
   `https://<your site>/p/searchconsole/oauth/callback`.
2. Install the plugin (Admin > Settings > Plugins) and grant its permissions.
3. In the plugin's settings fill in **client ID**, **client secret** (write-only:
   it is never shown again), the **public address** of the site and, optionally,
   an **encryption key**.
4. Open the **Search Console** page and press *Connect to Google*; open the
   address it shows (`/api/v1/p/searchconsole/oauth/start`) in a new tab while
   signed in, approve access, and you land back on the page. Then pick the
   property.

## How it works

- **OAuth** goes through the plugin's own routes: `GET /api/v1/p/searchconsole/oauth/start`
  (owner only) creates a random `state` and a PKCE verifier (S256), keeps them in
  `kv` for 10 minutes and returns the Google address (JSON, or a redirect page when
  opened in a browser); `GET /p/searchconsole/oauth/callback` (public) accepts only a
  live one-time `state`, exchanges the code with the verifier and the client secret,
  and stores the tokens.
- **Tokens** are encrypted with AES-256-GCM in the plugin's `connection` table. The
  key is derived from the client secret and the optional encryption key (both
  write-only settings). Those settings are stored in Hatamoto's own database, so
  this protects the plugin's schema on its own (a table-only export, a leaked
  plugin role), **not** a full database dump or backup: that holds the ciphertext
  and the key material together. Treat database backups as secret. Changing either
  setting means connecting again. Access tokens are refreshed on demand (and once
  after a 401).
- **Scope**: the full `webmasters` scope, because submitting a sitemap needs write
  access; the plugin only reads sites and performance, submits the sitemap and
  inspects URLs.
- **Features** (routes, all owner only): `GET /status`, `GET /sites`,
  `POST /site {site_url}`, `POST /sitemap {sitemap_url?}` (default: the public
  address plus `/sitemap.xml`), `POST /inspect {url}`, `GET /performance?days=28`,
  `POST /import`, `POST /disconnect`.
- **Daily import**: the `import` cron job (hourly) stores every day of the last
  `import_days` (default 7) that is not stored yet, ending two days ago, because
  Google's final data lags. Rows come in pages of 1,000 (compact JSON, so a page is
  far below the host's 1 MiB response cap; an answer that still hits the cap is an
  error, not half a document). One call stays within the plugin's 10 s limit and
  checks the time between pages: a large backlog, or one big day, takes a few runs,
  and the next run resumes at the page where the last one stopped
  (`import_progress`). Tables: `perf_daily` (day, query, page, clicks,
  impressions, ctr, position), `imported_days`, `import_runs`,
  `sitemap_submissions`, `inspections`.
- **Network**: `http.fetch` only, with exactly the hosts it uses as `allowed_hosts`:
  `oauth2.googleapis.com`, `www.googleapis.com`, `searchconsole.googleapis.com`,
  `accounts.google.com`.

## Test builds and the base URLs

The Google endpoints are constants in `google.go`. The manifest `config` keys
`endpoint_auth`, `endpoint_token`, `endpoint_api` and `endpoint_inspect` override
them for tests and for a proxy you build into your own package. They are **not**
settings: an owner-editable endpoint would let anyone with a settings session send
the client secret and the refresh token to an address of their choice. They do not
widen what the plugin can reach either, because `allowed_hosts` (exact hosts, not
`*.googleapis.com`) still decides. The plugin's own tests do not call Google. To exercise the flow, point these
keys at a stand-in server that answers with recorded responses, and use a copy of
the plugin whose manifest also allows that server's address.

## Build, test, install

Needs Go 1.27.3 and the public SDK (`hatamoto.dev/sdk`, see
https://github.com/Zen-Labs-Corp/hatamoto-sdk).

```sh
go vet ./... && go test ./...    # token encryption, PKCE
rm -rf build && mkdir -p build
CGO_ENABLED=0 GOOS=wasip1 GOARCH=wasm go build -buildmode=c-shared -o build/plugin.wasm .
cp plugin.json build/ && cp -r migrations ui build/
```

`build/` is the plugin package content. Install a released version from the
Hatamoto plugin registry (Admin > Settings > Plugins). To install your own build,
pack it and upload the file (Admin > Settings > Plugins > Upload package):

```sh
hatamoto plugin pack build --out searchconsole.hmp                    # unsigned
hatamoto plugin pack build --key mykey.key --out searchconsole.hmp    # signed with your own key
```

An unsigned package is refused unless the owner switches on *Allow unsigned
packages* in the same settings page; a signed one needs its key listed in
`HATAMOTO_TRUSTED_KEYS` (`hatamoto keys generate --name mykey` creates the pair).
See https://hatamoto.dev/docs/plugins/packaging-signing for packaging and signing.

## Limits

A sandboxed admin page cannot open windows, so the OAuth start address is shown
for you to open yourself. One property per site. Data older than the import window
is not back-filled.
