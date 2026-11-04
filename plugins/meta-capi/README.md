# meta-capi

Meta Conversions API: sends a server-side Lead event to Meta for each form submission, with hashed contact details, to back up the browser pixel.

## Use

1. Install the plugin from Admin > Settings > Plugins.
2. Grant the permissions it asks for.
3. Open its **Settings** tab.

## Permissions

- `settings`: reads its own settings.

## Build

From the repository root: `scripts/build-plugin.sh plugins/meta-capi`.

## License

Apache-2.0.
