// Run with: node jstest/browser_bus_test.js
// Loads both islands the way a page does, with a global document, so the env
// each island builds for itself is the one under test. A seats event dispatched
// on the document must reach the Tickets island, and the picker's pay event
// must too.
const assert = require("node:assert/strict");
const path = require("node:path");

class Node {
  constructor(tag) {
    Object.assign(this, { tag, children: [], attrs: {}, handlers: {}, value: "", checked: false, textContent: "", className: "", disabled: false, hidden: false, focused: 0 });
    this.classList = { toggle() {} };
  }
  append(...kids) { this.children.push(...kids); }
  replaceChildren(...kids) { this.children = [...kids]; }
  replaceWith() {}
  setAttribute(k, v) { this.attrs[k] = String(v); }
  getAttribute(k) { return k in this.attrs ? this.attrs[k] : null; }
  addEventListener(type, fn) { this.handlers[type] = fn; }
  focus() { this.focused++; }
  all(pred, out = []) {
    if (pred(this)) out.push(this);
    for (const c of this.children) c.all(pred, out);
    return out;
  }
  querySelectorAll(sel) { return this.all((n) => n !== this && n.className.split(" ").includes(sel.slice(1))); }
  querySelector(sel) { return this.querySelectorAll(sel)[0] || null; }
}

function typeRow() {
  const r = new Node("div");
  r.className = "hm-event-tickets_ticket-type";
  Object.assign(r.attrs, { "data-type": "5", "data-event": "e1" });
  for (const c of ["name", "left"]) { const n = new Node("span"); n.className = c; r.append(n); }
  return r;
}

function page() {
  const tickets = new Node("div");
  tickets.className = "hm-event-tickets_tickets";
  Object.assign(tickets.attrs, { "data-api": "/p/event-tickets/rsvp", "data-token-api": "/p/event-tickets/token", "data-heading": "Tickets" });
  const types = new Node("div");
  types.className = "types";
  types.append(typeRow());
  const note = new Node("p");
  note.className = "note";
  tickets.append(types, note);
  const picker = new Node("div");
  picker.className = "hm-event-tickets_seat-picker";
  Object.assign(picker.attrs, { "data-seats-api": "/p/event-tickets/seats", "data-token-api": "/p/event-tickets/token" });
  const listeners = {};
  const document = {
    createElement: (t) => new Node(t),
    addEventListener: (name, fn) => (listeners[name] ||= []).push(fn),
    dispatchEvent: (e) => { for (const fn of listeners[e.type] || []) fn(e); return true; },
    querySelectorAll: (sel) => (sel === ".hm-event-tickets_tickets" ? [tickets] : sel === ".hm-event-tickets_seat-picker" ? [picker] : []),
    querySelector: (sel) => (sel === ".hm-event-tickets_ticket-type[data-event]" ? types.children[0] : null),
  };
  return { tickets, picker, document };
}

const json = (body) => ({ ok: true, status: 200, json: async () => body });
const flush = () => new Promise((r) => setImmediate(r));
const TOKEN = { token: "1700000000000.abcd", types: [{ id: 5, name: "Standard", left: 4, sold_out: false, waitlist: false, price_cents: 1000, price: "10.00 USD" }], fields: [], providers: ["stripe"], currency: "USD" };
const SEATS = { ok: true, sections: [{ id: 7, kind: "grid", name: "Stalls", tier: "Std", type_id: 5, price: "10.00 USD", rows: [{ l: "A", s: "oo" }] }] };

function load(rel) {
  const file = path.join(__dirname, "..", rel);
  delete require.cache[require.resolve(file)];
  return require(file);
}

(async () => {
  const p = page();
  Object.assign(globalThis, {
    document: p.document,
    CustomEvent: class { constructor(type, init = {}) { this.type = type; this.detail = init.detail; } },
    fetch: async (u) => json(String(u).includes("/seats") ? SEATS : TOKEN),
    location: { assign() {} },
  });
  load("widgets/tickets/island.js");
  load("widgets/seat-picker/island.js");
  await flush();
  await flush();

  assert.equal(p.tickets.all((n) => n.tag === "button" && n.disabled).length, 0, "no stepper is locked before a seat is picked");

  // the picker's seats event, dispatched on the document, reaches the Tickets island
  document.dispatchEvent(new CustomEvent("event-tickets:seats", { detail: { hold: "H.1", counts: { 5: 2 }, seated: [5] } }));
  const locked = p.tickets.all((n) => n.tag === "button" && n.disabled === true);
  assert.ok(locked.length >= 2, "the seats event locks the steppers of the picked type");

  // the picker's Continue button reaches the Tickets island's name field
  const before = p.tickets.all((n) => n.focused).reduce((a, n) => a + n.focused, 0);
  document.dispatchEvent(new CustomEvent("event-tickets:pay", { detail: {} }));
  const after = p.tickets.all((n) => n.focused).reduce((a, n) => a + n.focused, 0);
  assert.equal(after, before + 1, "the pay event focuses the buyer's name field");
  console.log("ok");
  process.exit(0);
})().catch((e) => { console.error(e); process.exit(1); });
