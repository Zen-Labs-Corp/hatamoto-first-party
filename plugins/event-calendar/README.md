# event-calendar

Events with dates, venue and ticket link: upcoming and past lists for the page builder, an iCal feed and Event structured data.

It covers what The Events Calendar, Events Manager and Google Calendar Events do on WordPress, on top of a normal Hatamoto content type: you keep editing events like any other content, and the plugin adds the lists, the calendar feeds and the search-engine markup.

## Use

1. Install the plugin and grant the permissions it asks for. The content types ship with it: installing creates **Calendar event**, **Venue**, **Organizer**, **Series** and **Category** (slugs `calendar-event`, `calendar-venue`, `calendar-organizer`, `calendar-series` and `calendar-category`). There is nothing to paste. If your site already has a content type with one of those slugs that was not made by this plugin, the install is refused: rename that type first. An existing `event` type from version 0.1 keeps working (see Event content type below).

2. Add events as normal content. An event needs a start; an event whose start cannot be read is left out everywhere. Only published events are used. An event can point to a venue, organizers, a series and categories; if a reference cannot be read, the plain text venue fields of the event are used instead. A cancelled or postponed event stays in the lists and the feeds and is marked as such.

3. Show the details of an event on its page. The default theme has no layout of its own for calendar events, so the card does not appear by itself:
   - **Widget (main path).** In the page builder, add the **Event details** widget to the event page (or to the layout events use). It shows the card: date and time, status, venue, a link to the venue, organizer, price, an online link for online and hybrid events, the series, a **Get tickets** button and the add-to-calendar links. It reads the data source **This event** (`ext.event-calendar.event`) for the page's own event and needs no setup.
   - **Partial (needs a theme edit).** A theme can instead call `{{ template "plugin/event_calendar/event_details" . }}` in the layout that renders events, for example a `single-calendar-event` layout. This is a template edit and is not done for you.

4. Add to calendar: the card links to Google Calendar, Outlook, Yahoo and an Apple/iCal file (`/p/event-calendar/event/<slug>.ics`). In a theme template, `{{ plugin_event_calendar_addlink "google" .Entity.ID }}` returns one of these links (`google`, `outlook`, `yahoo` or `ics`), empty when the event cannot be found.

5. In the page builder, bind a list widget to the data source **Event calendar: Upcoming events** or **Event calendar: Past events** (`ext.event-calendar.upcoming`, `ext.event-calendar.past`). Upcoming lists events that are not over yet, soonest first (an event in progress counts as upcoming); past lists the rest, most recent first. No event is in both. Each item has the title, URL, excerpt, image and these attributes: `when`, `start_at`, `end_at`, `all_day`, `venue_name`, `venue_address`, `ticket_url` and `ics_url`. When the page has a language, events of that language and events without one are kept.

6. Calendar feeds, for visitors to subscribe to or download:
   - `/p/event-calendar/events.ics`: all upcoming events.
   - `/p/event-calendar/event/<slug>.ics`: one event (past events work too), the `<slug>` being the event's slug.

   An unknown, draft or other-type slug answers 404. A cancelled event is written with `STATUS:CANCELLED`, a postponed one with `STATUS:TENTATIVE`. The feed URLs and the event ids in them are the same as in version 0.1, so existing subscriptions keep working.

7. In a theme template, `when` renders the date and time as plain text, for example `Sat 14 Feb, 7:00 to 9:30 pm`:

   ```
   {{ plugin_event_calendar_when .Attributes.start_at .Attributes.end_at .Attributes.all_day }}
   ```

   Same-meridiem times are compacted (`7:00 to 9:30 pm`), all-day events read `Sat 7 Mar (all day)`, and the year is added only when it differs from the current year. Bad input renders nothing.

8. Event pages get Event structured data: the plugin sets `@type` to `Event` and adds `startDate`, `endDate`, `eventStatus` (scheduled, cancelled or postponed), `eventAttendanceMode`, `location` (the venue with its address and coordinates, and a virtual location for online events), `organizer`, `offers` (from the ticket link, with the price and currency when set) and `image`, only where the page does not already carry that key.

