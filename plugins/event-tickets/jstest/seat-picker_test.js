// Run with: node jstest/seat-picker_test.js
const assert = require("node:assert/strict");
const s = require("../widgets/seat-picker/island.js");

class Node {
  constructor(tag) {
    this.tag = tag;
    this.children = [];
    this.attrs = {};
    this.handlers = {};
    this.textContent = "";
    this.className = "";
    this.disabled = false;
    this.hidden = false;
  }
  append(...kids) { this.children.push(...kids); }
  replaceChildren(...kids) { this.children = [...kids]; }
  setAttribute(k, v) { this.attrs[k] = String(v); }
  getAttribute(k) { return k in this.attrs ? this.attrs[k] : null; }
  addEventListener(type, fn) { this.handlers[type] = fn; }
  click() { return this.handlers.click(); }
  all(pred, out = []) {
    if (pred(this)) out.push(this);
    for (const c of this.children) c.all(pred, out);
    return out;
  }
  byClass(cls) { return this.all((n) => n.className.split(" ").includes(cls)); }
  querySelector(sel) { return this.byClass(sel.slice(1))[0] || null; }
}

const AVAIL = {
  ok: true,
  sections: [
    { id: 7, kind: "grid", name: "Stalls", tier: "Std", type_id: 5, price: "10.00 EUR", rows: [{ l: "A", s: "oxob" }, { l: "B", s: "oooo" }] },
    { id: 8, kind: "zone", name: "Standing", tier: "", type_id: 6, price: "5.00 EUR", left: 80 },
  ],
};

function answer(status, body) {
  return { ok: status >= 200 && status < 300, status, json: async () => body };
}

const flush = () => new Promise((r) => setImmediate(r));

// setup mounts a picker with a fake clock, timers and event bus.
function setup(route) {
  const anchor = new Node("div");
  anchor.attrs["data-event"] = "e1";
  const doc = { querySelector: () => anchor, createElement: (t) => new Node(t) };
  const box = new Node("div");
  Object.assign(box.attrs, { "data-seats-api": "/p/event-tickets/seats", "data-token-api": "/p/event-tickets/token", "data-heading": "Pick seats" });
  const note = new Node("p");
  note.className = "note";
  box.append(note);
  const calls = [];
  const events = [];
  const listeners = {};
  const timers = [];
  let t = 1000000;
  const env = {
    doc,
    now: () => t,
    delay: (fn, ms) => timers.push({ fn, at: t + ms }),
    emit: (name, detail) => events.push({ name, detail }),
    on: (name, fn) => { listeners[name] = fn; },
    fetch: async (url, opt = {}) => {
      calls.push({ url, method: opt.method || "GET", body: opt.body ? JSON.parse(opt.body) : null });
      return route(url, opt);
    },
  };
  // advance moves the clock and runs the timers that are due, in order.
  async function advance(ms) {
    const end = t + ms;
    for (;;) {
      timers.sort((a, b) => a.at - b.at);
      if (!timers.length || timers[0].at > end) break;
      const x = timers.shift();
      t = Math.max(t, x.at);
      x.fn();
      await flush();
    }
    t = end;
  }
  return { box, env, calls, events, listeners, advance, note };
}

const seatButtons = (p) => p.map.byClass("seat");

