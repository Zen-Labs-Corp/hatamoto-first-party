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

5. In the page builder, bind a list widget to the data source **Event calendar: Upcoming events** or **Event calendar: Past events** (`ext.event-calendar.upcoming`, `ext.event-calendar.past`). Upcoming lists events that are not over yet, soonest first (an event in progress counts as upcoming); past lists the rest, most recent first. No event is in both. Each item has the title, URL, excerpt, image and these attributes: `when`, `start_at`, `end_at`, `all_day`, `time_zone` (when the event has its own), `venue_name`, `venue_address`, `ticket_url` and `ics_url`. When the page has a language, events of that language and events without one are kept.

6. Page builder widgets. Besides **Event details**, the plugin adds six more:
   - **Event calendar**: a searchable calendar with month, list, week, day, photo, summary and map views, filters (search, category, venue, city, free or paid), previous and next navigation, and subscribe links (Google Calendar, Outlook and the iCal feed). It loads its events from the JSON route below.
   - **Mini calendar**: a compact month that marks the days with events. A day links to the full calendar; set its page in **Calendar URL**.
   - **Event countdown**: days, hours and minutes until the next event, or until the event whose slug you enter.
   - **Featured venue**: a venue with its address and its next event; set the venue slug or name.
   - **Event map**: a map of the venues of upcoming events, with a height setting. It shows only while **Show map** is on.
   - **Event card**: the card used inside Event details, which you can also place on its own.

   The calendar, countdown and venue widgets are seeded when you add them from the palette: they come with a plain list bound to the upcoming source, so without JavaScript a visitor still sees real events. The interactive views need JavaScript. The seed only applies to a newly added widget; widgets already on a page are not changed.

7. The upcoming and past sources take four optional filters in the builder: **Category** (name or slug), **Venue** (name or slug), **City**, and **Free events only**. They narrow the list before it is cut to its length.

8. JSON route, used by the widgets and open to your own scripts: `GET /p/event-calendar/api/events`. It returns the event occurrences that overlap a date range, soonest first, as `{"ready", "tz", "week_start", "show_map", "truncated", "events"}`. Each event has its id, title, slug, URL, start and end (UTC), the local `day` and `end_day`, `all_day`, the `when` text, venue name, slug, city and address, coordinates (null when unknown), categories, price, image, featured flag and status. Query parameters:
   - `from` and `to`: an RFC 3339 time or a date (`2026-02-14`, midnight in the site zone). The default is now to 31 days ahead.
   - `q` (search the title and venue), `category`, `venue`, `city`, `price` (`any`, `free` or `paid`), `slug` (one event), `limit` (at most 500) and `view` (accepted, not used by the server).

   A parameter that is unknown, repeated, longer than 200 bytes or malformed answers `400 bad request`, and so does a range that ends before it starts or spans more than 400 days. At most 500 events come back, and `truncated` is true when the cap was reached. The answer is cached for 60 seconds (`Cache-Control: public, max-age=60`). On a site with up to a few hundred events, the first event you create, update or publish runs the full reconcile, so the index is ready within moments; larger sites become ready after the next hourly run. If the index is not ready or the database fails, the route answers 200 with `"ready": false` and no events, and never an error text.

9. Calendar feeds, for visitors to subscribe to or download:
   - `/p/event-calendar/events.ics`: all upcoming events.
   - `/p/event-calendar/event/<slug>.ics`: one event (past events work too), the `<slug>` being the event's slug.

   An unknown, draft or other-type slug answers 404. A cancelled event is written with `STATUS:CANCELLED`, a postponed one with `STATUS:TENTATIVE`. The feed URLs and the event ids in them are the same as in version 0.1, so existing subscriptions keep working.

10. In a theme template, `when` renders the date and time as plain text, for example `Sat 14 Feb, 7:00 to 9:30 pm`:

   ```
   {{ plugin_event_calendar_when .Attributes.start_at .Attributes.end_at .Attributes.all_day .Attributes.time_zone }}
   ```

   Same-meridiem times are compacted (`7:00 to 9:30 pm`), all-day events read `Sat 7 Mar (all day)`, and the year is added only when it differs from the current year. The optional fourth argument is the event's own time zone, so the text matches the card and the lists; without it the site time zone is used. Bad input renders nothing.

11. Event pages get Event structured data: the plugin sets `@type` to `Event` and adds `startDate`, `endDate`, `eventStatus` (scheduled, cancelled or postponed), `eventAttendanceMode`, `location` (the venue with its address and coordinates, and a virtual location for online events), `organizer`, `offers` (from the ticket link, with the price and currency when set) and `image`, only where the page does not already carry that key.

12. Optional: make the feed reachable at `/events.ics` by adding a redirect from `/events.ics` to `/p/event-calendar/events.ics`.

### Repeating events

