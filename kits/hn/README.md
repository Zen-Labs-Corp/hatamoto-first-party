# hn kit

Sets a site up as a Hacker News style community: the `hn` plugin, the `hn-classic`
theme, a content type, the pages, a header menu and the comment settings. Plugin
details: [../../plugins/hn/README.md](../../plugins/hn/README.md).

## What it creates

- Content type **Stories** (`hn-story`), addressed as `/item/<number>`.
- 16 pages: News, Newest, Past, Best, Ask, Show, Jobs, From, Submit, Comments,
  Threads, Login, User, Favorites, Search and Guidelines. One page per path the
  plugin serves, so the plugin fills each one.
- Header menu: new, past, comments, ask, show, jobs, submit.
- The `hn` plugin and the `hn-classic` theme, from the registry.

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
tick, and the hn plugin declares four:

- `site.scripts`: for multi-line submit text and votes without a reload. Without it
  the plugin falls back to a single-line submit text field and votes reload the page.
- `entities.write.own`: the plugin creates and edits stories in its own content type.
- `comments.moderate`: flags, vouches and moderator actions on comments.
- `client.key`: a pseudonymous key per visitor, for rate limits and network bans.

## Apply

```
hatamoto kit apply hn --accept-comment-warnings \
  --grant hn:site.scripts,hn:entities.write.own,hn:comments.moderate,hn:client.key
```

Add `--activate-theme --homepage` on a site that is not fresh. `--dry-run` prints the
steps and changes nothing.

## Core features

`requires` lists `comment.policy`, `comment.rank`, `plugin.entity-write` and
`comment.signin`. A core that lacks one refuses the kit.
