// Run with: node jstest/map_test.js
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const m = require("../widgets/map/island.js");

// tileXY
assert.deepEqual(m.tileXY(0, 0, 1), { x: 1, y: 1 });
assert.deepEqual(m.tileXY(0, 0, 0), { x: 0, y: 0 });
// the slippy-map formula with sinh, written another way than the island
const slippy = (lat, lon, z) => {
  const n = 2 ** z;
  const r = (lat * Math.PI) / 180;
  return { x: Math.floor(((lon + 180) / 360) * n), y: Math.floor(((1 - Math.asinh(Math.tan(r)) / Math.PI) / 2) * n) };
};
for (const [lat, lon, z] of [[51.5, -0.12, 15], [-33.86, 151.21, 10], [40.7, -74, 3], [0.1, 0.1, 16]]) {
  assert.deepEqual(m.tileXY(lat, lon, z), slippy(lat, lon, z));
}
assert.deepEqual(m.tileXY(51.5, -0.12, 15), { x: 16373, y: 10897 });

// bounds
assert.deepEqual(m.boundsOf([{ lat: 1, lon: 2 }, { lat: -3, lon: 8 }]), { minLat: -3, maxLat: 1, minLon: 2, maxLon: 8 });

// zoom
const one = [{ lat: 51.5, lon: -0.12 }];
assert.equal(m.pickZoom(one, 600, 360), 14);
assert.equal(m.pickZoom([], 600, 360), 14);
const near = [{ lat: 51.5, lon: -0.12 }, { lat: 51.51, lon: -0.1 }];
const far = [{ lat: 51.5, lon: -0.12 }, { lat: 40.7, lon: -74 }];
const zNear = m.pickZoom(near, 600, 360);
const zFar = m.pickZoom(far, 600, 360);
assert.ok(zNear > zFar, `near ${zNear} far ${zFar}`);
assert.ok(zNear <= 16 && zFar >= 1);
const wide = [{ lat: 60, lon: -170 }, { lat: -60, lon: 170 }];
assert.ok(m.pickZoom(wide, 600, 360) <= 2);

// projection stays inside the box
for (const pts of [near, far, one, wide]) {
  const z = m.pickZoom(pts, 600, 360);
  const v = m.project(pts, z, 600, 360);
  assert.equal(v.points.length, pts.length);
  for (const p of v.points) {
    assert.ok(p.x >= 0 && p.x <= 600, `x ${p.x}`);
    assert.ok(p.y >= 0 && p.y <= 360, `y ${p.y}`);
  }
}
const mid = m.project(one, 14, 600, 360).points[0];
assert.ok(Math.abs(mid.x - 300) < 1e-6 && Math.abs(mid.y - 180) < 1e-6);

// tiles cover the box and stay in range
const view = m.project(far, m.pickZoom(far, 600, 360), 600, 360);
const tiles = m.tilesFor(view, 600, 360);
assert.ok(tiles.length >= 1);
const n = 2 ** view.z;
for (const t of tiles) {
  assert.ok(t.x >= 0 && t.x < n && t.y >= 0 && t.y < n);
  assert.ok(t.left > -256 && t.left < 600 && t.top > -256 && t.top < 360);
}
assert.ok(Math.min(...tiles.map((t) => t.left)) <= 0);
assert.ok(Math.max(...tiles.map((t) => t.left)) + 256 >= 600);
// tiles above the top edge of the world are dropped
assert.ok(m.tilesFor({ z: 1, left: 0, top: -300 }, 256, 256).every((t) => t.y >= 0));

// host and tile address
assert.equal(m.validHost("tile.openstreetmap.org"), true);
for (const bad of ["a/b", "a:80", "u@h", "", "x y", null, "a".repeat(101)]) assert.equal(m.validHost(bad), false, String(bad));
assert.equal(m.tileUrl("tile.example.test", 5, 3, 4), "https://tile.example.test/5/3/4.png");

// route query
const now = Date.UTC(2026, 1, 14, 12);
assert.equal(m.routeQuery("", now), "view=map&from=2026-02-14&to=2026-05-15");
assert.equal(
  m.routeQuery("?category=Music&price=free&city=Leeds&q=a+b&view=day&date=2026-01-01&x=1", now),
  "view=map&from=2026-02-14&to=2026-05-15&category=Music&city=Leeds&price=free&q=a+b",
);
assert.equal(m.routeQuery("?price=weird", now), "view=map&from=2026-02-14&to=2026-05-15");
assert.equal(new URLSearchParams(m.routeQuery("?q=" + "x".repeat(300), now)).get("q").length, 200);

// venues
const pts = m.venuePoints([
  { venue: "Mill", lat: 1, lon: 2, title: "a" },
  { venue: "Mill", lat: 1, lon: 2, title: "b" },
  { venue: "Hall", lat: null, lon: null },
  { venue: "Pole", lat: 89, lon: 0 },
  { venue: "Bar", lat: 3, lon: 4 },
]);
assert.equal(pts.length, 2);
assert.equal(pts[0].events.length, 2);

const src = fs.readFileSync(path.join(__dirname, "../widgets/map/island.js"), "utf8");
assert.ok(Buffer.byteLength(src) <= 32768);
assert.doesNotMatch(src, /https?:\/\//i);
assert.doesNotMatch(src, /["'`]\/\/[A-Za-z0-9]/);

console.log("map island: ok");
