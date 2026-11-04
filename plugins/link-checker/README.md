# link-checker: a broken link checker for Hatamoto

Finds broken outbound and internal links in your published content on a
schedule and lists each one with the page it is on and a link to fix it. It
replaces the WordPress plugins Broken Link Checker and Broken Link Checker by
AIOSEO.

## Use

1. Install the plugin (Admin > Settings > Plugins) and grant its permissions.
   `http.fetch.any` lets it request any address, so the install screen shows it
   as a warning: that is how outbound links on other sites get checked.
2. Wait. A scan runs every 5 minutes. Each run reads some pages, checks the
   links that are due and stops after about 8 seconds. The first pass over a
   typical site takes up to a day; the page says "First scan still running:
   checked N of M links so far" until it is done. Pages you publish are read at
   once.
3. Open **Broken link checker**. Tabs show the links by result with their count:
   Broken, Redirected, OK, Ignored (and Unknown while some link has not been
   decided yet). The header shows when the last scan ran and how many links are
   waiting.

Each row is one link with these columns:

- **Status**: the HTTP status (`404`), or a short reason (`no page`,
  `timed out`, `connection failed`, `blocked address`).
- **Link**: the address, when it was last checked and, for a broken link, since
  when it has been broken.
- **Found on**: every page that contains the link, with its name and public
  path. **Copy editor path** copies `/entities/<id>`: paste it after your admin
  address to open the editor. If the browser blocks the clipboard, the text is
  selected and you press Ctrl/Cmd+C.
- **Recheck** checks the link on the next run. **Ignore** hides a link you
  accept (it moves to the Ignored tab, where **Un-ignore** puts it back).

How links are judged:

- Internal links are looked up in the site, never over the network. A path that
  is a site redirect is shown as Redirected, not Broken. The home page, listing
  pages above published pages, files (a path ending in an extension) and the
  `/media/`, `/uploads/`, `/p/` and `/assets/` paths count as fine.
- Outbound links get a HEAD request, and a GET request when the server refuses
  HEAD (405 or 501). 2xx and 3xx are fine; other 4xx and 5xx are broken, except
  429 and 503, which stay Unknown and are checked again the next day. A timeout
  or connection error is Unknown the first time and Broken when the next check
  fails too.
- Links are found in every text of a page's content: `<a href>` in HTML and the
  `url`, `href` and `link` values. `mailto:`, `tel:`, `#anchor`, `javascript:`
  and `data:` links are skipped, fragments are dropped, and an absolute link to
  your own site counts as an internal one.

Settings (plugin settings tab):

- **Links never checked**: address prefixes to skip, either a full address such
  as `https://example.org/private` or a site path such as `/members`.
- **Days between checks of a working link**: 1 to 90, default 7.
- **Timeout per outbound request**: 1000 to 10000 ms, default 5000. A run also
  never starts a request that would not finish inside its time budget.

## Permissions

| Permission | Why |
|---|---|
| `entities.read` | List published pages, read their content to find links, and look up internal links by path. |
| `redirects.read` | An internal link whose path is a site redirect is shown as Redirected, not Broken. |
| `http.fetch` | Check outbound links (HEAD, then GET). |
| `http.fetch.any` | Outbound links can point anywhere, so the allowed hosts are `*`. Shown as a warning at install. |
| `db` | Keep the links, where they are used and the result of each check. |
| `kv` | Keep where the scan stopped and when it last ran. |
| `settings` | Read the three settings above. |
| `log` | One warning line when a step of a run fails. |

No request sends cookies or credentials. The plugin changes no content and adds
nothing to public pages.

## Data kept

Tables in the plugin's own schema:

- `links`: one row per distinct link with its result (status, HTTP status,
  reason, last checked, first broken, next check, consecutive failures).
- `link_pages`: which page contains which link, with the page name and path
  copied so the admin table needs no lookups.
- `known_paths`: paths of published pages and their parent paths, used to tell a
  listing page from a missing one.

`kv` holds `scan:cursor` (where the current pass over the pages stands),
`scan:done` (when the last pass over all pages finished, and the locales seen)
and `scan:last` (the time of the last run).

A link that is on no page for 30 days is deleted by the scan, and so are page
rows and known paths not seen for 30 days. An ignored link is kept as long as it
is still on a page. Nothing is sent anywhere except the checks themselves: one
HEAD or GET request to the address in the link.

## Build

```sh
scripts/build-plugin.sh plugins/link-checker
```

This runs gofmt, vet and the tests, then builds `build/plugin.wasm` and
collects the package in `plugins/link-checker/build/`.

## Limits

- It never fixes anything: no link or content is changed.
- Images referenced from CSS are not checked, and neither are images in HTML.
- A redirect on another site is followed invisibly by the host, so outbound
  redirects are not reported.
- Internal redirects are matched by exact source path only, not by pattern.
- Connection errors are not told apart: a failed DNS lookup, a refused
  connection and a TLS error all show as `connection failed`.
- Throughput is small by design: about 10 to 20 outbound checks per 5-minute
  run, and at most one request per host per second. A large site needs days for
  a full pass.
- Some sites answer automated requests with 403 or 429; a 403 is shown as
  Broken, so use Ignore for those.
- With the SDK range this plugin declares (`^0.11`), paths with a language
  prefix are not resolved by the host. A path whose first segment is the
  language (or locale tag) of a published page, such as `/fr/contact`, is also
  looked up without that segment: it counts as fine when the unprefixed path is
  a page or a listing. This is a heuristic, not a check in that language.
- An internal link with no page stays pending (shown in no tab, counted as
  waiting) until the first pass over all published pages has finished, because
  listing paths are only known from their children.