async function main() {
  // render: STAGE, heading, rows, states, zone text, labels
  {
    const h = setup(async (url) => (url.includes("/seats/e1") ? answer(200, AVAIL) : answer(404, {})));
    const p = s.mount(h.box, h.env);
    await flush();
    assert.equal(h.box.hidden, false);
    assert.equal(h.note.hidden, true);
    assert.equal(h.box.byClass("stage")[0].textContent, "STAGE");
    assert.equal(h.box.byClass("legend")[0].textContent, "o free   x taken   * yours");
    assert.equal(h.calls[0].url, "/p/event-tickets/seats/e1");
    const labels = seatButtons(p).map((b) => b.attrs["aria-label"]);
    assert.equal(labels[0], "Row A seat 1, free");
    assert.equal(labels[1], "Row A seat 2, taken");
    assert.equal(labels[3], "Row A seat 4, not available");
    assert.equal(seatButtons(p)[3].className, "seat blocked gap");
    assert.equal(seatButtons(p)[1].disabled, true);
    assert.deepEqual(seatButtons(p).slice(0, 3).map((b) => b.textContent), ["o", "x", "o"]);
    assert.equal(p.map.byClass("section")[0].byClass("row-label")[0].textContent, "A");
    assert.equal(p.map.byClass("zone")[0].textContent, "Standing: 80 left, choose a number below");
    assert.equal(p.go.disabled, true);
    // the first announcement: nothing held, grid types are the seated ones
    assert.deepEqual(h.events.at(-1), { name: "event-tickets:seats", detail: { hold: "", counts: {}, seated: [5] } });
  }

  // a map wider than its box shows the scroll hint and scrolls to the seat just held
  {
    const h = setup(async (url, opt) => {
      if (url.startsWith("/p/event-tickets/token")) return answer(200, { token: "1.ab" });
      if (url.endsWith("/seats/hold") && opt.method === "POST") return answer(200, { ok: true, hold: "H1", seat_id: 41, expires_in: 600 });
      return answer(200, AVAIL);
    });
    const p = s.mount(h.box, h.env);
    await flush();
    const more = h.box.byClass("more")[0];
    assert.equal(more.hidden, true, "fits: no hint");
    p.map.scrollWidth = 672;
    p.map.clientWidth = 356;
    p.render();
    assert.equal(more.hidden, false);
    Node.prototype.offsetLeft = 500; // where the browser would place the seat
    const click = seatButtons(p)[2].click();
    await flush();
    await h.advance(3100);
    await click;
    await flush();
    delete Node.prototype.offsetLeft;
    assert.equal(p.map.byClass("yours").length, 1);
    assert.equal(p.map.scrollLeft, 500 - 356 / 2);
    p.map.scrollWidth = 100;
    p.render();
    assert.equal(more.hidden, true);
  }

  // no event on the page, or no seat map: nothing shown
  {
    const h = setup(async () => answer(404, {}));
    const p = s.mount(h.box, h.env);
    await flush();
    assert.equal(h.box.hidden, true);
    assert.equal(p.map.children.length, 0);
    const none = setup(async () => answer(200, AVAIL));
    none.env.doc.querySelector = () => null;
    assert.equal(s.mount(none.box, none.env), null);
  }

  // hold, yours marking, event, countdown, release
  {
    const h = setup(async (url, opt) => {
      if (url.startsWith("/p/event-tickets/token")) return answer(200, { token: "1.ab" });
      if (url.endsWith("/seats/hold") && opt.method === "POST") return answer(200, { ok: true, hold: "H1", seat_id: 41, label: "Stalls A-1", expires_in: 600 });
      if (opt.method === "DELETE") return answer(200, { ok: true, released: 1 });
      return answer(200, AVAIL);
    });
    const p = s.mount(h.box, h.env);
    await flush();
    const click = seatButtons(p)[0].click();
    await flush();
    await h.advance(3100);
    await click;
    await flush();
    const post = h.calls.find((c) => c.method === "POST");
    assert.deepEqual(post.body, { event: "e1", section: 7, row: 0, num: 1, hold: "", token: "1.ab", website: "" });
    assert.equal(seatButtons(p)[0].textContent, "*");
    assert.equal(seatButtons(p)[0].attrs["aria-label"], "Row A seat 1, yours");
    assert.equal(p.go.disabled, false);
    assert.equal(p.timer.textContent, "Held for you 10:00");
    assert.deepEqual(h.events.at(-1).detail, { hold: "H1", counts: { 5: 1 }, seated: [5] });
    await h.advance(61000);
    assert.equal(p.timer.textContent, "Held for you 08:59");

    // the second seat carries the hold token
    const click2 = seatButtons(p)[2].click();
    await flush();
    await click2;
    assert.equal(h.calls.filter((c) => c.method === "POST").at(-1).body.hold, "H1");

    // continue to payment
    p.go.click();
    assert.equal(h.events.at(-1).name, "event-tickets:pay");

    // hello is answered with the current state
    h.listeners["event-tickets:hello"]();
    assert.equal(h.events.at(-1).name, "event-tickets:seats");
    assert.equal(h.events.at(-1).detail.hold, "H1");

    // release one seat: DELETE with the seat id, no cookies, token in the path
    const un = seatButtons(p)[0].click();
    await un;
    await flush();
    const del = h.calls.find((c) => c.method === "DELETE");
    assert.equal(del.url, "/p/event-tickets/seats/hold/H1?event=e1&seat=41");
    assert.equal(seatButtons(p)[0].textContent, "o");
    assert.equal(p.mine.size, 1);
  }

  // countdown 600 s -> 0 clears the yours marks, says so and refetches
  {
    const h = setup(async (url, opt) => {
      if (url.startsWith("/p/event-tickets/token")) return answer(200, { token: "1.ab" });
      if (opt.method === "POST") return answer(200, { ok: true, hold: "H2", seat_id: 9, label: "Stalls A-1", expires_in: 600 });
      return answer(200, AVAIL);
    });
    const p = s.mount(h.box, h.env);
    await flush();
    const click = seatButtons(p)[0].click();
    await flush();
    await h.advance(3100);
    await click;
    await flush();
    const before = h.calls.length;
    await h.advance(596000);
    assert.equal(p.mine.size, 1);
    await h.advance(4000);
    assert.equal(p.mine.size, 0);
    assert.equal(p.status.textContent, s.ENDED);
    assert.equal(seatButtons(p)[0].textContent, "o");
    assert.deepEqual(h.events.at(-1).detail, { hold: "", counts: {}, seated: [5] });
    assert.ok(h.calls.slice(before).some((c) => c.url === "/p/event-tickets/seats/e1"));
  }

  // 409 marks the seat taken and says so; 429 says full
  {
    let code = 409;
    const h = setup(async (url, opt) => {
      if (url.startsWith("/p/event-tickets/token")) return answer(200, { token: "1.ab" });
      if (opt.method === "POST") return answer(code, { ok: false });
      return answer(200, AVAIL);
    });
    const p = s.mount(h.box, h.env);
    await flush();
    let click = seatButtons(p)[0].click();
    await flush();
    await h.advance(3100);
    await click;
    await flush();
    assert.equal(p.status.textContent, s.TAKEN);
    assert.equal(p.mine.size, 0);
    code = 429;
    click = seatButtons(p)[2].click();
    await click;
    assert.equal(p.status.textContent, s.FULL);
  }

  assert.equal(s.clock(600), "10:00");
  assert.equal(s.clock(61), "01:01");
  assert.equal(s.clock(-4), "00:00");
  assert.equal(s.safePath("//evil.example"), "");
  assert.equal(s.safePath("https://x"), "");
  assert.equal(s.safePath("/p/event-tickets/seats"), "/p/event-tickets/seats");
  console.log("seat-picker ok");
}

let done = false;
process.on("exit", (c) => { if (!done && c === 0) { console.error("seat-picker: test never finished"); process.exitCode = 1; } });
main().then(() => { done = true; }).catch((e) => { console.error(e); process.exit(1); });
