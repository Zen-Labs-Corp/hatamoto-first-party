// Run with: node jstest/venue-card_test.js
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const v = require("../widgets/venue-card/island.js");

// query building
assert.equal(v.venueQuery("  the-old-mill "), "the-old-mill");
assert.equal(v.venueQuery(""), "");
assert.equal(v.venueQuery(null), "");
assert.equal(v.venueQuery("x".repeat(300)).length, 200);
const now = Date.UTC(2026, 1, 14, 12, 30, 15);
assert.equal(
  v.routeQuery("The Old Mill", now),
  "from=2026-02-14T12%3A30%3A15Z&to=2027-03-20T12%3A30%3A15Z&limit=1&venue=The+Old+Mill",
);
assert.equal(new URLSearchParams(v.routeQuery("a&limit=500", now)).get("venue"), "a&limit=500");
assert.equal(new URLSearchParams(v.routeQuery("a&limit=500", now)).get("limit"), "1");

// model with an event
const res = {
  ready: true,
  events: [{ title: "Jazz <b>Night</b>", when: "Sat 14 Feb, 7:00 pm", venue: "The Old Mill", address: "12 High St, Leeds", url: "/events/jazz" }],
};
assert.deepEqual(v.buildModel(res, "old-mill"), {
  name: "The Old Mill",
  address: "12 High St, Leeds",
  next: { title: "Jazz <b>Night</b>", when: "Sat 14 Feb, 7:00 pm", url: "/events/jazz" },
});
// hostile link dropped, title kept as plain text
assert.equal(v.buildModel({ events: [{ title: "x", url: "javascript:alert(1)" }] }, "m").next.url, "");
// model without events, or with nothing usable
assert.deepEqual(v.buildModel({ ready: true, events: [] }, " Old Mill "), { name: "Old Mill", address: "", next: null });
assert.deepEqual(v.buildModel(null, "Old Mill"), { name: "Old Mill", address: "", next: null });
assert.equal(v.buildModel({ events: [{ title: "x" }] }, "slug").name, "slug");

const src = fs.readFileSync(path.join(__dirname, "../widgets/venue-card/island.js"), "utf8");
assert.ok(Buffer.byteLength(src) <= 32768);
assert.doesNotMatch(src, /https?:\/\//i);
assert.doesNotMatch(src, /["'`]\/\/[A-Za-z0-9]/);

console.log("venue card island: ok");
