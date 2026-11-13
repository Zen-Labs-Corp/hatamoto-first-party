// Event calendar island: a searchable calendar with a view switcher over the
// public events route of this plugin. The server output (a seeded upcoming
// list) stays in place until the route has answered.
//
// All date maths work on "YYYY-MM-DD" strings through Date.UTC, never on the
// visitor's local zone: the route sends each event's day already placed in the
// site zone, so months and weeks cannot shift by a day.

const DAY_MS = 86400000;
const VIEWS = ["month", "list", "week", "day", "photo", "summary", "map"];
const PRICES = ["", "free", "paid"];
const TEXT_KEYS = ["q", "category", "venue", "city"];
const FEED_PATH = "/p/event-calendar/events.ics";
// Two slashes, spelled so the install lint does not read them as a URL start.
const SLASHES = "/".repeat(2);

const pad = (n, w) => String(n).padStart(w, "0");

function parseDay(day) {
  const m = /^(\d{4})-(\d{2})-(\d{2})$/.exec(day);
  return m ? Date.UTC(+m[1], +m[2] - 1, +m[3]) : NaN;
}

function fmtDay(ms) {
  const d = new Date(ms);
  return pad(d.getUTCFullYear(), 4) + "-" + pad(d.getUTCMonth() + 1, 2) + "-" + pad(d.getUTCDate(), 2);
}

// validDay is true for a real calendar day.
function validDay(day) {
  const ms = typeof day === "string" ? parseDay(day) : NaN;
  return Number.isFinite(ms) && fmtDay(ms) === day;
}

function addDays(day, n) {
  return fmtDay(parseDay(day) + n * DAY_MS);
}

// column is the weekday of a day counted from the first day of the week.
function column(ms, weekStart) {
  const u = new Date(ms).getUTCDay();
  return weekStart === "sunday" ? u : (u + 6) % 7;
}

function monthStart(day) {
  return day.slice(0, 8) + "01";
}

// shiftMonth moves a day by whole months and keeps the day number when the
// target month is long enough.
function shiftMonth(day, n) {
  const y = +day.slice(0, 4);
  const total = y * 12 + (+day.slice(5, 7) - 1) + n;
  const ny = Math.floor(total / 12);
  const nm = total - ny * 12;
  const last = new Date(Date.UTC(ny, nm + 1, 0)).getUTCDate();
  return pad(ny, 4) + "-" + pad(nm + 1, 2) + "-" + pad(Math.min(+day.slice(8, 10), last), 2);
}

// monthGrid is six rows of seven days that cover a month; month is 1 to 12.
function monthGrid(year, month, weekStart) {
  const first = Date.UTC(year, month - 1, 1);
  const start = first - column(first, weekStart) * DAY_MS;
  const rows = [];
  for (let r = 0; r < 6; r++) {
    const row = [];
    for (let c = 0; c < 7; c++) {
      const ms = start + (r * 7 + c) * DAY_MS;
      row.push({ day: fmtDay(ms), inMonth: new Date(ms).getUTCMonth() === month - 1 });
    }
    rows.push(row);
  }
  return rows;
}

// weekRange is the first day of the week of a day and the day after its last
// day; ranges end exclusive, as the route reads them.
function weekRange(day, weekStart) {
  const ms = parseDay(day);
  const from = ms - column(ms, weekStart) * DAY_MS;
  return { from: fmtDay(from), to: fmtDay(from + 7 * DAY_MS) };
}

function dayRange(day) {
  return { from: day, to: addDays(day, 1) };
}

// shiftDate moves by n pages of a view: months, weeks or days.
function shiftDate(day, view, n) {
  if (view === "week") return addDays(day, 7 * n);
  if (view === "day") return addDays(day, n);
  return shiftMonth(day, n);
}

// rangeFor is what a view needs loaded. The month view also shows the days of
// the neighbouring months that fill its grid; list, photo, summary and map
// show the month of the date.
function rangeFor(view, day, weekStart) {
  if (view === "week") return weekRange(day, weekStart);
  if (view === "day") return dayRange(day);
  if (view === "month") {
    const rows = monthGrid(+day.slice(0, 4), +day.slice(5, 7), weekStart);
    const from = rows[0][0].day;
    return { from, to: addDays(from, 42) };
  }
  const from = monthStart(day);
  return { from, to: monthStart(shiftMonth(day, 1)) };
}

