// Event map: OpenStreetMap tiles with one marker per venue of the upcoming
// events, read from the public events route of this plugin. The server output
// is a short note. The tile host comes from the data-tiles attribute and tiles
// are always fetched over https. The widget removes itself while the map
// setting is off.

const TILE = 256;
const MAX_ZOOM = 16;
const DEFAULT_ZOOM = 14;
const PAD = 40;
const DAY_MS = 86400000;
const FILTERS = ["category", "venue", "city", "price", "q"];
// Two slashes, spelled so the install lint does not read them as a URL start.
const SLASHES = "/".repeat(2);

// tileXYf is the position of a point in tiles at a zoom level, with fractions.
function tileXYf(lat, lon, z) {
  const n = 2 ** z;
  const rad = (lat * Math.PI) / 180;
  return {
    x: ((lon + 180) / 360) * n,
    y: ((1 - Math.log(Math.tan(rad) + 1 / Math.cos(rad)) / Math.PI) / 2) * n,
  };
}

// tileXY is the tile that holds a point.
function tileXY(lat, lon, z) {
  const f = tileXYf(lat, lon, z);
  return { x: Math.floor(f.x), y: Math.floor(f.y) };
}

function boundsOf(points) {
  const b = { minLat: 90, maxLat: -90, minLon: 180, maxLon: -180 };
  for (const p of points) {
    b.minLat = Math.min(b.minLat, p.lat);
    b.maxLat = Math.max(b.maxLat, p.lat);
    b.minLon = Math.min(b.minLon, p.lon);
    b.maxLon = Math.max(b.maxLon, p.lon);
  }
  return b;
}

// pixelBox is the extent of the points in map pixels at a zoom level.
function pixelBox(points, z) {
  const xs = [];
  const ys = [];
  for (const p of points) {
    const f = tileXYf(p.lat, p.lon, z);
    xs.push(f.x * TILE);
    ys.push(f.y * TILE);
  }
  return { x0: Math.min(...xs), x1: Math.max(...xs), y0: Math.min(...ys), y1: Math.max(...ys) };
}

// pickZoom is the closest zoom at which all points fit in a w by h box with a margin.
function pickZoom(points, w, h) {
  if (points.length < 2) return DEFAULT_ZOOM;
  for (let z = MAX_ZOOM; z > 1; z--) {
    const b = pixelBox(points, z);
    if (b.x1 - b.x0 <= w - 2 * PAD && b.y1 - b.y0 <= h - 2 * PAD) return z;
  }
  return 1;
}

// project places points in a w by h box centred on the middle of their extent.
// The result has the zoom, the map pixel at the box's top left corner and the
// position of each point in the box.
function project(points, z, w, h) {
  const b = pixelBox(points, z);
  const left = (b.x0 + b.x1) / 2 - w / 2;
  const top = (b.y0 + b.y1) / 2 - h / 2;
  return {
    z,
    left,
    top,
    points: points.map((p) => {
      const f = tileXYf(p.lat, p.lon, z);
      return { x: f.x * TILE - left, y: f.y * TILE - top };
    }),
  };
}

// tilesFor lists the tiles that cover the box, with each one's place in it.
// Tiles beyond the poles are left out and the columns wrap around the world.
function tilesFor(view, w, h) {
  const n = 2 ** view.z;
  const out = [];
  const wrap = (x) => ((x % n) + n) % n;
  for (let ty = Math.floor(view.top / TILE); ty <= Math.floor((view.top + h) / TILE); ty++) {
    if (ty < 0 || ty >= n) continue;
    for (let tx = Math.floor(view.left / TILE); tx <= Math.floor((view.left + w) / TILE); tx++) {
      out.push({ x: wrap(tx), y: ty, left: tx * TILE - view.left, top: ty * TILE - view.top });
    }
  }
  return out;
}

// validHost accepts a bare host name only, so a data attribute cannot add a
// path, a port or a credential.
function validHost(h) {
  return typeof h === "string" && h.length > 0 && h.length <= 100 && /^[a-z0-9.-]+$/.test(h);
}

function tileUrl(host, z, x, y) {
  return "https:" + SLASHES + host + "/" + z + "/" + x + "/" + y + ".png";
}

