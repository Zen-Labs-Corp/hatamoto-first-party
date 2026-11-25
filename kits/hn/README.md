# hn kit

Sets a site up as a Hacker News style community: the `hn` plugin, the `hn-classic`
theme, a content type, the pages, a header menu and the comment settings. Plugin
details: [../../plugins/hn/README.md](../../plugins/hn/README.md).

## What it creates

- Content type **Stories** (`hn-story`), addressed as `/item/<number>`.
- 19 pages: News, Newest, Past, Best, Ask, Show, Jobs, From, Submit, Comments,
  Threads, Login, User, Favorites, Search, Guidelines, FAQ, Lists and API. One page per
  path the plugin serves, so the plugin fills each one; the last three are plain text.
- Header menu: new, past, comments, ask, show, jobs, submit.
- Footer menu: Guidelines, FAQ, Lists, API. On a site whose footer location already holds
  a menu, the kit keeps it: add the items by hand.
- The `hn` plugin and the `hn-classic` theme, from the registry.

Security, Legal and Contact are not included: their text depends on who runs the site.
Write them as pages and add them to the Footer menu in the admin.

## What the owner ticks on the review screen

- Use **News** as the homepage.
- Turn comments on (two comment warnings, accepted together).
- The community policy on Stories: reply depth 32, edit window 120 minutes, slow
  mode 30 seconds. `policy.enabled` in `kit.json` is only a hint: the policy applies
  when you tick it.
- Make `hn-classic` the active theme.

## Permissions

`grant: "all"` gives the plugin the permissions an owner needs no second thought
for. Every permission the review screen marks "Handle with care" stays a separate
tick. For hn there are four to tick:

- `entities.write.own`: the plugin creates and edits stories in its own content type.
- `comments.moderate`: flags, vouches and moderator actions on comments.
- `client.key`: a pseudonymous key per visitor, for the sign-up and login rate limits (without it, the limits are one site-wide bucket that one visitor can use up, so tick it)
  and for network bans.
- `comments.identity`: lets the plugin sign members in with their username and password
  (core v1.4.6 or newer). It also needs `comments.read` and the plugin's content type,
  which hn already has. Without it Create Account answers that sign-up is not possible
  and members can only use the email link. Revoking only this grant later does not sign
  anyone out (issued cookies stay valid until they expire or are signed out under
  Commenters); uninstalling hn does sign every member out.

The plugin also declares `site.scripts`, for multi-line submit text and votes without
a reload. Hatamoto does not grant it to a community package, so you have nothing to
tick: the plugin falls back to a single-line submit text field and votes reload the page.

## Apply

```
hatamoto kit apply hn --accept-comment-warnings \
  --grant hn:entities.write.own,hn:comments.moderate,hn:client.key,hn:comments.identity
```

Add `--activate-theme --homepage` on a site that is not fresh. `--dry-run` prints the
steps and changes nothing.

## First days

Nobody can flag until members earn karma (`flag_karma` is 30 and members start at 1).
Until then, kill spam from the **Recent** tab of the News moderation page. You can lower
`flag_karma` in the plugin settings.

## Core features

`requires` lists `comment.policy`, `comment.rank`, `plugin.entity-write`,
`comment.signin` and `comment.identity` (core v1.4.6 or newer). A core that lacks one
refuses the kit.
