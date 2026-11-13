// Run with: node jstest/mini-calendar_test.js
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const m = require("../widgets/mini-calendar/island.js");

// grid
let g = m.monthGrid(2026, 2, "monday");
assert.equal(g.length, 6);
assert.ok(g.every((r) => r.length === 7));
assert.equal(g[0][0].day, "2026-01-26");
assert.equal(g[0][6].day, "2026-02-01");
assert.equal(g.flat().filter((x) => x.inMonth).length, 28);
g = m.monthGrid(2026, 2, "sunday");
assert.equal(g[0][0].day, "2026-02-01");
assert.equal(m.monthGrid(2024, 2, "monday").flat().filter((x) => x.inMonth).length, 29);
assert.equal(m.monthGrid(2026, 12, "sunday").flat().at(-1).day, "2027-01-09");

// month range and navigation
assert.deepEqual(m.monthRange("2026-02-14"), { from: "2026-02-01", to: "2026-03-01" });
assert.deepEqual(m.monthRange("2026-12-31"), { from: "2026-12-01", to: "2027-01-01" });
assert.equal(m.shiftMonth("2026-01-31", 1), "2026-02-28");
assert.equal(m.shiftMonth("2026-01-15", -1), "2025-12-15");

// day marking
const marks = m.markedDays([
  { day: "2026-02-13", end_day: "2026-02-15" },
  { day: "2026-02-14", end_day: "2026-02-14" },
  { day: "2026-02-20" },
  { day: "bad", end_day: "2026-02-21" },
]);
assert.deepEqual([...marks.entries()], [["2026-02-13", 1], ["2026-02-14", 2], ["2026-02-15", 1], ["2026-02-20", 1]]);

// links into the full calendar
assert.equal(m.dayLink("/events", "2026-02-14"), "/events?view=day&date=2026-02-14");
assert.equal(m.dayLink("/events?x=1", "2026-02-14"), "/events?x=1&view=day&date=2026-02-14");
assert.equal(m.dayLink("/events#cal", "2026-02-14"), "/events?view=day&date=2026-02-14#cal");
assert.equal(m.dayLink("https://example.test/e", "2026-02-14"), "https://example.test/e?view=day&date=2026-02-14");
assert.equal(m.dayLink("", "2026-02-14"), "");
assert.equal(m.dayLink("javascript:alert(1)", "2026-02-14"), "");
assert.equal(m.dayLink("//evil.test/", "2026-02-14"), "");
assert.equal(m.dayLink("/events", "2026-02-30"), "");

const src = fs.readFileSync(path.join(__dirname, "../widgets/mini-calendar/island.js"), "utf8");
assert.ok(Buffer.byteLength(src) <= 32768);
assert.doesNotMatch(src, /https?:\/\//i);
assert.doesNotMatch(src, /["'`]\/\/[A-Za-z0-9]/);

console.log("mini calendar island: ok");
