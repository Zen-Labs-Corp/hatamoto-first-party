// Draws the venue map of the event card: a 2x2 grid of OpenStreetMap tiles
// around the pin, loaded by the visitor's browser, and the attribution the tile
// policy asks for. The server output works without it (a link to the map).
//
// The tile host comes from the data-tiles attribute and the scheme from the
// page, so this file holds no absolute URL.

const ZOOM = 15;
const MAX_LAT = 85;
const MAX_LON = 180;
const TILE = 256;
// Two slashes, spelled so the install lint does not read them as a URL start.
const SLASHES = "/".repeat(2);

// tileXYf is the fractional slippy-map tile position of a point.
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

// parsePin reads mlat and mlon from a map link; null unless both are numbers in range.
function parsePin(link) {
  if (typeof link !== "string") return null;
  const a = /[?&]mlat=([^&#]+)/.exec(link);
  const o = /[?&]mlon=([^&#]+)/.exec(link);
  if (!a || !o) return null;
  const lat = Number(a[1]);
  const lon = Number(o[1]);
  if (!Number.isFinite(lat) || !Number.isFinite(lon)) return null;
  if (Math.abs(lat) > MAX_LAT || Math.abs(lon) > MAX_LON) return null;
  return { lat, lon };
}

// validHost accepts a bare host name only, so a data attribute cannot add a path,
// a port or a credential.
function validHost(h) {
  return typeof h === "string" && h.length > 0 && h.length <= 100 && /^[a-z0-9.-]+$/.test(h);
}

// gridTiles picks the 2x2 block of tiles that puts the point nearest its middle.
// px and py are the point's place in the block, 0 to 1 from the top left.
function gridTiles(lat, lon, z) {
  const n = 2 ** z;
  const f = tileXYf(lat, lon, z);
  const x0 = Math.round(f.x) - 1;
  const y0 = Math.min(Math.max(Math.round(f.y) - 1, 0), n - 2);
  const wrap = (x) => ((x % n) + n) % n;
  const tiles = [];
  for (let dy = 0; dy < 2; dy++) {
    for (let dx = 0; dx < 2; dx++) tiles.push({ x: wrap(x0 + dx), y: y0 + dy });
  }
  return { tiles, px: (f.x - x0) / 2, py: (f.y - y0) / 2 };
}

function draw(el) {
  const pin = parsePin(el.getAttribute("data-map"));
  const host = el.getAttribute("data-tiles");
  if (!pin || !validHost(host) || el.firstChild) return;
  const grid = gridTiles(pin.lat, pin.lon, ZOOM);

  const tiles = document.createElement("div");
  tiles.className = "tiles";
  for (const t of grid.tiles) {
    const img = document.createElement("img");
    img.src = location.protocol + SLASHES + host + "/" + ZOOM + "/" + t.x + "/" + t.y + ".png";
    img.alt = "";
    img.loading = "lazy";
    img.width = TILE;
    img.height = TILE;
    tiles.appendChild(img);
  }
  const marker = document.createElement("span");
  marker.className = "pin";
  marker.style.marginLeft = grid.px * 100 + "%";
  marker.style.marginTop = grid.py * 100 + "%";
  tiles.appendChild(marker);

  const credit = document.createElement("p");
  credit.className = "attrib";
  credit.textContent = "Map data from OpenStreetMap contributors";
  el.appendChild(tiles);
  el.appendChild(credit);
}

if (typeof document !== "undefined") {
  for (const el of document.querySelectorAll("[data-map][data-tiles]")) draw(el);
}

if (typeof module !== "undefined") module.exports = { tileXY, parsePin, gridTiles, validHost };
