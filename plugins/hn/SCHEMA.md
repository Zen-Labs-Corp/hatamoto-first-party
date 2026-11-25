# Data model of the hn plugin

The plugin owns one private database area, `plugin_hn`, managed by the host. Table
names are unqualified in every statement. Migrations use goose `-- +goose Up` only, no
functions or triggers, foreign keys only between tables of this area. Ids are
`bigint GENERATED ALWAYS AS IDENTITY` or the core uuid of the story or comment.
Migrations are append-only once released.

Block rule: each phase owns one numbered block of `migrations/`: `010` accounts,
`020` stories, `030` comments, `040` moderation, `050` parity (polish has no tables).
A phase that needs a column or table adds `011_..` to `019_..` (its own block number
plus 1 to 9) before the first release; nobody edits another phase's block.

Cross-reads (read only, the tables exist from day one): stories read `users`, `hidden`
and `bans`; comments read `story_meta`; moderation reads `users`, `story_meta` (and
writes its `dead`) and `comment_meta`; parity reads `comment_meta` and `story_meta`.

Every table has `created_at timestamptz NOT NULL DEFAULT now()` unless noted.

<!-- tables:begin -->
| Table | File | Phase |
|---|---|---|
| `users` | `010_accounts.sql` | accounts |
| `user_logins` | `011_accounts_password.sql` | accounts |
| `item_numbers` | `020_stories.sql` | stories |
| `story_meta` | `020_stories.sql` | stories |
| `story_votes` | `020_stories.sql` | stories |
| `story_actions` | `021_stories_actions.sql` | stories |
| `comment_meta` | `030_comments.sql` | comments |
| `comment_votes` | `030_comments.sql` | comments |
| `flags` | `040_moderation.sql` | moderation |
| `vouches` | `040_moderation.sql` | moderation |
| `bans` | `040_moderation.sql` | moderation |
| `banned_domains` | `040_moderation.sql` | moderation |
| `net_bans` | `040_moderation.sql` | moderation |
| `rate_events` | `040_moderation.sql` | moderation |
| `mod_log` | `040_moderation.sql` | moderation |
| `kills` | `041_moderation_kills.sql` | moderation |
| `favorites` | `050_parity.sql` | parity |
| `hidden` | `050_parity.sql` | parity |
| `search_docs` | `050_parity.sql` | parity |
| `visits` | `050_parity.sql` | parity |
<!-- tables:end -->

## 010_accounts

Written by accounts (karma is also changed by stories and comments through `core.AddKarma`).

| Table | Columns |
|---|---|
| `users` | `id`, `commenter_key` text unique, `username`, `username_lower` unique, `about` default '', `karma` integer default 1, `showdead` boolean default false, `noprocrast` boolean default false, `maxvisit` integer default 20, `minaway` integer default 180 |

## 011_accounts_password

Written by accounts only. The host runs plugin migrations out of order, so 011 applies after 051 on sites that already run an earlier version.

| Table | Columns |
|---|---|
| `user_logins` | `user_id` primary key, references `users` (cascade), `account` text unique (plugin-owned id passed to `comments.identity.*`, 32 hex chars), `pw_hash` (bcrypt), `email` default '' (optional, never shown), `pw_changed_at` |

The hash and the email never leave this table (`users` is read as `to_jsonb(u)`). `users.commenter_key` is the key `comments.identity.ensure` returned for an account created with a password; login re-attaches the row when core gave the account a new commenter (for example after a reinstall). An email-link member who connects a password keeps their key.

## 020_stories

Written by stories (`comment_count` also by comments, `dead` also by moderation).

| Table | Columns |
|---|---|
| `item_numbers` | `item_no` identity primary key. Reserve-only: a row is inserted first and its number becomes the story slug, so the canonical URL reads `/item/<number>`. The `/item/{slug}` url_pattern is set on the `hn-story` type by the hn kit (or by the owner in Settings > Content types), not by the plugin package; without it stories route at `/<number>` |
| `story_meta` | `entity_id` uuid primary key, `item_no` unique, `author_id` references `users`, `title`, `url`, `url_key` (normalised, indexed), `domain` (indexed with `created_at`), `body`, `kind` one of story, ask, show, job, `points` default 1, `comment_count` default 0, `score` double precision default 0 (gravity), `dead` default false. Indexes: score desc where not dead; created desc; kind + score; author + created |
| `story_actions` | `id` identity, `author_id` references `users` (cascade), `kind` submit or edit; append-only, so deleting a story does not free a `submit_per_hour` slot. Index: author + kind + created |
| `story_votes` | `story_id` references `story_meta` (cascade), `user_id` references `users` (cascade), primary key both, `counted` (022) false when the voting-ring check stored the vote without counting it |

## 030_comments

Written by comments.

| Table | Columns |
|---|---|
| `comment_meta` | `comment_id` uuid primary key (the core comment id), `entity_id` uuid, `parent_id` uuid null, `author_id` references `users` null (staff), `author_name`, `body_html`, `live` default true, `points` default 1, `rank` double precision default 0, `dirty` default true. Indexes: entity; author + created; partial on dirty; created desc |
| `comment_votes` | `comment_id` references `comment_meta` (cascade), `user_id` references `users` (cascade), `value` smallint 1 or -1, primary key both |

## 040_moderation

Written by moderation.

| Table | Columns |
|---|---|
| `flags`, `vouches` | `kind` story or comment, `target_id` uuid (no foreign key, the target is a story or a comment), `user_id` references `users` (cascade), primary key all three |
| `bans` | `user_id` primary key references `users` (cascade), `mode` dead or blocked, `reason` |
| `banned_domains` | `domain` primary key, `reason` |
| `net_bans` | `net_key` primary key, `reason`, `expires_at` null |
| `rate_events` | `id` identity, `kind`, `key`; index `(kind, key, created_at)` |
| `mod_log` | `id` identity, `actor`, `action`, `target`, `detail` |
| `kills` (041) | `kind` story or comment, `target_id` uuid, `cause` flags, owner or ban, primary key `(kind, target_id)` |

## 050_parity

Written by parity.

| Table | Columns |
|---|---|
| `favorites` | `user_id` references `users` (cascade), `kind` story or comment, `target_id` uuid, primary key all three |
| `hidden` | `user_id` references `users` (cascade), `story_id` references `story_meta` (cascade), primary key both |
| `search_docs` | `kind`, `target_id`, `entity_id`, `title`, `body`, `tsv` tsvector (plain column written by parity), `src_hash` md5 of the comment HTML the document was built from (`051_parity_src_hash.sql`), primary key `(kind, target_id)`, gin index on `tsv` |
| `visits` | `user_id` primary key references `users` (cascade), `window_start`, `last_seen` |
