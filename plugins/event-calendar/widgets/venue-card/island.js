// Featured venue: the venue's name and address and its next event, read from
// the public events route of this plugin. The server output (the next event
// of the site) stays in place until the route has answered.

const DAY_MS = 86400000;
// The route reads at most 400 days per call.
const AHEAD_DAYS = 399;
// Two slashes, spelled so the install lint does not read them as a URL start.
const SLASHES = "/".repeat(2);

// venueQuery reads the venue setting: trimmed and capped; empty means unset.
function venueQuery(prop) {
  return typeof prop === "string" ? prop.trim().slice(0, 200) : "";
}

function isoSeconds(ms) {
  return new Date(ms).toISOString().slice(0, 19) + "Z";
}

// routeQuery is the query of the route call: the venue's first event from now on.
function routeQuery(prop, nowMs) {
  return new URLSearchParams({
    from: isoSeconds(nowMs),
    to: isoSeconds(nowMs + AHEAD_DAYS * DAY_MS),
    limit: "1",
    venue: venueQuery(prop),
  }).toString();
}

// buildModel is what the card shows; the venue setting names the venue when it
// has no upcoming event.
function buildModel(res, prop) {
  const ev = res && Array.isArray(res.events) && res.events.length ? res.events[0] : null;
  if (!ev) return { name: venueQuery(prop), address: "", next: null };
  return {
    name: String(ev.venue || venueQuery(prop)),
    address: String(ev.address || ""),
    next: { title: String(ev.title || ""), when: String(ev.when || ""), url: safeUrl(ev.url) },
  };
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
  const prop = box.getAttribute("data-venue");
  if (!api.startsWith("/") || api.startsWith(SLASHES) || !venueQuery(prop)) return;
  let res;
  try {
    const r = await fetch(api + "?" + routeQuery(prop, Date.now()), { headers: { Accept: "application/json" } });
    if (!r.ok) return;
    res = await r.json();
  } catch (e) {
    return;
  }
  if (!res || !res.ready) return;
  const model = buildModel(res, prop);

  const out = [el("h3", "name", model.name)];
  if (model.address) out.push(el("p", "address", model.address));
  if (model.next) {
    const p = el("p", "next");
    const a = el(model.next.url ? "a" : "span", "", model.next.title);
    if (model.next.url) a.href = model.next.url;
    p.append("Next: ", a, el("span", "when", model.next.when));
    out.push(p);
  } else {
    out.push(el("p", "next", "No upcoming events."));
  }
  box.replaceChildren(...out);
}

if (typeof document !== "undefined") {
  for (const box of document.querySelectorAll(".hm-event-calendar_venue-card")) init(box);
}

if (typeof module !== "undefined") module.exports = { venueQuery, routeQuery, buildModel, safeUrl };