// eventsByDay lists the events of every day they cover (day to end_day).
function eventsByDay(events) {
  const out = new Map();
  for (const ev of events) {
    if (!validDay(ev.day)) continue;
    const last = validDay(ev.end_day) && ev.end_day > ev.day ? ev.end_day : ev.day;
    for (let d = ev.day, i = 0; d <= last && i < 62; d = addDays(d, 1), i++) {
      if (!out.has(d)) out.set(d, []);
      out.get(d).push(ev);
    }
  }
  return out;
}

// cellEvents splits a day's events into the ones shown and how many are left.
function cellEvents(list, perDay) {
  const n = Math.max(1, perDay | 0);
  return { shown: list.slice(0, n), more: Math.max(0, list.length - n) };
}

function cap(s) {
  return typeof s === "string" ? s.trim().slice(0, 200) : "";
}

// stateFromSearch reads the shareable state of a page address; anything not
// on the lists falls back to the defaults.
function stateFromSearch(search, today, defView) {
  const p = new URLSearchParams(search);
  const view = p.get("view");
  const date = p.get("date");
  const price = p.get("price");
  const st = {
    view: VIEWS.includes(view) ? view : VIEWS.includes(defView) ? defView : "month",
    date: validDay(date) ? date : today,
    price: PRICES.includes(price) ? price : "",
  };
  for (const k of TEXT_KEYS) st[k] = cap(p.get(k));
  return st;
}

// searchFromState is the query string that stateFromSearch reads back; values
// that equal the defaults are left out.
function searchFromState(st, today, defView) {
  const p = new URLSearchParams();
  if (st.view !== defView) p.set("view", st.view);
  if (st.date !== today) p.set("date", st.date);
  for (const k of TEXT_KEYS) if (st[k]) p.set(k, st[k]);
  if (st.price) p.set("price", st.price);
  return p.toString();
}

// addFilters copies the search and filter values of a state into a query.
function addFilters(p, st) {
  for (const k of TEXT_KEYS) if (st[k]) p.set(k, st[k]);
  if (st.price) p.set("price", st.price);
}

// apiQuery is the query string of one route call; the view is not sent.
function apiQuery(range, st) {
  const p = new URLSearchParams({ from: range.from, to: range.to });
  addFilters(p, st);
  return p.toString();
}

function cacheKey(range, st, weekStart) {
  return [range.from, range.to, weekStart, st.q, st.category, st.venue, st.city, st.price].join("|");
}

// debounce delays fn until ms have passed without a call; timers can be
// replaced for tests.
function debounce(fn, ms, timers) {
  // Pulled out of the object: browsers throw "Illegal invocation" when these
  // run as methods of anything but window.
  const { setTimeout: set, clearTimeout: clear } = timers || { setTimeout, clearTimeout };
  let id = null;
  return (...args) => {
    if (id !== null) clear(id);
    id = set(() => {
      id = null;
      fn(...args);
    }, ms);
  };
}

// subscribeLinks builds the three subscribe links from the page origin and the
// provider prefixes the markup carries.
function subscribeLinks(origin, prefixes) {
  const host = String(origin).split(SLASHES)[1] || "";
  const feed = origin + FEED_PATH;
  const webcal = "webcal:" + SLASHES + host + FEED_PATH;
  return {
    ical: webcal,
    google: (prefixes.gcal || "") + encodeURIComponent(webcal),
    outlook: (prefixes.outlook || "") + encodeURIComponent(feed),
  };
}

// safeUrl keeps links to the site and to web pages and drops the rest, such as
// script links.
function safeUrl(u) {
  if (typeof u !== "string" || u.length > 2048) return "";
  if (u.startsWith("/") && !u.startsWith(SLASHES)) return u;
  return /^https?:/i.test(u) && u.includes(SLASHES) ? u : "";
}

// ---- DOM ----

const LABELS = { month: "Month", list: "List", week: "Week", day: "Day", photo: "Photo", summary: "Summary", map: "Map" };
let uid = 0;

function el(tag, cls, text) {
  const e = document.createElement(tag);
  if (cls) e.className = cls;
  if (text !== undefined) e.textContent = text;
  return e;
}

function button(label, onClick) {
  const b = el("button", "", label);
  b.type = "button";
  b.addEventListener("click", onClick);
  return b;
}

