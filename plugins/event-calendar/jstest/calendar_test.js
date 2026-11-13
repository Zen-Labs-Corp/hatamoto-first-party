// Run with: node jstest/calendar_test.js
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const c = require("../widgets/calendar/island.js");

const flat = (rows) => rows.flat().map((x) => x.day);

// month grids
let g = c.monthGrid(2026, 2, "monday");
assert.equal(g.length, 6);
assert.ok(g.every((r) => r.length === 7));
assert.equal(g[0][0].day, "2026-01-26"); // 1 Feb 2026 is a Sunday
assert.equal(g[0][0].inMonth, false);
assert.equal(g[0][6].day, "2026-02-01");
assert.equal(g[0][6].inMonth, true);
assert.equal(g[5][6].day, "2026-03-08");
g = c.monthGrid(2026, 2, "sunday");
assert.equal(g[0][0].day, "2026-02-01");
assert.equal(g[5][6].day, "2026-03-14");
g = c.monthGrid(2024, 2, "monday"); // leap year
assert.ok(flat(g).includes("2024-02-29"));
assert.equal(g.flat().filter((x) => x.inMonth).length, 29);
assert.equal(c.monthGrid(2026, 2, "monday").flat().filter((x) => x.inMonth).length, 28);
assert.equal(c.monthGrid(2026, 12, "monday").flat().at(-1).day, "2027-01-10");

// week and day ranges, across a year boundary
assert.deepEqual(c.weekRange("2026-12-31", "monday"), { from: "2026-12-28", to: "2027-01-04" });
assert.deepEqual(c.weekRange("2026-12-31", "sunday"), { from: "2026-12-27", to: "2027-01-03" });
assert.deepEqual(c.weekRange("2026-02-01", "monday"), { from: "2026-01-26", to: "2026-02-02" });
assert.deepEqual(c.dayRange("2026-12-31"), { from: "2026-12-31", to: "2027-01-01" });
assert.deepEqual(c.rangeFor("week", "2026-02-14", "monday"), { from: "2026-02-09", to: "2026-02-16" });
assert.deepEqual(c.rangeFor("day", "2026-02-14", "monday"), { from: "2026-02-14", to: "2026-02-15" });
assert.deepEqual(c.rangeFor("month", "2026-02-14", "monday"), { from: "2026-01-26", to: "2026-03-09" });
assert.deepEqual(c.rangeFor("list", "2026-02-14", "monday"), { from: "2026-02-01", to: "2026-03-01" });
assert.deepEqual(c.rangeFor("map", "2026-12-14", "sunday"), { from: "2026-12-01", to: "2027-01-01" });

// prev and next
assert.equal(c.shiftDate("2026-01-31", "month", 1), "2026-02-28");
assert.equal(c.shiftDate("2026-03-15", "month", -1), "2026-02-15");
assert.equal(c.shiftDate("2026-12-15", "list", 1), "2027-01-15");
assert.equal(c.shiftDate("2026-01-15", "photo", -1), "2025-12-15");
assert.equal(c.shiftDate("2026-12-30", "week", 1), "2027-01-06");
assert.equal(c.shiftDate("2026-03-01", "day", -1), "2026-02-28");

// multi-day events spread over their days
const by = c.eventsByDay([
  { title: "a", day: "2026-02-13", end_day: "2026-02-15" },
  { title: "b", day: "2026-02-14", end_day: "2026-02-14" },
  { title: "c", day: "nonsense", end_day: "" },
  { title: "d", day: "2026-02-20" },
]);
assert.deepEqual([...by.keys()], ["2026-02-13", "2026-02-14", "2026-02-15", "2026-02-20"]);
assert.deepEqual(by.get("2026-02-14").map((e) => e.title), ["a", "b"]);
assert.equal(by.get("2026-02-20").length, 1);

// +N more
const five = [1, 2, 3, 4, 5].map((n) => ({ title: "e" + n }));
assert.deepEqual(c.cellEvents(five, 3).more, 2);
assert.equal(c.cellEvents(five, 3).shown.length, 3);
assert.deepEqual(c.cellEvents(five.slice(0, 2), 3), { shown: five.slice(0, 2), more: 0 });
assert.equal(c.cellEvents(five, 0).shown.length, 1);

