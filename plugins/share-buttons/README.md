# share-buttons

Share links for posts and pages as a theme partial and a section: LinkedIn, X,
Facebook, WhatsApp, Bluesky and email. Plain links: no JavaScript, no tracking,
no share counts. It replaces plugins such as AddToAny, Simple Share Buttons and
Social Pug.

## Use

1. Install the plugin and open its **Settings** tab:
   - **Networks**: the networks to show, in display order, as a
     comma-separated list of ids (`linkedin`, `x`, `facebook`, `whatsapp`,
     `bluesky`, `email`), for example `bluesky, x, email`. Empty means the
     defaults: linkedin, x, facebook, whatsapp, email.
   - **Style**: icons and labels, icons only (the label stays available to
     screen readers) or labels only.
   - **Site URL**: your public origin with the scheme and no path, for example
     `https://example.com` (not `example.com`, not `https://example.com/en`:
     those are ignored and the section renders nothing). Only the section needs
     it.
2. In a post or page template, add the partial and pass the full page data:

   ```
   {{template "plugin/share_buttons/links" .}}
   ```

   It reads `.Head.Canonical` for the address and `.Entity.Title` (or
   `.Page.Name` when there is no entity) for the title. Call it with `.`: a
   dot without `.Head` is a theme error. It renders nothing when the page has
   no canonical address, or when the address is not absolute (a site without a
   public base URL has only a path there).
3. Or add a `share_buttons` section to a page. The address is the section's
   `url` content if set, otherwise Site URL plus the page path (so set Site URL
   first). The title is the section's `title` content, otherwise the section
   title. With no address the section renders nothing. Section packs carry no
   schema, so the page editor shows no `url` or `title` field: those keys can
   only be set in the page's JSON (API or import). Normally leave them out and
   set Site URL.

Remove the `{{template}}` call from your theme before you disable or uninstall
the plugin: core removes the plugin's partials and sections when it unloads, so
the call then has nothing to resolve (see `docs/THEMES.md` and
`docs/PLUGIN_API.md` in the Hatamoto core repository).

The theme's base layout must call the plugin-head slot so `assets/plugin.css`
is linked (the default theme does).

## Permissions

`settings` only. Templates cannot read a plugin's settings, so the plugin's
template funcs read its own settings. No `site.scripts`, no `http.fetch`, no
network access from the plugin. The buttons are ordinary links to the
networks; web links open in a new tab with `rel="noopener noreferrer"`.

## Data kept

None. No database, no key-value store, no cookies, no logs, nothing sent
anywhere by the plugin.

## How it works

Three template funcs (`network`, `style`, `page_url`) return the enabled
network at a position, the style and the absolute section address. They fail
open: a missing grant, a host error or a bad setting gives the defaults, and
the templates fall back to the default networks even when a func returns
nothing, and the templates decide the fallback once per render: if the first
network call fails they show the default list, otherwise a later failure just
ends the list. The plugin keeps its settings for 300 ms, so the 7 or 8 func
calls of one render cost one settings read. Core clears the page cache when
settings are saved but cannot tell the plugin, so a page rendered right after a
save (the window is under a second) can keep the previous settings until its
page cache entry expires. Saving the settings again or clearing the page cache
fixes it. The output is cached with the page.

## Build

Needs the Go toolchain pinned in `go.mod`. From the repository root:

```
scripts/build-plugin.sh plugins/share-buttons
```

The tests run with `GOWORK=off go test ./...` in this folder. `build/` holds
`plugin.wasm`, `plugin.json`, `partials/`, `sections/` and `assets/`.

## Limits

- No share counts, no Web Share API (it needs JavaScript), no follow buttons.
- The section needs Site URL: core gives sections neither the canonical
  address nor the page title.
- Labels are English only.
- WhatsApp and Bluesky share the title and the address; email uses the title
  as subject and the address as body.
- The default theme does not call the partial; add it to your theme.
- The community registry build must ship `partials/` and `sections/` (a core
  follow-up).

## License

Apache-2.0, see `LICENSE`.
