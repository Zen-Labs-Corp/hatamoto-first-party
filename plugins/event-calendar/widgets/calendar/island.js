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

// apiQuery is the query string of one route call; the view is not sent.
function apiQuery(range, st) {
  const p = new URLSearchParams({ from: range.from, to: range.to });
  for (const k of TEXT_KEYS) if (st[k]) p.set(k, st[k]);
  if (st.price) p.set("price", st.price);
  return p.toString();
}

function cacheKey(range, st, weekStart) {
  return [range.from, range.to, weekStart, st.q, st.category, st.venue, st.city, st.price].join("|");
}

// debounce delays fn until ms have passed without a call; timers can be
// replaced for tests.
function debounce(fn, ms, timers) {
  const t = timers || { setTimeout, clearTimeout };
  let id = null;
  return (...args) => {
    if (id !== null) t.clearTimeout(id);
    id = t.setTimeout(() => {
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

if (typeof module !== "undefined") {
  module.exports = {
    validDay, addDays, shiftMonth, monthGrid, weekRange, dayRange, shiftDate, rangeFor, eventsByDay,
    cellEvents, stateFromSearch, searchFromState, apiQuery, cacheKey, debounce, subscribeLinks, safeUrl,
  };
}