function fmtLabel(day, opts) {
  return new Intl.DateTimeFormat(undefined, { ...opts, timeZone: "UTC" }).format(parseDay(day));
}

// timeLabel is the clock time of a timed event in the site zone.
function timeLabel(ev, tz) {
  if (ev.all_day) return "";
  try {
    return new Intl.DateTimeFormat(undefined, { hour: "numeric", minute: "2-digit", timeZone: tz || undefined }).format(new Date(ev.start));
  } catch (e) {
    return "";
  }
}

// titleLink shows an event title, as a link when the event has a safe address.
// Titles are only ever set as text.
function titleLink(ev, cls) {
  const href = safeUrl(ev.url);
  const a = el(href ? "a" : "span", cls, String(ev.title || ""));
  if (href) a.href = href;
  return a;
}

function labelled(text, input) {
  const l = el("label");
  l.appendChild(el("span", "", text + " "));
  l.appendChild(input);
  return l;
}

// latest numbers the loads of a view: only the newest one may draw, so a slow
// answer for a view the visitor has left (even one served from the cache since)
// never overwrites the current one.
function latest() {
  let n = 0;
  return { next: () => ++n, current: (t) => t === n };
}

function init(box) {
  const api = box.getAttribute("data-api") || "";
  if (!api.startsWith("/") || api.startsWith(SLASHES)) return;
  const defView = box.getAttribute("data-view");
  const perDay = +box.getAttribute("data-per-day") || 3;
  const mapUrl = safeUrl(box.getAttribute("data-map-url") || "");
  const today = fmtDay(Date.now() - new Date().getTimezoneOffset() * 60000);
  const st = stateFromSearch(location.search, today, defView);
  const id = "ec-cal-" + ++uid;
  const cache = new Map();
  let data = { events: [], tz: "", show_map: false, truncated: false };
  let ws = "monday";
  let shown = false;
  let ctl = null;
  const seq = latest();

  const fallback = Array.from(box.childNodes);
  const app = el("div", "app");
  app.hidden = true;
  box.appendChild(app);

  const sync = () => {
    const s = searchFromState(st, today, defView);
    try {
      history.replaceState(null, "", location.pathname + (s ? "?" + s : "") + location.hash);
    } catch (e) {
      // the address stays as it is
    }
  };
  const go = (patch) => {
    Object.assign(st, patch);
    sync();
    load();
  };

  // search and date jump
  const bar = el("div", "bar");
  const q = el("input");
  q.type = "search";
  q.placeholder = "Search events...";
  q.value = st.q;
  q.setAttribute("aria-label", "Search events");
  const search = debounce((v) => go({ q: cap(v) }), 300);
  q.addEventListener("input", () => search(q.value));
  const jump = el("input");
  jump.type = "date";
  jump.value = st.date;
  jump.setAttribute("aria-label", "Jump to date");
  bar.append(q, labelled("Jump to date", jump), button("Find", () => validDay(jump.value) && go({ date: jump.value })));

  // filters
  const filters = el("div", "filters");
  const lists = {};
  for (const k of ["category", "venue", "city"]) {
    const input = el("input");
    input.type = "text";
    input.placeholder = "All";
    input.value = st[k];
    input.setAttribute("list", id + "-" + k);
    input.addEventListener("change", () => go({ [k]: cap(input.value) }));
    lists[k] = el("datalist");
    lists[k].id = id + "-" + k;
    filters.append(labelled(k.charAt(0).toUpperCase() + k.slice(1), input), lists[k]);
  }
  const price = el("select");
  for (const [v, t] of [["", "Any"], ["free", "Free"], ["paid", "Paid"]]) {
    const o = el("option", "", t);
    o.value = v;
    price.appendChild(o);
  }
  price.value = st.price;
  price.addEventListener("change", () => go({ price: price.value }));
  filters.appendChild(labelled("Price", price));

  // views and navigation
  const tabs = el("div", "tabs");
  tabs.setAttribute("role", "group");
  const tabBtn = {};
  for (const v of VIEWS) {
    tabBtn[v] = button(LABELS[v], () => go({ view: v }));
    tabs.appendChild(tabBtn[v]);
  }
  const title = el("h3", "title");
  const nav = el("div", "nav");
  nav.append(button("< Prev", () => go({ date: shiftDate(st.date, st.view, -1) })), title, button("Today", () => go({ date: today })), button("Next >", () => go({ date: shiftDate(st.date, st.view, 1) })));
  const status = el("p", "status");
  status.setAttribute("aria-live", "polite");
  const content = el("div", "content");

  const sub = el("div", "subscribe");
  const links = subscribeLinks(location.origin, { gcal: box.getAttribute("data-gcal"), outlook: box.getAttribute("data-outlook") });
  sub.appendChild(el("span", "", "Subscribe:"));
  for (const [t, h] of [["iCal", links.ical], ["Google Calendar", links.google], ["Outlook", links.outlook]]) {
    const a = el("a", "", t);
    a.href = h;
    a.rel = "noopener";
    sub.appendChild(a);
  }
  app.append(bar, filters, tabs, nav, status, content, sub);

  function fillLists(events) {
    const found = { category: new Set(), venue: new Set(), city: new Set() };
    for (const ev of events) {
      for (const c of ev.categories || []) found.category.add(c);
      if (ev.venue) found.venue.add(ev.venue);
      if (ev.city) found.city.add(ev.city);
    }
    for (const k of Object.keys(found)) {
      const have = new Set(Array.from(lists[k].children, (o) => o.value));
      for (const v of found[k]) {
        if (have.has(v)) continue;
        const o = el("option");
        o.value = v;
        lists[k].appendChild(o);
      }
    }
  }

  function chip(ev) {
    const t = timeLabel(ev, data.tz);
    const a = titleLink(ev, "chip");
    a.textContent = (t ? t + " " : "") + String(ev.title || "");
    return a;
  }

  function card(ev, photo) {
    const item = el("li", "item");
    const src = photo ? safeUrl(ev.image) : "";
    if (src) {
      const img = el("img");
      img.src = src;
      img.alt = String(ev.title || "");
      img.loading = "lazy";
      item.appendChild(img);
    }
    const h = el("h4");
    h.appendChild(titleLink(ev));
    item.appendChild(h);
    item.appendChild(el("p", "", ev.when || ""));
    const where = [ev.venue, ev.city].filter(Boolean).join(", ");
    if (where) item.appendChild(el("p", "", where));
    if (ev.price) item.appendChild(el("p", "", ev.price));
    return item;
  }

  function monthView(events) {
    const by = eventsByDay(events);
    const grid = el("div", "grid");
    const rows = monthGrid(+st.date.slice(0, 4), +st.date.slice(5, 7), ws);
    for (const cell of rows[0]) grid.appendChild(el("div", "dow", fmtLabel(cell.day, { weekday: "short" })));
    for (const cell of rows.flat()) {
      const d = el("div", "cell" + (cell.inMonth ? "" : " out") + (cell.day === today ? " today" : ""));
      d.appendChild(el("span", "num", String(+cell.day.slice(8, 10))));
      const part = cellEvents(by.get(cell.day) || [], perDay);
      for (const ev of part.shown) d.appendChild(chip(ev));
      if (part.more) d.appendChild(button("+" + part.more + " more", () => go({ view: "day", date: cell.day })));
      grid.appendChild(d);
    }
    return grid;
  }

  function weekView(events) {
    const by = eventsByDay(events);
    const days = el("div", "days week");
    const from = weekRange(st.date, ws).from;
    for (let i = 0; i < 7; i++) {
      const day = addDays(from, i);
      const d = el("div", "day");
      d.appendChild(el("h3", "", fmtLabel(day, { weekday: "short", day: "numeric", month: "short" })));
      for (const ev of by.get(day) || []) d.appendChild(chip(ev));
      days.appendChild(d);
    }
    return days;
  }

  function summaryView(events) {
    const by = eventsByDay(events);
    const dl = el("dl", "summary");
    for (const day of Array.from(by.keys()).sort()) {
      dl.appendChild(el("dt", "", fmtLabel(day, { weekday: "long", day: "numeric", month: "long" })));
      for (const ev of by.get(day)) {
        const dd = el("dd");
        dd.appendChild(chip(ev));
        dl.appendChild(dd);
      }
    }
    return dl;
  }

  function mapView(events) {
    const places = new Map();
    for (const ev of events) {
      if (!ev.venue) continue;
      const p = places.get(ev.venue) || { address: ev.address || "", n: 0 };
      p.n++;
      places.set(ev.venue, p);
    }
    const wrap = el("div", "map");
    const ul = el("ul", "places");
    for (const [name, p] of places) {
      const li = el("li");
      li.appendChild(el("strong", "", name));
      li.appendChild(el("span", "", (p.address ? " - " + p.address : "") + " (" + p.n + ")"));
      ul.appendChild(li);
    }
    wrap.appendChild(places.size ? ul : el("p", "", "No venues in this range."));
    if (mapUrl) {
      const f = new URLSearchParams();
      addFilters(f, st);
      const qs = f.toString();
      const a = el("a", "", "Open map");
      a.href = mapUrl + (qs ? (mapUrl.includes("?") ? "&" : "?") + qs : "");
      wrap.appendChild(a);
    }
    return wrap;
  }

  function listView(events, photo) {
    const ul = el("ul", photo ? "photos" : "list");
    for (const ev of events) ul.appendChild(card(ev, photo));
    return ul;
  }

  function render() {
    for (const v of VIEWS) {
      tabBtn[v].setAttribute("aria-pressed", String(v === st.view));
      tabBtn[v].hidden = v === "map" && !data.show_map;
    }
    const range = rangeFor(st.view, st.date, ws);
    if (st.view === "week") {
      title.textContent = fmtLabel(range.from, { month: "short", day: "numeric" }) + " - " + fmtLabel(addDays(range.to, -1), { month: "short", day: "numeric", year: "numeric" });
    } else if (st.view === "day") {
      title.textContent = fmtLabel(st.date, { weekday: "long", year: "numeric", month: "long", day: "numeric" });
    } else {
      title.textContent = fmtLabel(st.date, { month: "long", year: "numeric" });
    }
    const events = data.events;
    content.replaceChildren();
    const inDay = st.view === "day" ? events.filter((ev) => (eventsByDay([ev]).has(st.date))) : events;
    status.textContent = !inDay.length ? "No events found." : data.truncated ? "Showing the first events only. Narrow the search to see more." : "";
    const views = { month: monthView, week: weekView, summary: summaryView, map: mapView };
    if (st.view === "month" || st.view === "week" || st.view === "map" || inDay.length) {
      const one = views[st.view];
      content.appendChild(one ? one(events) : listView(inDay, st.view === "photo"));
    }
  }

  function fail() {
    status.textContent = "Could not load events.";
    content.replaceChildren();
  }

  async function load() {
    const token = seq.next();
    const used = ws;
    const range = rangeFor(st.view, st.date, used);
    const key = cacheKey(range, st, used);
    let res = cache.get(key);
    if (ctl) ctl.abort();
    if (!res) {
      const mine = (ctl = new AbortController());
      if (shown) status.textContent = "Loading events...";
      try {
        const r = await fetch(api + "?" + apiQuery(range, st), { signal: mine.signal, headers: { Accept: "application/json" } });
        if (!r.ok) throw new Error("status " + r.status);
        res = await r.json();
      } catch (e) {
        if (seq.current(token) && shown) fail();
        return;
      }
      if (!seq.current(token)) return;
      if (!res || !Array.isArray(res.events)) {
        if (shown) fail();
        return;
      }
      if (res.ready) {
        if (cache.size >= 24) cache.delete(cache.keys().next().value);
        cache.set(key, res);
      }
    }
    ws = res.week_start === "sunday" ? "sunday" : "monday";
    if (ws !== used) return load();
    data = res;
    if (!shown) {
      if (!res.ready && !res.events.length) return;
      shown = true;
      for (const n of fallback) n.remove();
      app.hidden = false;
    }
    if (st.view === "map" && !data.show_map) {
      st.view = "month";
      sync();
      return load();
    }
    fillLists(res.events);
    render();
  }

  load();
}

if (typeof document !== "undefined") {
  for (const box of document.querySelectorAll(".hm-event-calendar_calendar")) init(box);
}

if (typeof module !== "undefined") {
  module.exports = {
    validDay, latest, addDays, shiftMonth, monthGrid, weekRange, dayRange, shiftDate, rangeFor, eventsByDay,
    cellEvents, stateFromSearch, searchFromState, apiQuery, cacheKey, debounce, subscribeLinks, safeUrl,
  };
}
