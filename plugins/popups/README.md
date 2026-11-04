# popups

Announcement bars and popups with page targeting, start and end dates and frequency caps, built to stay accessible and keep pages cached. It covers what OptinMonster, Popup Maker, Simple Banner and WPFront Notification Bar do on WordPress, without making any page uncacheable.

## Use

1. Install the plugin from Admin > Settings > Plugins and grant the permissions it asks for.
2. Open its **Settings** tab and add up to 10 campaigns. Each one has:
   - **Id**: lowercase letters, digits and dashes. It is also the dismissal key: a new id shows the campaign again.
   - **Kind**: `bar` or `popup`.
   - **Message**: plain text, up to 500 characters.
   - **Link label** and **Link url**: an optional link. Only `http`, `https` and site paths starting with `/` are kept; anything else drops the link.
   - **Include paths** and **Exclude paths**: one glob per line. `*` matches inside one path segment, `**` matches any depth, and `/blog/**` also matches `/blog`. An empty include list means every page.
   - **Start** and **End**: `YYYY-MM-DD` or `YYYY-MM-DD HH:MM`, always UTC. A date alone starts at 00:00 and ends at 23:59:59. Empty means open.
   - **Frequency**: how soon a visitor sees it again after closing it: `session` (next visit), `day`, `week` or `once`.
   - **Trigger** (popups): `load`, `delay` (after **Delay seconds**), `scroll` (after **Scroll percent**) or `exit` (the pointer leaves through the top of the window).
   - **Position** (bars): `top` or `bottom`. Bars always show on load.
3. Save. Cached pages are refreshed.

A bar is a fixed strip that sits on top of the page and never pushes content. A popup is a native modal dialog: it has a visible close button, closes with Esc, keeps focus inside while open and returns focus to the previous element afterwards. Both can be dismissed.

Pages stay cached. The server only publishes the campaigns that can still apply to the page, as one `<meta name="hatamoto-popups">` tag in the head. The browser script checks the dates, paths and frequency again, so a cached page never shows an ended or not yet started campaign.

## Permissions

- `settings`: the head filter reads the campaigns you saved.
- `site.scripts`: the browser script and stylesheet that draw the bar and the dialog.

It asks for nothing else: no `http.fetch`, no database, no key-value store, and no route.

## Data kept

Nothing on the server. The visitor's browser remembers dismissals under the key `hm-popups:<id>`, with the dismissal time as value: in `sessionStorage` for the `session` frequency, in `localStorage` for `day`, `week` and `once`. Nothing leaves the browser. If storage is blocked, the campaign shows on every page view.

## Build

From the repository root: `scripts/build-plugin.sh plugins/popups`.

## Limits

- At most 10 campaigns, 500 characters per message.
- No forms inside popups; link to the form page instead.
- No A/B tests and no analytics.
- The exit trigger does not show on touch-only devices.
- At most one bar per position and one popup per page view; the first match in the settings order wins.
- No animation.
- A bar is 48px high, one line; a long message is cut with an ellipsis. The page reserves that space from the stylesheet before first paint. When the bar does not show (not started yet, already dismissed, or closed), the script removes the reserve, so a visitor who already dismissed a bar may see the reserved space drop when the script runs.
- A scheduled campaign is in the page source up to about 31 days before its start, so do not put secret codes in one until it starts.
- The theme's base layout must include the plugin slots (the default theme does) so the core links the script and stylesheet.

## License

Apache-2.0.
