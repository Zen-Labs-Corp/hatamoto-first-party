// Run with: node jstest/event-card_test.js
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const { tileXY, parsePin, gridTiles, validHost, pinBackground, tileUrl } = require("../widgets/event-card/island.js");

assert.deepEqual(tileXY(0, 0, 1), { x: 1, y: 1 });

const slippy = (lat, lon, z) => {
  const n = 2 ** z;
  const r = (lat * Math.PI) / 180;
  return {
    x: Math.floor(((lon + 180) / 360) * n),
    y: Math.floor(((1 - Math.log(Math.tan(r) + 1 / Math.cos(r)) / Math.PI) / 2) * n),
  };
};
assert.deepEqual(tileXY(51.5, -0.12, 15), slippy(51.5, -0.12, 15));

// the link the card source builds
const link = "https://www.openstreetmap.org/?mlat=53.80000&mlon=-1.55000#map=16/53.80000/-1.55000";
assert.deepEqual(parsePin(link), { lat: 53.8, lon: -1.55 });
for (const junk of [
  "", null, undefined, 42, "https://www.openstreetmap.org/",
  "x?mlat=abc&mlon=1", "x?mlat=1", "x?mlat=86&mlon=1", "x?mlat=1&mlon=181", "x?mlat=Infinity&mlon=1", "x?mlat=&mlon=1",
]) {
  assert.equal(parsePin(junk), null, String(junk));
}

assert.equal(validHost("tile.openstreetmap.org"), true);
for (const bad of ["a/b", "a.b:80", "", "a b", "A.B", "user@host", null, undefined, "a".repeat(101)]) {
  assert.equal(validHost(bad), false, String(bad));
}

// the block of four tiles holds the point near its middle and wraps the world
for (const [lat, lon] of [[53.8, -1.55], [0, 0], [51.5, -0.12], [-33.9, 151.2], [84, 179.99], [0, -179.99]]) {
  const g = gridTiles(lat, lon, 15);
  assert.equal(g.tiles.length, 4);
  assert.ok(g.px >= 0.25 && g.px <= 0.75 && g.py >= 0 && g.py <= 1, `${lat},${lon}: ${g.px},${g.py}`);
  for (const t of g.tiles) assert.ok(t.x >= 0 && t.x < 2 ** 15 && t.y >= 0 && t.y < 2 ** 15);
}

// tiles are always requested over https
assert.equal(tileUrl("tile.openstreetmap.org", 15, { x: 1, y: 2 }), "https://tile.openstreetmap.org/15/1/2.png");

// the pin is a background dot on a square laid over the tiles: it must not sit in
// the tiles grid (it would take rows and push the tiles down) and core refuses
// position values other than static and relative in a pack's CSS
assert.match(pinBackground({ px: 0.5, py: 0.25 }), /^radial-gradient\(circle at 50% 25%, /);
const css = fs.readFileSync(path.join(__dirname, "..", "widgets", "event-card", "widget.css"), "utf8");
const pin = new RegExp("\\.hm-event-calendar_event-card \\.pin\\{([^}]*)\\}").exec(css)[1];
assert.match(pin, /aspect-ratio:1\/1/);
assert.match(pin, /margin-top:-100%/);
// a non-positioned pin paints under the opaque tile images (CSS painting order);
// relative is the one position core allows and lifts it above them
assert.match(pin, /position:relative/);
assert.match(pin, /pointer-events:none/);
assert.doesNotMatch(pin, /grid-area|[^-]top:|left:/);
assert.doesNotMatch(css, /absolute|fixed|sticky/);
const island = fs.readFileSync(path.join(__dirname, "..", "widgets", "event-card", "island.js"), "utf8");
assert.doesNotMatch(island, /marginLeft|marginTop/);
assert.doesNotMatch(island, /tiles\.appendChild\(marker\)/);

// the island is lint-clean: none of the tokens the install scan refuses
const src = fs.readFileSync(path.join(__dirname, "..", "widgets", "event-card", "island.js"), "utf8");
assert.ok(src.length <= 32 * 1024);
const banned = [
  /\beval\s*\(/, /\bnew\s+Function\b/, /\bdocument\s*\.\s*write/, /\bimport\s*\(/, /\bimportScripts\b/,
  /https?:\/\//i, /["'`]\/\/[A-Za-z0-9]/, /["'`]\/\//, /\bfetch\s*\(/, /\binnerHTML\b/, /localStorage|sessionStorage|XMLHttpRequest/,
];
for (const re of banned) assert.ok(!re.test(src), `island.js matches ${re}`);

console.log("island ok");