// URL state
const today = "2026-02-14";
let st = c.stateFromSearch("", today, "month");
assert.deepEqual(st, { view: "month", date: today, price: "", q: "", category: "", venue: "", city: "" });
assert.equal(c.stateFromSearch("", today, "list").view, "list");
assert.equal(c.stateFromSearch("", today, "bogus").view, "month");
st = c.stateFromSearch("?view=week&date=2026-03-02&q=jazz%20night&category=Music&venue=Old+Mill&city=Leeds&price=free", today, "month");
assert.deepEqual(st, { view: "week", date: "2026-03-02", price: "free", q: "jazz night", category: "Music", venue: "Old Mill", city: "Leeds" });
assert.deepEqual(c.stateFromSearch("?" + c.searchFromState(st, today, "month"), today, "month"), st);
assert.equal(c.searchFromState(c.stateFromSearch("", today, "month"), today, "month"), "");
assert.equal(c.searchFromState({ ...st, view: "month", date: today, q: "", category: "", venue: "", city: "", price: "" }, today, "month"), "");
// hostile values fall back
const bad = c.stateFromSearch("?view=%3Cscript%3E&date=2026-02-30&price=free%27&q=" + "x".repeat(500), today, "month");
assert.equal(bad.view, "month");
assert.equal(bad.date, today);
assert.equal(bad.price, "");
assert.equal(bad.q.length, 200);
assert.equal(c.validDay("2026-02-29"), false);
assert.equal(c.validDay("2024-02-29"), true);
assert.equal(c.validDay(null), false);

// route query and cache key
const range = { from: "2026-01-26", to: "2026-03-09" };
assert.equal(c.apiQuery(range, c.stateFromSearch("", today, "month")), "from=2026-01-26&to=2026-03-09");
assert.equal(
  c.apiQuery(range, { ...st, q: "a&b=c" }),
  "from=2026-01-26&to=2026-03-09&q=a%26b%3Dc&category=Music&venue=Old+Mill&city=Leeds&price=free",
);
assert.notEqual(c.cacheKey(range, st, "monday"), c.cacheKey(range, st, "sunday"));
assert.notEqual(c.cacheKey(range, st, "monday"), c.cacheKey(range, { ...st, city: "Hull" }, "monday"));

// debounce with fake timers
{
  const pending = new Map();
  let next = 1;
  const timers = {
    // like the browser: throws unless called without a receiver
    setTimeout: function (fn, ms) {
      if (this !== undefined && this !== globalThis) throw new TypeError("Illegal invocation");
      pending.set(next, { fn, ms });
      return next++;
    },
    clearTimeout: function (id) {
      if (this !== undefined && this !== globalThis) throw new TypeError("Illegal invocation");
      pending.delete(id);
    },
  };
  const calls = [];
  const d = c.debounce((v) => calls.push(v), 300, timers);
  d("a");
  d("ab");
  d("abc");
  assert.equal(pending.size, 1);
  assert.equal([...pending.values()][0].ms, 300);
  assert.deepEqual(calls, []);
  [...pending.values()][0].fn();
  assert.deepEqual(calls, ["abc"]);
}

// subscribe links
const origin = "https:" + "/".repeat(2) + "example.test";
const links = c.subscribeLinks(origin, { gcal: "G?cid=", outlook: "O?url=" });
assert.equal(links.ical, "webcal://example.test/p/event-calendar/events.ics");
assert.equal(links.google, "G?cid=webcal%3A%2F%2Fexample.test%2Fp%2Fevent-calendar%2Fevents.ics");
assert.equal(links.outlook, "O?url=https%3A%2F%2Fexample.test%2Fp%2Fevent-calendar%2Fevents.ics");
assert.equal(c.subscribeLinks(origin, {}).google.startsWith("webcal%3A"), true);

// links from event data: site paths and web pages only
assert.equal(c.safeUrl("/events/jazz"), "/events/jazz");
assert.equal(c.safeUrl("https://example.test/e"), "https://example.test/e");
assert.equal(c.safeUrl("javascript:alert(1)"), "");
assert.equal(c.safeUrl("data:text/html,x"), "");
assert.equal(c.safeUrl("//evil.test/x"), "");
assert.equal(c.safeUrl("https:evil"), "");
assert.equal(c.safeUrl(5), "");

// only the newest load may draw: February, Next (slow March), Prev (February
// from the cache); the March answer arrives last and is dropped
const seq = c.latest();
const march = seq.next();
const febAgain = seq.next();
assert.equal(seq.current(march), false);
assert.equal(seq.current(febAgain), true);

// the island file itself
const src = fs.readFileSync(path.join(__dirname, "../widgets/calendar/island.js"), "utf8");
assert.ok(Buffer.byteLength(src) <= 32768, "island.js over 32768 bytes");
assert.match(src, /if \(!seq\.current\(token\)\) return;\n\s+if \(!res \|\|/, "load must drop a stale response before it draws");
assert.match(src, /let res = cache\.get\(key\);\n\s+if \(ctl\) ctl\.abort\(\);/, "a cache hit must abort the pending request");
assert.doesNotMatch(src, /https?:\/\//i);
assert.doesNotMatch(src, /["'`]\/\/[A-Za-z0-9]/);
assert.doesNotMatch(src, /\beval\s*\(|\bnew\s+Function\b|\bdocument\s*\.\s*write|\bimport\s*\(|\bimportScripts\b/);

console.log("calendar island: ok");
