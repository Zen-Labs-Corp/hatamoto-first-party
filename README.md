# stats: install stats receiver as a plugin

Receives the opt-in install stats ping that Hatamoto sends once a day
(`POST /p/stats/v1/ping` with exactly `install`, `version` and `channel`) and shows
the totals to the site owner on the plugin's admin page.

Work in progress: the plugin is not built yet. What it stores, and for how long,
will be documented here before the first release.

## License

Apache License 2.0, see [LICENSE](LICENSE).
