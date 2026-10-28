# Contributing

- **Sign off every commit** (`git commit -s`): the Developer Certificate of
  Origin, checked on every pull request by the `dco` workflow.
- **One plugin per folder.** `plugins/<name>/` holds one Go module; the folder
  name, `plugin.json` `"name"` and the route prefix `/p/<name>/` are the same.
- **A new plugin** starts from `scripts/new-plugin.sh <name> "<description>"`.
- **Before a pull request**, run `scripts/build-plugin.sh plugins/<name>` for
  every plugin you touched. CI runs the same script.
- **Keep permissions minimal.** A new permission in `plugin.json` needs a
  reason in the pull request.
- **Migrations** (`migrations/*.sql`) are append-only once released.
- **No `go.work`**, no `replace` of `hatamoto.dev/sdk`, no local-path
  `replace`: the registry build refuses them.
- The `sdk` range in `plugin.json` must match the `hatamoto.dev/sdk` version in
  `go.mod`.

Security reports: see the core repository's
[SECURITY.md](https://github.com/Zen-Labs-Corp/hatamoto/blob/main/SECURITY.md),
not a public issue.
