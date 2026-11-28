# registry-pages

hatamoto.dev only: the plugin and theme directory pages, filled from the signed registry. It is not listed in the public registry and is not meant for other sites.

## What it does

- Fetches `https://hatamoto.dev/registry.json` and checks its ed25519 signature against the pinned first-party key. The developer index (`https://hatamoto.dev/p/devportal/registry.json`) is checked against its own listing key and merged in. An index that is unsigned, altered, signed by another key or past its `expires_at` is refused, and the directory shows the `unavailable` state.
- Keeps the result for 3 minutes (15 seconds after a failure). While the registry is unreachable the last good index is shown, but never at or after its own `expires_at`. One snapshot is shared between the plugin's instances through `kv`.
- On `/plugins` and `/themes` it replaces a placeholder block (a block of type `registry_directory`, or one whose anchor is `registry-directory`) with a `registry_directory` block. A page without that block is left as it is.
- Keeps one `registry-entry` entry per registry name, by cron every minute, so `/plugins/<name>` and `/themes/<name>` are real pages. A name that is not in the registry has no entry and gives a 404. Entries of names that left the registry are deleted. Nothing is written unless the plugin holds a verified index.
- On those entry pages it adds a `registry_detail` block with the card of that name. An entry whose name is no longer listed as that kind gets no block.
- Any error leaves the page unchanged. The hook never fails a page view.

### Block content

`registry_directory`: `state` (`ok`, `empty` or `unavailable`; `off` is only there for template parity and is never produced), `linked` (always `true`), `list_url` (`/plugins` or `/themes`, so one template can build the card links `<list_url>/<name>`), `cards`.

`registry_detail`: `type` (`plugin` or `theme`), `list_name` (`Plugins` or `Themes`), `list_url`, and `plugin` or `theme` holding one card.

Plugin card: `name`, `title`, `version` (with a `v`), `tier`, `tier_label`, `author` (community and developer only), `description`, `source` (https only), `perm_count`, `perm_warn`, `perm_lines` (`name`, `text`, `warn`), `permissions`, `search`, `about` (paragraphs), `features`.

Theme card: `name`, `title`, `version`, `description`, `source`, `screenshot`, `tier`, `tier_label`, `author`, `search`, `about`, `features`.

Cards are sorted first-party, community, developer, then by name. The plugin ships plain fallback sections for both block types; a section of the same type in the site theme wins.

## Permissions

- `http.fetch`, with `allowed_hosts` `["hatamoto.dev"]`: reads the two index URLs above, nothing else.
- `kv`: the shared verified snapshot, and a short list of names whose entry could not be created under its own slug.
- `entities.read`: lists the plugin's own `registry-entry` entries, so the sync works from what exists and survives edits and deletes made by the owner.
- `entities.write.own`: creates, updates and deletes `registry-entry` entries, only in that one content type. Entries are published at once, writes are rate limited by the host and appear in the audit log.

It does not ask for `settings`, `db`, `log`, `site.scripts` or any other host. The registry URLs and keys are constants.

## Install (one time, on hatamoto.dev)

1. Install the theme templates first: `layouts/single-registry-entry.html`, `sections/registry_directory.html` and `sections/registry_detail.html`. Without them the entry pages use a default layout.
2. Install and enable the plugin and grant its permissions.
3. In Settings > Content types > Registry entry, set the URL pattern to `/plugins/{slug}` and add an override: attribute `kind` equals `theme`, URL pattern `/themes/{slug}`. A package cannot ship these settings. Without them the entries live at `/{slug}`: if a create lands anywhere but `/plugins/<name>` or `/themes/<name>`, the sync deletes that entry, logs an error that names the missing URL pattern and creates nothing for five minutes, so set the pattern before the first tick or expect one create and delete per five minutes until then.
4. Put a `registry_directory` block on the `/plugins` and `/themes` pages where the directory should appear. Use only this block there: if the pages have core's `plugin_directory` or `theme_directory` section, remove it, because core would then serve `/plugins/<name>` and `/themes/<name>` itself and compete with the entries.
5. Wait one or two cron ticks. The first sync creates about fifty entries, and the host allows 60 writes a minute, so it can take a few ticks.

## Notes

- If the owner edits the SEO fields of an entry, the host treats the entry as taken over. The plugin then gets `not_owned` for it, remembers that for six hours (the refusal costs a write token, so it is not retried every minute) and leaves the entry alone. Removals run before updates in each tick, so a withdrawn name is never held back by refused writes.
- A name listed as both a plugin and a theme gets no entry, because both would need the same slug.
- Not ported from core: the guard against an index older than one already seen. The index is fetched over HTTPS from the site's own host, and its signature and expiry are still enforced.

## Layout

```
plugin.go            registers the render.page filter and the sync cron job
exports_wasip1.go    the //go:wasmexport lines
verify.go            pinned keys, envelope, signature check
index.go             index types, freshness, sanitisers, developer filter
cards.go permtext.go card types and builders, permission wording
directory.go         fetch, merge, snapshot, memory and kv cache
page.go              render.page: directory and detail blocks
sync.go              registry-entry sync
schemas/             the registry-entry content type
sections/            fallback sections
```

Status: 0.1.0, built against SDK v0.29.0.
