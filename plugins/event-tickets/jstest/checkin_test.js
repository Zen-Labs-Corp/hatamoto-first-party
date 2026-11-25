// Run with: node jstest/checkin_test.js
// Tests ui/checkin.js (scan parsing, storage fallback, offline queue). Lives
// outside ui/ because everything under ui/ is served.
"use strict";
const assert = require("node:assert/strict");
const { parseScan, openStore, makeQueue, QUEUE_KEY } = require("../ui/checkin.js");

const reply = (status, obj) => ({ status, body: JSON.stringify(obj) });
const memStorage = () => {
  const m = new Map();
  return { getItem: (k) => (m.has(k) ? m.get(k) : null), setItem: (k, v) => m.set(k, String(v)), removeItem: (k) => m.delete(k) };
};

function harness(send, store) {
  const results = [];
  const timers = [];
  let changes = 0;
  const q = makeQueue({
    send,
    store: store || openStore(() => { throw new Error("no storage"); }),
    onResult: (item, out) => results.push([item.body, out.result]),
    onChange: () => changes++,
    schedule: (fn, ms) => timers.push({ fn, ms }),
  });
  return { q, results, timers, changes: () => changes };
}

(async () => {
  // 1. parseScan
  assert.equal(parseScan("https://x.test/p/event-tickets/ticket/abc123.def?x=1#y"), "abc123.def");
  assert.equal(parseScan("  abc123.def \n"), "abc123.def");
  assert.equal(parseScan("a".repeat(513)), "");

  // 2. storage that throws: memory queue, not persistent
  const mem = openStore(() => { throw new Error("SecurityError"); });
  assert.equal(mem.persistent, false);
  let h = harness(async () => reply(200, { ok: true, result: "ok" }), mem);
  assert.equal(h.q.persistent, false);
  h.q.add("1", { scan: "t1" });
  assert.equal(h.q.size(), 1);
  assert.deepEqual(mem.load().map((i) => i.body.scan), ["t1"]);

  // 3. working storage persists across queues
  const st = memStorage();
  let store = openStore(() => st);
  assert.equal(store.persistent, true);
  h = harness(async () => { throw new Error("offline"); }, store);
  h.q.add("1", { scan: "t1" });
  h.q.add("1", { attendee_id: 9 });
  assert.ok(st.getItem(QUEUE_KEY));
  const h2 = harness(async () => reply(200, { ok: true, result: "ok" }), openStore(() => st));
  assert.equal(h2.q.size(), 2);
  await h2.q.sync();
  assert.equal(h2.q.size(), 0);
  assert.equal(st.getItem(QUEUE_KEY), "[]");

  // 4. offline then online: FIFO, conflicts reported, queue emptied
  let online = false;
  const sent = [];
  const answers = { a: "ok", b: "already", c: "refused" };
  h = harness(async (ev, body) => {
    if (!online) throw new Error("offline");
    sent.push(body.scan);
    return reply(200, { ok: true, result: answers[body.scan] });
  });
  for (const s of ["a", "b", "c"]) h.q.add("1", { scan: s });
  await h.q.sync();
  assert.equal(h.q.size(), 3);
  assert.equal(h.timers.length, 1);
  assert.equal(h.timers[0].ms, 15000);
  await h.q.sync(); // a second failed run does not stack timers
  assert.equal(h.timers.length, 1);
  online = true;
  await h.timers[0].fn();
  assert.deepEqual(sent, ["a", "b", "c"]);
  assert.deepEqual(h.results.map((r) => r[1]), ["ok", "already", "refused"]);
  assert.equal(h.q.size(), 0);

  // 5. a 503 keeps the item and stops the run; 429 too
  for (const status of [503, 429, 0]) {
    const calls = [];
    h = harness(async (ev, body) => { calls.push(body.scan); return reply(status, {}); });
    h.q.add("1", { scan: "x" });
    h.q.add("1", { scan: "y" });
    await h.q.sync();
    assert.deepEqual(calls, ["x"], "status " + status);
    assert.equal(h.q.size(), 2);
    assert.equal(h.results.length, 0);
  }

  // 6. a 400 drops the item as an error and the run goes on
  h = harness(async (ev, body) => (body.scan === "bad" ? reply(400, { error: "bad request" }) : reply(200, { ok: true, result: "ok" })));
  h.q.add("1", { scan: "bad" });
  h.q.add("1", { scan: "good" });
  await h.q.sync();
  assert.deepEqual(h.results.map((r) => r[1]), ["error", "ok"]);
  assert.equal(h.q.size(), 0);

  // 6b. host 403/404 (plugin off or reloading) keep the scan and retry; only 400/413 are final
  for (const status of [403, 404]) {
    const stalls = [];
    let up = false;
    h = harness(async () => (up ? reply(200, { ok: true, result: "ok" }) : reply(status, { error: "x" })));
    h.q.add("1", { scan: "x" });
    await h.q.sync();
    assert.equal(h.q.size(), 1, "kept on " + status);
    assert.equal(h.results.length, 0);
    assert.equal(h.timers.length, 1);
    up = true;
    await h.timers[0].fn();
    assert.deepEqual(h.results.map((r) => r[1]), ["ok"]);
    assert.equal(h.q.size(), 0);
  }
  h = harness(async () => reply(413, {}));
  h.q.add("1", { scan: "big" });
  await h.q.sync();
  assert.deepEqual(h.results.map((r) => r[1]), ["error"]);
  const stalled = [];
  const hs = makeQueue({ send: async () => reply(404, {}), store: openStore(() => { throw new Error(); }), onStall: (s) => stalled.push(s), schedule() {} });
  hs.add("1", { scan: "x" });
  await hs.sync();
  assert.deepEqual(stalled, [404]);

  // 6c. reply lost after the server committed: the retry's "already" is this scan's own
  const checkedAt = new Date(Date.now() + 5000).toISOString();
  const att = { id: 1, checked_in_at: checkedAt };
  let first = true;
  h = harness(async () => {
    if (first) { first = false; throw new Error("reply lost"); }
    return reply(200, { ok: true, result: "already", attendee: att });
  });
  h.q.add("1", { scan: "t" });
  await h.q.sync();
  await h.timers[0].fn();
  assert.deepEqual(h.results.map((r) => r[1]), ["ok"], "retried item, check-in not before the scan");
  // an earlier check-in (someone else's) stays "already", also on retry
  first = true;
  att.checked_in_at = new Date(Date.now() - 3600000).toISOString();
  h = harness(async () => {
    if (first) { first = false; throw new Error("reply lost"); }
    return reply(200, { ok: true, result: "already", attendee: att });
  });
  h.q.add("1", { scan: "t" });
  await h.q.sync();
  await h.timers[0].fn();
  assert.deepEqual(h.results.map((r) => r[1]), ["already"]);
  // a first attempt is never rewritten
  h = harness(async () => reply(200, { ok: true, result: "already", attendee: { checked_in_at: new Date(Date.now() + 5000).toISOString() } }));
  h.q.add("1", { scan: "t" });
  await h.q.sync();
  assert.deepEqual(h.results.map((r) => r[1]), ["already"]);

  // 7. duplicates are ignored; remove() undoes a queued scan
  h = harness(async () => { throw new Error("offline"); });
  assert.equal(h.q.add("1", { scan: "t" }), true);
  assert.equal(h.q.add("1", { scan: "t" }), false);
  assert.equal(h.q.add("2", { scan: "t" }), true);
  assert.equal(h.q.size(), 2);
  h.q.remove(h.q.items()[0].key);
  assert.equal(h.q.size(), 1);

  console.log("checkin ok");
})().catch((e) => { console.error(e); process.exit(1); });
