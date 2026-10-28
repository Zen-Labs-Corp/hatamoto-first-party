# hatamoto-first-party

The first-party plugins for [Hatamoto](https://hatamoto.dev), one folder each.
They are built from source, signed with the Hatamoto first-party key and
listed in the plugin directory at hatamoto.dev/plugins.

| Plugin | Folder | What it does |
|---|---|---|
| `gtm` | [plugins/gtm](plugins/gtm) | Google Tag Manager: loads your GTM container on every public page |
| `google-analytics` | [plugins/google-analytics](plugins/google-analytics) | Google Analytics: loads the Google tag (GA4) on every public page, with a way to leave your own visits out |
| `searchconsole` | [plugins/searchconsole](plugins/searchconsole) | Google Search Console: OAuth, sitemap submit, URL inspection, daily search performance |
| `stats` | [plugins/stats](plugins/stats) | Receives the opt-in install stats ping and shows the counts (runs on hatamoto.dev only) |

Each plugin is its own Go module, built for `wasip1` against the public
[SDK](https://github.com/Zen-Labs-Corp/hatamoto-sdk) (`hatamoto.dev/sdk`). The
plugin contract is
[PLUGIN_API.md](https://github.com/Zen-Labs-Corp/hatamoto/blob/main/docs/PLUGIN_API.md)
in the core repository.

## Layout

```
plugins/<name>/        one plugin; <name> = plugin.json "name" = route prefix /p/<name>/
template/              what scripts/new-plugin.sh copies
scripts/build-plugin.sh   vet, test and build one plugin exactly as CI and the registry do
scripts/new-plugin.sh     create plugins/<name> from the template
scripts/changed-plugins.sh  which plugins a change touches (CI)
```

## Build

```sh
scripts/build-plugin.sh plugins/gtm      # -> plugins/gtm/build/
```

Needs Go (the `toolchain` line of each `go.mod` pins it) and `jq`; `node` runs
any `*_test.js`. No `go.work`: every plugin builds alone with `GOWORK=off`, as
the registry build does.

## Releases

A release of one plugin is the tag `<name>/v<version>` on the commit that sets
that version in `plugins/<name>/plugin.json`. The registry lists it once
`manifests/<name>.json` in
[hatamoto-plugins](https://github.com/Zen-Labs-Corp/hatamoto-plugins) pins that
commit with `"subdir": "plugins/<name>"`.

## History

`gtm`, `searchconsole` and `stats` were imported with their full history from
their former repositories (`hatamoto-plugin-<name>`, now archived).

## License

Apache-2.0, see [LICENSE](LICENSE). Contributions: see
[CONTRIBUTING.md](CONTRIBUTING.md).
