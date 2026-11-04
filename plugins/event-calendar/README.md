# event-calendar

Events with dates, venue and ticket link: upcoming and past lists for the page builder, an iCal feed and Event structured data.

It covers what The Events Calendar, Events Manager and Google Calendar Events do on WordPress, on top of a normal Hatamoto content type: you keep editing events like any other content, and the plugin adds the lists, the calendar feeds and the search-engine markup.

## Use

1. Install the plugin and grant the permissions it asks for.
2. Create the event content type. A plugin package cannot ship a content type, so open the content types editor, add a type with the slug `event`, switch to its **JSON** tab (the field picker has no date field) and paste:

```json
{
  "type": "object",
  "required": ["start_at"],
  "properties": {
    "start_at": { "type": "string", "format": "date-time", "title": "Starts" },
    "end_at": { "type": "string", "format": "date-time", "title": "Ends" },
    "all_day": { "type": "boolean", "title": "All day" },
    "venue_name": { "type": "string", "title": "Venue" },
    "venue_address": { "type": "string", "title": "Address" },
    "ticket_url": { "type": "string", "format": "uri", "title": "Ticket link" },
    "description": { "type": "string", "title": "Description" },
    "image": { "type": "string", "title": "Image" }
  }
}
```

   An event needs a start; an event whose start cannot be read is left out everywhere. Only published events are used. If your type has another slug, set it in the plugin settings.

3. In the page builder, bind a list widget to the data source **Event calendar: Upcoming events** or **Event calendar: Past events** (`ext.event-calendar.upcoming`, `ext.event-calendar.past`). Upcoming lists events that are not over yet, soonest first (an event in progress counts as upcoming); past lists the rest, most recent first. No event is in both. Each item has the title, URL, excerpt, image and these attributes: `when`, `start_at`, `end_at`, `all_day`, `venue_name`, `venue_address`, `ticket_url` and `ics_url` (the link to that event's calendar file). When the page has a language, events of that language and events without one are kept.

4. Calendar feeds, for visitors to subscribe to or download:
   - `/p/event-calendar/events.ics`: all upcoming events.
   - `/p/event-calendar/event/<slug>.ics`: one event (past events work too), the `<slug>` being the event's slug.

   An unknown, draft or other-type slug answers 404.

5. In a theme template, `when` renders the date and time as plain text, for example `Sat 14 Feb, 7:00 to 9:30 pm`:

   ```
   {{ plugin_event_calendar_when .Attributes.start_at .Attributes.end_at .Attributes.all_day }}
   ```

   Same-meridiem times are compacted (`7:00 to 9:30 pm`), all-day events read `Sat 7 Mar (all day)`, and the year is added only when it differs from the current year. Bad input renders nothing.

6. Event pages get Event structured data: the plugin sets `@type` to `Event` and adds `startDate`, `endDate`, `eventStatus`, `eventAttendanceMode`, `location`, `offers` (from the ticket link) and `image`, only where the page does not already carry that key.

7. Optional: make the feed reachable at `/events.ics` by adding a redirect from `/events.ics` to `/p/event-calendar/events.ics`.

### Settings

| Setting | Default | Meaning |
|---|---|---|
| Event content type (`schema_slug`) | `event` | Slug of the content type that holds events. |
| Time zone (`time_zone`) | `UTC` | IANA name such as `Europe/London`. |
| Date format (`date_format`) | `short` | `short` (`Sat 14 Feb`), `us` (`Sat, Feb 14`) or `iso` (`2026-02-14`). |

Set the time zone to your site's zone. The editor stores times without a zone, so the plugin reads them in this zone, shows them in it, and converts them to UTC for the calendar feeds. A time that carries its own offset keeps it. An unknown zone name falls back to UTC.

## Permissions

- `entities.read`: the only data source; lists and reads the events of the configured content type.
- `builder.data`: answers the two page-builder data sources.
- `settings`: reads the three settings above.

Nothing else: no network access, no scripts on the site, no database.

## Data kept

None. The plugin stores nothing; it reads published events each time it is asked.

## Build

From the repository root: `scripts/build-plugin.sh plugins/event-calendar`.

## Limits

- At most 1000 events are read per request (5 pages of 200); beyond that the rest is ignored.
- At most 48 items per builder source.
- No recurring events, no maps, no ticketing or payments: only a ticket link.
- The calendar feeds are cached for 15 minutes.
- The feed holds upcoming events only; past events are available one by one.
- The feed is not filtered by language.

## License

Apache-2.0.