9. Optional: make the feed reachable at `/events.ics` by adding a redirect from `/events.ics` to `/p/event-calendar/events.ics`.

### Settings

| Setting | Default | Meaning |
|---|---|---|
| Event content type (`schema_slug`) | empty | Empty means automatic: events of `calendar-event` and of an older `event` type are both used. Name one content type to use only that one. |
| Time zone (`time_zone`) | `UTC` | IANA name such as `Europe/London`. An event can carry its own zone. |
| Date format (`date_format`) | `short` | `short` (`Sat 14 Feb`), `us` (`Sat, Feb 14`) or `iso` (`2026-02-14`). |
| Show map (`show_map`) | off | Show an OpenStreetMap map in the event card. Turning it on looks up venue addresses (see Permissions). |
| Nominatim contact (`geocode_contact`) | empty | An email or web address added to the lookup's User-Agent, as the Nominatim usage policy asks. Use one you control, for example `events@example.org`. |

Set the time zone to your site's zone. The editor stores times without a zone, so the plugin reads them in this zone, shows them in it, and converts them to UTC for the calendar feeds. A time that carries its own offset keeps it. An unknown zone name falls back to UTC.

### The map

The map is off by default. With **Show map** on, the card shows a small map of the venue: its latitude and longitude when the venue has them, otherwise the position found for its address. The tiles are loaded by the visitor's browser from OpenStreetMap (an image request per tile, so OpenStreetMap sees the visitor's request), with the attribution shown under the map and a plain **View on OpenStreetMap** link. Without JavaScript only the link shows.

### Upgrading from 0.1

- Updating from 0.1.0 needs the site owner to confirm it: the new version asks for another host (`nominatim.openstreetmap.org`) and for the `builder.island` permission. Until the update is confirmed and `db` is granted, the plugin keeps reading the events themselves, as 0.1 did.
- If you saved **Event content type** as `event` in 0.1, that value is still stored and pins the plugin to that one type, so the new **Calendar event** type is ignored. Clear the setting (leave it empty) to use both types.

## Permissions

- `entities.read`: reads the events and the venues, organizers, series and categories they point to.
- `builder.data`: answers the three page-builder data sources.
- `settings`: reads the settings above.
- `db`: the plugin's own event index (used by the lists and the feed, and kept up to date when events change, are imported or are removed, plus an hourly check) and the cache of address lookups. Both live in the plugin's own schema.
- `http.fetch`, to `nominatim.openstreetmap.org` only: looks up the position of venue addresses that have no coordinates. It runs only while **Show map** is on, results are cached, a failed lookup is retried after a day, and it makes at most one request per second (at most 20 lookups per hourly run).
- `builder.island`: runs a small script on pages that use the Event details widget, which draws the map. Without this grant the widget still shows everything except the map picture.

Nothing else: no mail, no scripts added to every page, no other hosts.

## Data kept

The plugin keeps three things in its own database schema: the event index (one row per event with its time, name, venue, categories and status, plus a marker for when it was last checked in full and a short list of recently removed event ids), and the address lookup cache (address text, position, when it was looked up). Events themselves stay in your content. Both tables are removed with the plugin's data; the index is rebuilt from your events if it is lost.

## Build

From the repository root: `scripts/build-plugin.sh plugins/event-calendar`.

## Limits

- Lists and the feed read the plugin's index, so they do not depend on how many events the site has; the feed holds at most 500 upcoming events. Until the first complete hourly check of the index has run (or if the plugin has no database access), the plugin reads the events themselves, at most 1000 per request.
- At most 48 items per builder source.
- Core gives the page builder 500 ms in total for external data. The plugin stops looking up venues, organizers and series after 300 ms; the card then shows what the event itself holds.
- No recurring events yet: the content type has the repeat fields, but events are not expanded into dates until a later release. No ticketing or payments: only a ticket link. The map is off by default.
- The calendar feeds are cached for 15 minutes.
- The feed holds upcoming events only; past events are available one by one.
- The feed is not filtered by language.

## License

Apache-2.0.
