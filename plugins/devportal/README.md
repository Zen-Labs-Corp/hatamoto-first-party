# devportal

Developer accounts for hatamoto.dev: sign in with GitHub, accept the terms,
create a free developer account and register one ed25519 signing key. A
developer can then submit a plugin or theme by its release URL. The owner
reviews each submission, and approved ones are listed in a signed developer
index that a core site reads as its developer registry. The owner can also list
and suspend accounts, revoke a listed version and revoke a developer key.

This plugin is **hatamoto.dev-only**: it is not in the public plugin registry and
is installed from a signed `.hmp` on the site that runs the developer portal.

## How it works

1. A visitor opens the themed CMS page `/developers`. The plugin's `render.page`
   filter replaces the page's placeholder block with the portal section for the
   visitor's state: signed out, signup, dashboard or suspended.
2. "Sign in with GitHub" is a plain link to GitHub's authorize URL. Its `state`
   equals the cookie `st` set by the filter.
3. GitHub returns to the public route `GET /p/devportal/auth/callback`. The
   plugin checks `state` against the cookie, marks it used (single use), trades
   the code for a token, reads the profile and the verified emails, drops the
   token and answers `303` to `/developers`.
4. A first sign-in lands on the signup form. The account row is created only
   when the terms box is ticked. Later sign-ins find the account by GitHub id,
   never by login, and refresh the login.
5. The dashboard shows the account's one key (its 16 hex character key id, like
   `hatamoto keys generate` prints) or a form to paste a public key.

The forms are real same-origin forms that `POST` to public routes under
`/p/devportal/`. Each route requires an `Origin` equal to the `public_url`
setting and a hidden CSRF token that matches the session, and answers
`303 Location: /developers`.

## Submissions, review and the index

- **Submit.** A signed-in developer with an active account and a key posts a
  release URL (and a source URL) from the dashboard to `POST /submit`. The
  plugin has core inspect the package (`packages.inspect`: manifest, size,
  hash, signature) and runs its checks. The name must be free or already
  owned by the same developer, the package must be signed by the developer's
  registered key, and the version must be newer than a listed one. A package
  that fails a check is refused and the developer sees why. One that passes
  waits for review, and the owner gets a mail at `review_email`.
- **Review.** The owner opens Admin > devportal > Review. **Waiting** shows
  each submission with its permissions, hosts, size and checks, with
  **Approve** and **Reject** (a reason is required and is mailed to the
  developer). **Listed** shows live listings with **Revoke version** and
  **Revoke developer key**. **Revoked** shows the revocation list. Approving
  lists the version and publishes the index.
- **Index.** `GET /p/devportal/registry.json` serves the signed developer
  index, exactly as stored. It lists every active listing of an active account
  plus the revocations. Point a core site at it with
  `HATAMOTO_DEVELOPER_REGISTRY_URL` set to
  `<public_url>/p/devportal/registry.json`.
- **Recheck.** Every hour the `recheck` cron inspects the listed packages
  again, two per run. A file whose hash or signer changed is dropped from the
  index and the developer is told. A failed inspection only counts, so a
  passing network error never delists anything.
- **Re-sign.** The `resign` cron signs the index again every 24 hours so it
  never expires. The `mail` cron sends queued notices every minute.

## The `/developers` page

Create a themed page at `/developers` with **one block** whose `anchor` is
`devportal` (a short text block is fine; it is what visitors see if the plugin
is off). The plugin replaces that block, or the page's first block when none has
the anchor. Nothing else on the page is touched, and every other path is
returned unchanged without a host call. The section type is `devportal`
(`sections/devportal.html`).

Cost: while a plugin handles `render.page`, the host serves every public page
`private, no-store` and does not page-cache it. The hook timeout is 100 ms and
the filter fails open: on any error the placeholder block stays.

Ship checks:

- The portal forms need public pages that are not sandboxed. When any community
  plugin's script, island or script section is live on the site (or a QA build
  sets `HATAMOTO_QA_SANDBOX_ALL=1`), core answers public pages with
  `Content-Security-Policy: sandbox allow-scripts allow-forms` and no
  `allow-same-origin`. Forms then post with `Origin: null` and without the
  `sid` cookie, so every portal POST is refused with a plain `403`. Sign-in
  still works, account creation, key and sign-out do not.
- `curl` `/developers` and expect the "Publish plugins and themes" heading, not
  only a `200`: a page that only exists in the page builder (no section model)
  never runs the filter, and a page without an `anchor: devportal` block loses
  its first block.
- The signup and key forms use `required` fields. A click on the button with
  the fields empty is stopped by the browser and sends no request; test with a
  filled form (a real click on the button then posts to `/p/devportal/...`).

## Settings

