// Run with: node jstest/countdown_test.js
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const c = require("../widgets/countdown/island.js");

const MIN = 60000;
const HOUR = 60 * MIN;
const DAY = 24 * HOUR;

// parts, including the boundaries
assert.deepEqual(c.parts(3 * DAY + 4 * HOUR + 12 * MIN), { d: 3, h: 4, m: 12 });
assert.deepEqual(c.parts(0), { d: 0, h: 0, m: 0 });
assert.deepEqual(c.parts(MIN - 1), { d: 0, h: 0, m: 0 });
assert.deepEqual(c.parts(MIN), { d: 0, h: 0, m: 1 });
assert.deepEqual(c.parts(HOUR - 1), { d: 0, h: 0, m: 59 });
assert.deepEqual(c.parts(HOUR), { d: 0, h: 1, m: 0 });
assert.deepEqual(c.parts(DAY - 1), { d: 0, h: 23, m: 59 });
assert.deepEqual(c.parts(DAY), { d: 1, h: 0, m: 0 });
assert.deepEqual(c.parts(-5 * DAY), { d: 0, h: 0, m: 0 });
assert.deepEqual(c.parts(NaN), { d: 0, h: 0, m: 0 });

// route query
const now = Date.UTC(2026, 1, 14, 12, 30, 15, 999);
assert.equal(c.countdownQuery("", now), "from=2026-02-14T12%3A30%3A15Z&to=2027-03-20T12%3A30%3A15Z&limit=1&upcoming=1");
assert.equal(
  c.countdownQuery(" spring-jazz ", now),
  "from=2026-02-14T12%3A30%3A15Z&to=2027-03-20T12%3A30%3A15Z&limit=1&slug=spring-jazz",
);
assert.equal(new URLSearchParams(c.countdownQuery("x".repeat(500), now)).get("slug").length, 200);
assert.equal(new URLSearchParams(c.countdownQuery(null, now)).has("slug"), false);
// the range stays under the 400 days the route accepts
const q = new URLSearchParams(c.countdownQuery("", now));
assert.ok((Date.parse(q.get("to")) - Date.parse(q.get("from"))) / DAY <= 400);

// ticker with injected timers
{
  const log = [];
  let fn = null;
  const timers = {
    setInterval: (f, ms) => ((fn = f), log.push("set " + ms), 7),
    clearInterval: (id) => log.push("clear " + id),
  };
  let ticks = 0;
  const stop = c.startTicker(() => ticks++, 60000, timers);
  assert.equal(ticks, 1);
  fn();
  fn();
  assert.equal(ticks, 3);
  stop();
  assert.deepEqual(log, ["set 60000", "clear 7"]);
}

assert.equal(c.safeUrl("/e/1"), "/e/1");
assert.equal(c.safeUrl("javascript:x"), "");
assert.equal(c.safeUrl("//evil.test"), "");

const src = fs.readFileSync(path.join(__dirname, "../widgets/countdown/island.js"), "utf8");
assert.ok(Buffer.byteLength(src) <= 32768);
assert.doesNotMatch(src, /https?:\/\//i);
assert.doesNotMatch(src, /["'`]\/\/[A-Za-z0-9]/);

console.log("countdown island: ok");
