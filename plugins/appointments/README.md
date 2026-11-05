# appointments

Booking from Cal.com or Calendly: a section that embeds your scheduling page
and a button that links to it. You keep the scheduling, availability and
payments with the provider; the site only shows them. It replaces the kind of
booking plugins WordPress sites use (LatePoint, Amelia, Bookly, Simply Schedule
Appointments) for a business that is happy to run its calendar on Cal.com or
Calendly.

## Use

1. Install the plugin, grant its permissions and open its **Settings** tab:

   | Setting | Meaning | Default |
   |---|---|---|
   | Provider | `calcom` or `calendly`. Only a hint: the host of the link decides. | `calcom` |
   | Scheduling link | Your public link, for example `https://cal.com/your-name/30min`. Empty shows nothing. | empty |
   | Button label | Text of the booking button, at most 60 characters. | `Book an appointment` |
   | Embed height | Height of the embedded scheduler in pixels, 300 to 2000. | `700` |

   A link is accepted only when it is `https` on `cal.com`, a subdomain of
   `cal.com` (such as `app.cal.com`) or `calendly.com`, with a path, in lower
   case, without a user name, a port or spaces, and at most 500 characters.
   Anything else is ignored and nothing is shown.
2. Add an `appointments` section to a page. It embeds the scheduler in a
   sandboxed frame and, under it, a plain "Book on Cal.com" (or Calendly) link
   for visitors whose browser blocks the frame. The link comes from the
   section's `link` content key when it is set, otherwise from the setting. The
   section title, when set, is shown above the frame. With no valid link the
   section renders nothing.
3. Add a booking button to a theme template:

   ```
   {{template "plugin/appointments/button" .}}
   ```

   It shows the link from the settings and works with any dot, in a page, a
   layout or a section. With no valid link it renders nothing.
4. In a page or layout template that is passed the full page data, you can use
   the variant that falls back to the business profile's booking URL when the
   setting is empty:

   ```
   {{template "plugin/appointments/button_site" .}}
   ```

   It reads `.Site.Org.BookingURL` and only shows it when it is a valid
   cal.com or calendly.com link. Call it with the page dot only: a dot without
   `.Site` is a theme error, so never use it inside a section. This variant is
   an addition to the section and the plain button.

The plugin never fails a page: a settings problem, a host error or an invalid
link makes the section and the buttons render nothing.

Remove the template calls from your theme before you disable or uninstall the
plugin. Core removes the plugin's partials when it unloads, and a theme that
still calls a missing partial fails to render until the call is removed.

The theme's base layout must call the plugin-head slot so `assets/plugin.css`
is linked (the default theme does). The stylesheet is a few lines with
`appointments__*` classes.

## Permissions

- `settings`: reads the plugin's own settings (templates cannot).
- `site.scripts`: the section contains an `<iframe>`, and Hatamoto only
  registers plugin markup with a frame when this is granted. The plugin ships
  no script and runs none on your pages.
- Content-Security-Policy frame hosts: `https://cal.com`, `https://*.cal.com`
  and `https://calendly.com`. They are listed so the install prompt shows where
  frames can come from. While the site's base policy already allows every
  `https` frame, this changes nothing.

No `http.fetch`, no database, no key-value store, no routes, no admin page.

## Data kept

None. The plugin stores nothing and sets no cookies. Visitors who load the
embedded scheduler talk to Cal.com or Calendly, and that provider's own privacy
terms apply to them; nothing is loaded from the provider unless a page contains
the section (the frame is lazy-loaded) or the visitor follows a button.

## Build

From the repository root:

```
scripts/build-plugin.sh plugins/appointments
```

It checks the manifest, formats, vets, runs the tests and builds
`build/appointments.wasm`.

## Limits

- No native booking, availability or payments: the provider does all of that.
- Needs a Hatamoto with SDK 0.18, that is core v1.0.8 or newer; older hosts
  refuse the plugin.
- The Calendly `embed_domain` is not set, so Calendly may skip some of its own
  usage analytics for the embed.
- The section's `link` key has no field in the page builder; set it through the
  page's content JSON (API or import), or leave it out and use the setting.
- The embed height comes from the setting; a section's own `height` key is
  ignored.
- A provider's confirmation or payment step may open in a new tab, because the
  frame is sandboxed.