| Key | Type | Default | What |
|---|---|---|---|
| `github_client_id` | string | empty | OAuth App client ID |
| `github_client_secret` | string, write-only | empty | OAuth App client secret; never shown or logged |
| `listing_key` | string, write-only | empty | The key file contents that sign the developer index; never shown or logged |
| `review_email` | string | empty | Contact shown on the terms page; also receives new submission notices |
| `public_url` | string | `https://hatamoto.dev` | Site origin without a trailing slash; forms must come from it and GitHub returns to it |
| `terms_markdown` | string | a short draft | Developer terms: paragraphs separated by blank lines, `# ` lines are headings. Shown as plain text: every character is escaped, no HTML, no links |

GitHub OAuth App: callback URL `<public_url>/p/devportal/auth/callback`; the
plugin asks for the scopes `read:user user:email`.

## Permissions

| Permission | Why |
|---|---|
| `db` | accounts, keys and sessions |
| `kv` | single-use marker for the OAuth `state` (10 minutes) |
| `http.fetch` | GitHub token exchange, `/user`, `/user/emails` (hosts `github.com`, `api.github.com`) |
| `settings` | reads the settings above |
| `log` | failure stages only, never tokens, codes or secrets |
| `packages.inspect` | inspect a submitted release before it is listed and when it is rechecked |
| `mail.send` | notices to the reviewer and to developers |

Allowed hosts: `github.com`, `api.github.com`, and
`release-assets.githubusercontent.com`, `objects.githubusercontent.com` for
release downloads that GitHub redirects. No `site.scripts`.

## Routes

| Route | Access | Purpose |
|---|---|---|
| `GET /auth/callback` | public | GitHub return |
| `POST /account` | public | create the account (terms ticked) |
| `POST /key` | public | register the key |
| `POST /logout` | public | end the session |
| `POST /submit` | public | submit a plugin or theme release |
| `GET /terms` | public | the terms, read only |
| `GET /registry.json` | public | the signed developer index |
| `GET /admin/accounts` | owner | list and search, 50 per page |
| `POST /admin/accounts/{id}/suspend` | owner | suspend and end the sessions |
| `POST /admin/accounts/{id}/unsuspend` | owner | lift the suspension |
| `GET /admin/submissions` | owner | waiting, listed and revoked lists |
| `POST /admin/submissions/{id}/approve` | owner | list a waiting submission and publish |
| `POST /admin/submissions/{id}/reject` | owner | turn it down with a reason |
| `POST /admin/listings/{id}/revoke` | owner | revoke one listed version |
| `POST /admin/accounts/{id}/revoke-key` | owner | revoke the developer key and unlist every package of it |

Public routes are limited by the host to 10 requests per second per address.

## Data

- `accounts`: GitHub id, login, display name, one verified email, plan
  (`free`), status (`active` or `suspended`), the time the terms were accepted.
- `keys`: at most one per account; the public key is unique across accounts.
- `sessions`: the sha256 of the cookie token, never the token; 30 days sliding.
  A signup session carries the GitHub identity until the account exists. An
  hourly cron (`gc`) deletes expired rows.

Migration `002` adds `names` (who owns a package name), `submissions`,
`listings` (the live entry per kind and name), `revocations`, `mail_queue` and
`published` (the stored signed index).

The GitHub access token is used for the two reads and discarded. Only verified
emails are stored or accepted.

## Security notes

The OAuth `state` is bound to a host-only, `HttpOnly`, `SameSite=Lax` cookie. An
attacker who can plant that cookie in a victim's browser (a sibling subdomain,
or a man-in-the-middle on plain HTTP) can force a login into the attacker's
account. Serve the site with HSTS to close that gap.

Listing key custody: the `listing_key` setting is write-only and is never
logged. Anyone who holds it can sign an index that core sites trust, so keep
the key file at mode `0600`, outside the repository, and delete the local copy
once it is set. A revoked developer key stays on the revocation list for good.

## Shipping this version

1. Set the listing key from a file, so the secret never appears in a shell
   history or process list:
   `hatamoto plugin settings set devportal listing_key @/path/to/listing.key`
   where the file is mode `0600`. Set `review_email` the same way if needed.
2. Update the plugin. The new permissions (`packages.inspect`, `mail.send`) and
   the two new allowed hosts need a fresh grant by the owner after the update.
3. Migration `002` is new and runs on update; ship it as a manual step.
4. The first `resign` runs one period (24 hours) after the plugin loads, so
   approve a submission once after setting the key (approving publishes). Then check that `GET /p/devportal/registry.json` answers `200` and
   that a core started with `HATAMOTO_DEVELOPER_REGISTRY_URL` pointing at it
   verifies the index.
5. Remove test accounts and test listings afterwards.

## Development

```
scripts/build-plugin.sh plugins/devportal
```

To try it in a local core, put the built plugin in a directory
`<dir>/devportal/` and start core with `HATAMOTO_PLUGIN_DIR=<dir>` (see the core
plugin API, "Development"). The manifest `config` keys
`github_authorize_url`, `github_token_url` and `github_api_url` exist so unit
tests can substitute the endpoints. A local core cannot use them: `http.fetch`
only reaches `allowed_hosts` and refuses
loopback and private addresses, and editing the manifest invalidates the
signed package. An end-to-end sign-in needs the real GitHub.

## License

Apache-2.0, see `LICENSE`.