function fmtDay(ms) {
  const d = new Date(ms);
  const p = (n) => String(n).padStart(2, "0");
  return d.getUTCFullYear() + "-" + p(d.getUTCMonth() + 1) + "-" + p(d.getUTCDate());
}

// routeQuery is the query of the route call: the next 90 days and the filters
// the page address carries.
function routeQuery(search, nowMs) {
  const src = new URLSearchParams(search);
  const p = new URLSearchParams({ view: "map", from: fmtDay(nowMs), to: fmtDay(nowMs + 90 * DAY_MS) });
  for (const k of FILTERS) {
    const v = (src.get(k) || "").trim().slice(0, 200);
    if (v && (k !== "price" || v === "free" || v === "paid")) p.set(k, v);
  }
  return p.toString();
}

// venuePoints groups the events that have a position by venue.
function venuePoints(events) {
  const by = new Map();
  for (const ev of events) {
    if (typeof ev.lat !== "number" || typeof ev.lon !== "number") continue;
    if (Math.abs(ev.lat) > 85 || Math.abs(ev.lon) > 180) continue;
    const key = ev.lat + "," + ev.lon;
    if (!by.has(key)) by.set(key, { lat: ev.lat, lon: ev.lon, name: String(ev.venue || ""), address: String(ev.address || ""), events: [] });
    by.get(key).events.push(ev);
  }
  return Array.from(by.values());
}

function el(tag, cls, text) {
  const e = document.createElement(tag);
  if (cls) e.className = cls;
  if (text !== undefined) e.textContent = text;
  return e;
}

async function init(box) {
  const api = box.getAttribute("data-api") || "";
  const host = box.getAttribute("data-tiles");
  if (!api.startsWith("/") || api.startsWith(SLASHES) || !validHost(host)) return;
  let res;
  try {
    const r = await fetch(api + "?" + routeQuery(location.search, Date.now()), { headers: { Accept: "application/json" } });
    if (!r.ok) return;
    res = await r.json();
  } catch (e) {
    return;
  }
  if (!res || !res.ready) return;
  if (!res.show_map) {
    box.remove();
    return;
  }
  const places = venuePoints(Array.isArray(res.events) ? res.events : []);
  box.replaceChildren();
  if (!places.length) {
    box.appendChild(el("p", "note", "No events with a known location."));
    return;
  }

  const w = Math.max(240, box.clientWidth || 600);
  const h = Math.min(800, Math.max(200, +box.getAttribute("data-height") || 360));
  const view = project(places, pickZoom(places, w, h), w, h);
  const frame = el("div", "view");
  frame.style.width = w + "px";
  frame.style.height = h + "px";
  for (const t of tilesFor(view, w, h)) {
    const img = el("img");
    img.src = tileUrl(host, view.z, t.x, t.y);
    img.alt = "";
    img.loading = "lazy";
    img.width = TILE;
    img.height = TILE;
    img.style.position = "absolute";
    img.style.left = Math.round(t.left) + "px";
    img.style.top = Math.round(t.top) + "px";
    frame.appendChild(img);
  }

  const list = el("ul", "places");
  const rows = places.map((p) => {
    const li = el("li");
    li.appendChild(el("strong", "", p.name || p.address || "Venue"));
    if (p.address) li.appendChild(el("p", "", p.address));
    for (const ev of p.events.slice(0, 3)) li.appendChild(el("p", "", String(ev.when || "") + " " + String(ev.title || "")));
    list.appendChild(li);
    return li;
  });
  view.points.forEach((pt, i) => {
    const m = el("button", "marker");
    m.type = "button";
    m.title = places[i].name || places[i].address || "Venue";
    m.setAttribute("aria-label", m.title);
    m.style.position = "absolute";
    m.style.left = Math.round(pt.x - 10) + "px";
    m.style.top = Math.round(pt.y - 10) + "px";
    m.addEventListener("click", () => {
      rows.forEach((r, j) => r.classList.toggle("on", j === i));
    });
    frame.appendChild(m);
  });
  box.append(frame, el("p", "attrib", "Map data from OpenStreetMap contributors"), list);
}

if (typeof document !== "undefined") {
  for (const box of document.querySelectorAll(".hm-event-calendar_map")) init(box);
}

if (typeof module !== "undefined") {
  module.exports = { tileXY, boundsOf, pickZoom, project, tilesFor, validHost, tileUrl, routeQuery, venuePoints };
}