An event repeats when its **Repeats** field is set (daily, weekly, monthly or yearly). The other fields refine it:

- **Every** (`repeat_every`): every N days, weeks, months or years. The default is 1.
- **On** (`repeat_weekdays`, weekly): the days of the week. Empty means the weekday of the event's start.
- **Monthly by** (`repeat_by`): `day_of_month` (the start's day; day 31 in a shorter month falls on that month's last day) or `nth_weekday` (**Nth** (`repeat_nth`) is 1 to 5, or -1 for the last; empty means the start's own position, such as the second Thursday). A fifth weekday that a month does not have skips that month. A yearly 29 February happens in leap years only.
- **Ends**: **Until** (`repeat_until`, the last day) or **Count** (`repeat_count`, the number of generated dates, counted before skipping); neither means never. An until date before the first start leaves the event as a single date.
- **Extra dates** (`repeat_extra_dates`): more days with the same time of day. A date listed both as extra and as skipped is skipped.
- **Skipped dates** and **Changed dates** (`repeat_skip_dates`, `repeat_overrides` as `date|start|end`): managed by the Repeating events page below, though you can also edit them by hand. A changed date may move to another day; it then appears on the new day only.

Every date keeps the event's local time of day, also across a daylight saving change. The start and end you enter are the first date; the plugin works out the rest and keeps the dates from 90 days ago to 18 months ahead, at most 800 per event. The lists, the calendar, the JSON route and the feeds show one entry per date. In the feed each date is its own event: the first keeps the event's usual id, later dates add `-YYYYMMDD` to it, and there is no RRULE, so subscribers see the exact dates with skipped and changed dates applied. The file of one event (`/p/event-calendar/event/<slug>.ics`) holds its next 12 dates. The event page, its structured data and the add-to-calendar links describe the first date.

New dates appear as the hourly check (`reconcile`) rolls the window forward, and right away when you save the event.

### Repeating events page

The plugin adds a **Repeating events** page to the admin. It lists every repeating event with a summary of its rule and its next 10 dates, each normal, skipped or changed. **Skip**, **Change time** and **Restore** and the **Duplicate event...** button fill in the inputs of a run. Two limits of the admin shape this:

- A page can only fill in a run's inputs; it cannot start the run. Where the admin has no run form for the page, the page shows what to enter instead: open the Runs tab, pick **Edit repeat dates or duplicate**, enter the values shown and press Start.
- Nothing is written directly. The run stages the change in **Changes**, and the dates change once you review and publish it. Only the site owner can start a run.

The admin can start only the first run a plugin declares, so there is one run, `edit_dates` (**Edit repeat dates or duplicate**). Its `action` says what it does:

- `skip`, `change` or `restore` a single date. Inputs: `event` (the event's slug), `date` (`2026-03-03`), and for `change` a `start_at` and optional `end_at` (`18:30` or `2026-03-03T18:30`).
- `duplicate`: inputs `event` and `shift_days` (0 by default); `date` is not used. It stages a copy named "<name> (copy)" with its dates moved, as a draft you publish in Changes. Where the original came from (source fields) is not copied.

A run can only change an event that the plugin itself created, and the plugin cannot see who owns an event. So the page can change single dates of events made by the `duplicate` action; for any other event (made by hand in the editor, imported, or handed back) the page shows "Hand this event back in the editor (Who owns this page) to change single dates here, or edit the dates in the event itself." If an event is edited in a way that takes it over, the run fails with a message and changes nothing. The same happens when the event's Source ID points at no event the plugin made. The page offers the buttons only for an event whose `dup-` Source ID no other event carries, so an event copied with core's Duplicate is not treated as the plugin's.

### Series, venue and organizer pages

The plugin serves a page for each of these, at `/p/event-calendar/series/<slug>`, `/p/event-calendar/venue/<slug>` and `/p/event-calendar/organizer/<slug>`. A page shows the details of the series, venue or organizer and the upcoming dates of its events (skipped dates are not listed). The pages are plain HTML, cached for 5 minutes, and answer 404 for an unknown or unpublished slug. The event card links to them.

### Settings

| Setting | Default | Meaning |
|---|---|---|
| Event content type (`schema_slug`) | empty | Empty means automatic: events of `calendar-event` and of an older `event` type are both used. Name one content type to use only that one. |
| Time zone (`time_zone`) | `UTC` | IANA name such as `Europe/London`. An event can carry its own zone. |
| Date format (`date_format`) | `short` | `short` (`Sat 14 Feb`), `us` (`Sat, Feb 14`) or `iso` (`2026-02-14`). |
| Week starts on (`week_starts_on`) | `monday` | `monday` or `sunday`: the first column of the calendar and mini calendar. |
| Show map (`show_map`) | off | Show an OpenStreetMap map in the event card and the map widget. Turning it on looks up venue addresses (see Permissions). |
| Nominatim contact (`geocode_contact`) | empty | An email or web address added to the lookup's User-Agent, as the Nominatim usage policy asks. Use one you control, for example `events@example.org`. |

Set the time zone to your site's zone. The editor stores times without a zone, so the plugin reads them in this zone, shows them in it, and converts them to UTC for the calendar feeds. A time that carries its own offset keeps it. An unknown zone name falls back to UTC.

### The map

The map is off by default, and nothing is requested from OpenStreetMap until you turn it on. With **Show map** on, the event card and the **Event map** widget show a map. The card's map marks the venue, at its latitude and longitude when it has them, otherwise at the position found for its address. The tiles are loaded by the visitor's browser from OpenStreetMap (an image request per tile, so OpenStreetMap sees the visitor's request), with the attribution "Map data from OpenStreetMap contributors" shown under the map and a plain **View on OpenStreetMap** link. Without JavaScript only the link shows.

### Upgrading

- Version 0.4.0 adds a database migration (003) and the repeating events, pages and runs above. The index is cleared and rebuilt, so until the next hourly check the lists read your events directly and a repeating event shows only its first date; its other dates appear after that check. The new admin page and runs need no new permission. No action is needed.
- Version 0.3.0 adds a database migration (002) with new index columns. After the update, the lists and the feed read your events directly until the next hourly check (`reconcile`) rebuilds the index, which then fills the new columns; they may be slightly slower until that check has run. The JSON route behind the calendar, mini calendar, countdown, venue card and map has no such fallback: until the index is ready it answers with no events, so those widgets show only their no-JavaScript content. No action is needed.

### Upgrading from 0.1

- Updating from 0.1.0 needs the site owner to confirm it: the new version asks for another host (`nominatim.openstreetmap.org`) and for the `builder.island` permission. Until the update is confirmed and `db` is granted, the lists and the feed keep reading the events themselves, as 0.1 did; the widgets that use the JSON route stay empty.
- If you saved **Event content type** as `event` in 0.1, that value is still stored and pins the plugin to that one type, so the new **Calendar event** type is ignored. Clear the setting (leave it empty) to use both types.

## Permissions

- `entities.read`: reads the events and the venues, organizers, series and categories they point to.
- `builder.data`: answers the three page-builder data sources.
- The public route `/p/event-calendar/api/events`, which needs no grant and only reads the plugin's own index.
- `settings`: reads the settings above.
- `db`: the plugin's own event index (used by the lists and the feed, and kept up to date when events change, are imported or are removed, plus an hourly check) and the cache of address lookups. Both live in the plugin's own schema.
- `http.fetch`, to `nominatim.openstreetmap.org` only: looks up the position of venue addresses that have no coordinates. It runs only while **Show map** is on, a venue address is looked up when its event is saved or published (or, at the latest, by the hourly check), results are cached, a failed lookup is retried after a day, and it makes at most one request per second (at most 20 lookups per hourly run).
- `builder.island`: runs small scripts on pages that use the calendar, mini calendar, countdown, venue or map widgets, and the Event details widget (which draws the map). Without this grant the widgets still show their seeded content and links, but not the interactive views or the map picture.

Nothing else: no mail, no scripts added to every page, no other hosts.

## Data kept

The plugin keeps three things in its own database schema: the event index (one row per event with its time, name, venue, categories and status, and one row per date of a repeating event, plus a marker for when it was last checked in full and a short list of recently removed event ids), and the address lookup cache (address text, position, when it was looked up). Events themselves stay in your content. Both tables are removed with the plugin's data; the index is rebuilt from your events if it is lost.

## Build

From the repository root: `scripts/build-plugin.sh plugins/event-calendar`.

## Limits

- Lists and the feed read the plugin's index, so they do not depend on how many events the site has; the feed holds at most 500 upcoming events. Until the first complete hourly check of the index has run (or if the plugin has no database access), the plugin reads the events themselves, at most 1000 per request.
- At most 48 items per builder source; at most 500 events per call of the JSON route, which looks at most 400 days ahead in one call.
- Core gives the page builder 500 ms in total for external data. The plugin stops looking up venues, organizers and series after 300 ms; the card then shows what the event itself holds.
- A repeating event keeps its dates from 90 days ago to 18 months ahead, at most 800 per event; older dates drop out of the lists when the event is next saved or checked. The feed holds at most 500 entries, so a daily event can fill it; the dates are read from the index but the details of each event are read once per event. Until the first complete check, a repeating event shows only its first date. The Repeating events page reads up to 50 events and the details of up to 30 of them, and shows 10 dates each.
- No ticketing or payments: only a ticket link. The map is off by default.
- The calendar feeds are cached for 15 minutes.
- The feed holds upcoming events only; past events are available one by one.
- The feed is not filtered by language.

## License

Apache-2.0.
