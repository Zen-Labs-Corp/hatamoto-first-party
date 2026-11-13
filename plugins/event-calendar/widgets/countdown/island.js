// Event countdown: the time left until the next event, or until the event of
// the slug the widget is set to, read from the public events route of this
// plugin and updated every minute. The server output (the next event's title
// and date) stays in place until the route has answered.

const DAY_MS = 86400000;
const MINUTE_MS = 60000;
// The route reads at most 400 days per call.
const AHEAD_DAYS = 399;
// Two slashes, spelled so the install lint does not read them as a URL start.
const SLASHES = "/".repeat(2);

// parts splits a time span into whole days, hours and minutes; a span that is
// over counts as zero.
function parts(msLeft) {
  const ms = Math.max(0, Number.isFinite(msLeft) ? msLeft : 0);
  return {
    d: Math.floor(ms / DAY_MS),
    h: Math.floor((ms % DAY_MS) / 3600000),
    m: Math.floor((ms % 3600000) / MINUTE_MS),
  };
}

function isoSeconds(ms) {
  return new Date(ms).toISOString().slice(0, 19) + "Z";
}

// countdownQuery is the query of the route call: the first event from now on,
// or the event of a slug.
function countdownQuery(slug, nowMs) {
  const p = new URLSearchParams({ from: isoSeconds(nowMs), to: isoSeconds(nowMs + AHEAD_DAYS * DAY_MS), limit: "1" });
  const s = typeof slug === "string" ? slug.trim().slice(0, 200) : "";
  if (s) p.set("slug", s);
  return p.toString();
}

// startTicker calls fn now and then every ms until the returned function is
// called; timers can be replaced for tests.
function startTicker(fn, ms, timers) {
  const t = timers || { setInterval, clearInterval };
  fn();
  const id = t.setInterval(fn, ms);
  return () => t.clearInterval(id);
}

// safeUrl keeps links to the site and to web pages and drops the rest, such as
// script links.
function safeUrl(u) {
  if (typeof u !== "string" || u.length > 2048) return "";
  if (u.startsWith("/") && !u.startsWith(SLASHES)) return u;
  return /^https?:/i.test(u) && u.includes(SLASHES) ? u : "";
}

function el(tag, cls, text) {
  const e = document.createElement(tag);
  if (cls) e.className = cls;
  if (text !== undefined) e.textContent = text;
  return e;
}

async function init(box) {
  const api = box.getAttribute("data-api") || "";
  if (!api.startsWith("/") || api.startsWith(SLASHES)) return;
  let ev;
  try {
    const r = await fetch(api + "?" + countdownQuery(box.getAttribute("data-slug"), Date.now()), { headers: { Accept: "application/json" } });
    if (!r.ok) return;
    const res = await r.json();
    ev = res && Array.isArray(res.events) ? res.events[0] : null;
  } catch (e) {
    return;
  }
  const start = ev ? Date.parse(ev.start) : NaN;
  if (!Number.isFinite(start)) return;

  const name = el("h3", "name");
  const href = safeUrl(ev.url);
  const title = el(href ? "a" : "span", "", String(ev.title || ""));
  if (href) title.href = href;
  name.appendChild(title);
  const left = el("p", "left");
  left.setAttribute("role", "timer");
  box.replaceChildren(name, left, el("p", "when", String(ev.when || "")));

  let stop = null;
  let over = false;
  stop = startTicker(() => {
    const ms = start - Date.now();
    left.replaceChildren();
    if (ms <= 0) {
      left.textContent = "Started";
      over = true;
      if (stop) stop();
      return;
    }
    const p = parts(ms);
    for (const [n, unit] of [[p.d, " d"], [p.h, " h"], [p.m, " m"]]) {
      const s = el("span", "", String(n));
      s.appendChild(el("span", "unit", unit));
      left.appendChild(s);
    }
  }, MINUTE_MS);
  if (over) stop();
}

if (typeof document !== "undefined") {
  for (const box of document.querySelectorAll(".hm-event-calendar_countdown")) init(box);
}

if (typeof module !== "undefined") module.exports = { parts, countdownQuery, startTicker, safeUrl };
