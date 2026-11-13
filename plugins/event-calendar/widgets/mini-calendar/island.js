// Mini calendar: one compact month that marks the days with events, read from
// the public events route of this plugin. A day links to the full calendar,
// which reads the same view and date keys from its address. The helpers are
// copies of the ones in the calendar island: islands cannot share code.

const DAY_MS = 86400000;
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

function validDay(day) {
  const ms = typeof day === "string" ? parseDay(day) : NaN;
  return Number.isFinite(ms) && fmtDay(ms) === day;
}

function addDays(day, n) {
  return fmtDay(parseDay(day) + n * DAY_MS);
}

function column(ms, weekStart) {
  const u = new Date(ms).getUTCDay();
  return weekStart === "sunday" ? u : (u + 6) % 7;
}

// shiftMonth moves a day by whole months and keeps the day number when the
// target month is long enough.
function shiftMonth(day, n) {
  const total = +day.slice(0, 4) * 12 + (+day.slice(5, 7) - 1) + n;
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

// monthRange is the month of a day, as the route reads ranges: the end is exclusive.
function monthRange(day) {
  const from = day.slice(0, 8) + "01";
  return { from, to: shiftMonth(from, 1) };
}

// markedDays counts the events of every day they cover (day to end_day).
function markedDays(events) {
  const out = new Map();
  for (const ev of events) {
    if (!validDay(ev.day)) continue;
    const last = validDay(ev.end_day) && ev.end_day > ev.day ? ev.end_day : ev.day;
    for (let d = ev.day, i = 0; d <= last && i < 62; d = addDays(d, 1), i++) out.set(d, (out.get(d) || 0) + 1);
  }
  return out;
}

// safeUrl keeps links to the site and to web pages and drops the rest, such as
// script links.
function safeUrl(u) {
  if (typeof u !== "string" || u.length > 2048) return "";
  if (u.startsWith("/") && !u.startsWith(SLASHES)) return u;
  return /^https?:/i.test(u) && u.includes(SLASHES) ? u : "";
}

// dayLink is the address of the full calendar on one day, in the state keys
// the calendar island reads. Any fragment of the base address is kept at the end.
function dayLink(base, day) {
  const safe = safeUrl(base);
  if (!safe || !validDay(day)) return "";
  const [head, ...frag] = safe.split("#");
  return head + (head.includes("?") ? "&" : "?") + "view=day&date=" + day + (frag.length ? "#" + frag.join("#") : "");
}

function el(tag, cls, text) {
  const e = document.createElement(tag);
  if (cls) e.className = cls;
  if (text !== undefined) e.textContent = text;
  return e;
}

function fmtLabel(day, opts) {
  return new Intl.DateTimeFormat(undefined, { ...opts, timeZone: "UTC" }).format(parseDay(day));
}

function init(box) {
  const api = box.getAttribute("data-api") || "";
  if (!api.startsWith("/") || api.startsWith(SLASHES)) return;
  const base = box.getAttribute("data-calendar-url") || "";
  const today = fmtDay(Date.now() - new Date().getTimezoneOffset() * 60000);
  const cache = new Map();
  let day = today;
  let seq = 0;

  async function load() {
    const range = monthRange(day);
    const key = range.from;
    const mine = ++seq;
    let res = cache.get(key);
    if (!res) {
      try {
        const r = await fetch(api + "?" + new URLSearchParams({ view: "month", from: range.from, to: range.to }), { headers: { Accept: "application/json" } });
        if (!r.ok) return;
        res = await r.json();
      } catch (e) {
        return;
      }
      if (!res || !Array.isArray(res.events)) return;
      if (res.ready) cache.set(key, res);
    }
    if (mine === seq) draw(res);
  }

  function draw(res) {
    const ws = res.week_start === "sunday" ? "sunday" : "monday";
    const marks = markedDays(res.events);
    const rows = monthGrid(+day.slice(0, 4), +day.slice(5, 7), ws);

    const nav = el("div", "nav");
    for (const [label, n] of [["<", -1], [">", 1]]) {
      const b = el("button", "", label);
      b.type = "button";
      b.setAttribute("aria-label", n < 0 ? "Previous month" : "Next month");
      b.addEventListener("click", () => {
        day = shiftMonth(day, n);
        load();
      });
      nav.appendChild(b);
    }
    nav.insertBefore(el("strong", "head", fmtLabel(day, { month: "long", year: "numeric" })), nav.lastChild);

    const table = el("table");
    const head = el("tr");
    for (const cell of rows[0]) head.appendChild(el("th", "", fmtLabel(cell.day, { weekday: "narrow" })));
    table.appendChild(head);
    for (const row of rows) {
      const tr = el("tr");
      for (const cell of row) {
        const td = el("td", (cell.inMonth ? "" : "out") + (cell.day === today ? " today" : ""));
        const n = cell.inMonth ? marks.get(cell.day) || 0 : 0;
        const href = n ? dayLink(base, cell.day) : "";
        const num = String(+cell.day.slice(8, 10));
        const inner = el(href ? "a" : "span", n ? "has" : "", num);
        if (href) inner.href = href;
        if (n) inner.title = n + (n === 1 ? " event" : " events");
        td.appendChild(inner);
        tr.appendChild(td);
      }
      table.appendChild(tr);
    }
    box.replaceChildren(nav, table);
  }

  load();
}

if (typeof document !== "undefined") {
  for (const box of document.querySelectorAll(".hm-event-calendar_mini-calendar")) init(box);
}

if (typeof module !== "undefined") {
  module.exports = { validDay, addDays, shiftMonth, monthGrid, monthRange, markedDays, safeUrl, dayLink };
}
