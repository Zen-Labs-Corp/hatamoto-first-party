// Run with: node jstest/ui_test.js
// Runs ui/app.js against a minimal DOM stub; no dependencies. Lives outside
// ui/ because everything under ui/ is served.
"use strict";
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const vm = require("node:vm");

const src = fs.readFileSync(path.join(__dirname, "..", "ui", "app.js"), "utf8");

class Node {
  constructor(tag) {
    this.tag = tag;
    this.children = [];
    this.handlers = {};
    this.hidden = false;
    this.disabled = false;
    this.value = "";
    this.textContent = "";
    this.classes = new Set();
    this.classList = { toggle: (c, on) => (on ? this.classes.add(c) : this.classes.delete(c)) };
  }
  append(...kids) { this.children.push(...kids); }
  replaceChildren(...kids) { this.children = kids; }
  addEventListener(type, fn) { this.handlers[type] = fn; }
  select() {}
}

const IDS = [
  "#notice", "#tab-events", "#tab-attendees", "#tab-settings", "#panel-events", "#panel-attendees", "#panel-settings",
  "#ev-search", "#ev-go", "#ev-state", "#ev-table", "#ev-rows", "#ev-more", "#ed", "#ed-title", "#ed-types", "#ed-add",
  "#ed-fields", "#ed-add-field", "#ed-save-fields", "#ed-close", "#at-event", "#at-state", "#at-table", "#at-rows",
  "#wl-state", "#wl-table", "#wl-rows", "#at-more", "#at-csv", "#st-rows",
  "#tab-orders", "#panel-orders", "#ed-bundle-box", "#cp-form", "#ed-bundles", "#ed-add-bundle", "#ed-save-bundles", "#or-status", "#or-event", "#or-search", "#or-go",
  "#or-state", "#or-table", "#or-rows", "#or-more", "#or-refund", "#or-refund-title", "#or-refund-amount", "#or-refund-cur",
  "#or-refund-tickets", "#or-refund-act", "#or-refund-close", "#cp-state", "#cp-table", "#cp-rows", "#cp-title", "#cp-code", "#cp-kind",
  "#cp-value", "#cp-max", "#cp-until", "#cp-event", "#cp-active", "#cp-save", "#cp-reset", "#or-fees",
];

let clock = 1000000;
const fakeDate = { now: () => clock };

// load runs the page; respond(method, path) returns {status, body}.
async function load({ respond, timers = [] }) {
  const els = {};
  for (const id of IDS) els[id] = new Node(id);
  const routes = [];
  const document = { querySelector: (s) => els[s], createElement: (t) => new Node(t), execCommand: () => true };
  const hatamoto = {
    ready: Promise.resolve(),
    route: async (method, p, body, query) => {
      routes.push({ method, p });
      return respond(method, p);
    },
  };
  const mod = { exports: {} };
  vm.runInNewContext(src, {
    document, hatamoto, module: mod, setTimeout: (fn) => timers.push(fn),
    Date: fakeDate, encodeURIComponent, Object, Number, String, Set, JSON, Error, Boolean,
  });
  await new Promise((r) => setTimeout(r, 10));
  routes.length = 0;
  return { els, routes, api: mod.exports };
}

// same compares values that were built inside the page's own context.
const same = (got, want, msg) => assert.equal(JSON.stringify(got), JSON.stringify(want), msg);
const reply = (status, obj) => ({ status, body: JSON.stringify(obj) });
const quiet = (method, p) => reply(200, p.endsWith("/settings") ? {} : { events: [], attendees: [], waitlist: [], ok: true });

(async () => {
  // the CSV path is built from an encoded event id
  let { api } = await load({ respond: quiet });
  assert.equal(api.csvPath("12"), "/api/v1/p/event-tickets/admin/events/12/attendees.csv");
  assert.equal(api.csvPath("a/b c?"), "/api/v1/p/event-tickets/admin/events/a%2Fb%20c%3F/attendees.csv");

  // a resend the route refuses shows the route's message
  let ctx = await load({ respond: (m, p) => (p.endsWith("/resend") ? reply(429, { error: "resent recently; try again in a few minutes" }) : quiet(m, p)) });
  await ctx.api.resendAttendee("7");
  assert.equal(ctx.els["#notice"].textContent, "resent recently; try again in a few minutes");
  assert.ok(ctx.els["#notice"].classes.has("error") && !ctx.els["#notice"].hidden);
  assert.deepEqual(ctx.routes[0], { method: "POST", p: "/admin/attendees/7/resend" });

  // the sandboxed iframe has no modal dialogs: ui/app.js must not use them
  assert.ok(!/\b(confirm|alert|prompt)\s*\(/.test(src), "no confirm/alert/prompt in ui/app.js");

  // cancel is a two-step button: the first click only arms it, the second cancels
  const timers = [];
  ctx = await load({ respond: quiet, timers });
  const btn = new Node("button");
  btn.textContent = "Cancel";
  await ctx.api.cancelAttendee("7", btn);
  assert.equal(btn.textContent, "Confirm cancel");
  assert.equal(ctx.routes.filter((r) => r.p.endsWith("/cancel")).length, 0, "first click: no call");
  // a double-click: the second click right after arming is ignored
  clock += 100;
  await ctx.api.cancelAttendee("7", btn);
  assert.equal(btn.textContent, "Confirm cancel", "a double-click stays armed");
  assert.equal(ctx.routes.filter((r) => r.p.endsWith("/cancel")).length, 0, "double-click: no call");
  timers.forEach((fn) => fn());
  assert.equal(btn.textContent, "Cancel", "the armed button goes back");
  await ctx.api.cancelAttendee("7", btn);
  clock += 500;
  await ctx.api.cancelAttendee("7", btn);
  assert.deepEqual(ctx.routes[0], { method: "POST", p: "/admin/attendees/7/cancel" });
  assert.equal(btn.textContent, "Cancel");

  // money is exact: no floating point, decimals from the currency
  api = (await load({ respond: quiet })).api;
  assert.equal(api.parseMoney("12.50", 2), 1250);
  assert.equal(api.parseMoney("12", 2), 1200);
  assert.equal(api.parseMoney("0.07", 2), 7);
  assert.equal(api.parseMoney("1200", 0), 1200);
  assert.equal(api.parseMoney("12.5", 0), null, "no decimals in JPY");
  for (const bad of ["", "-1", "1.234", "1,50", "abc", "1e3", "1000000.00", " "]) assert.equal(api.parseMoney(bad, 2), null, bad);
  assert.equal(api.centsText(1250, 2), "12.50");
  assert.equal(api.centsText(5, 2), "0.05");
  assert.equal(api.centsText(1200, 0), "1200");
  assert.equal(api.formatMoney(11200, "USD", 2), "112.00 USD");
  assert.equal(api.formatMoney(0, "USD", 2), "Free");

  // order rows read like the brief: tickets, money, status
  const paid = { id: 1042, status: "paid", total_cents: 11200, refunded_cents: 0, currency: "USD", lines: [{ qty: 2, name: "GA" }, { qty: 1, name: "VIP" }] };
  assert.equal(api.orderTickets(paid), "2 x GA, 1 x VIP");
  assert.equal(api.orderTickets({ lines: [], tickets: [{ type_name: "RSVP" }, { type_name: "RSVP" }] }), "2 x RSVP");
  assert.equal(api.orderStatus(paid), "Paid");
  assert.equal(api.orderStatus({ ...paid, refunded_cents: 500 }), "Paid (5.00 USD refunded)");
  assert.match(api.orderStatus({ ...paid, problem: "amount_mismatch" }), /did not match/);

  // the refund body: an amount in minor units and the picked tickets, never more than is left
  same(api.refundRequest(paid, "112.00", []), { body: { amount_cents: 11200, attendees: [] } });
  same(api.refundRequest(paid, "5", [3, 4]), { body: { amount_cents: 500, attendees: [3, 4] } });
  for (const bad of ["112.01", "0", "", "-5", "x"]) assert.ok(api.refundRequest(paid, bad, []).error, bad);
  assert.ok(api.refundRequest({ ...paid, refunded_cents: 11000 }, "5", []).error, "only 2.00 is left");

  // the coupon body
  const form = { id: 0, type_id: 0, code: "summer10", kind: "percent", value: "10", max: "100", until: "2027-06-30", event_id: "", active: true };
  same(api.couponRequest(form, 2), { body: { code: "summer10", kind: "percent", value: 10, active: true, max_uses: 100, expires_at: "2027-06-30" } });
  same(api.couponRequest({ ...form, id: 5, type_id: 3, kind: "fixed", value: "5.00", max: "", until: "", event_id: "e1" }, 2), { body: { code: "summer10", kind: "fixed", value: 500, active: true, id: 5, event_id: "e1", type_id: 3 } });
  for (const bad of [{ code: "ab" }, { code: "a b c" }, { value: "101" }, { value: "0" }, { value: "1.5" }, { kind: "fixed", value: "1.234" }]) {
    assert.ok(api.couponRequest({ ...form, ...bad }, 2).error, JSON.stringify(bad));
  }

  // the bundle body
  const rule = { type_ids: [5], all_types: false, min_qty: 4, kind: "percent", valueText: "15" };
  same(api.bundlesBody([rule], 2), { body: { rules: [{ type_ids: [5], all_types: false, min_qty: 4, kind: "percent", value: 15 }] } });
  assert.equal(api.bundlesBody([{ ...rule, kind: "fixed", valueText: "3.00" }], 2).body.rules[0].value, 300);
  assert.ok(api.bundlesBody([{ ...rule, valueText: "150" }], 2).error);
  assert.ok(api.bundlesBody([{ ...rule, all_types: true, type_ids: [] }], 2).error);

  // fees in words
  assert.equal(api.feesText({ currency: "USD", fee_fixed_cents: 100, fee_per: "ticket", fee_percent_bp: 0, fee_payer: "buyer" }), "1.00 USD per paid ticket, passed to the buyer");
  assert.equal(api.feesText({ currency: "USD", fee_fixed_cents: 30, fee_per: "order", fee_percent_bp: 250, fee_payer: "organizer" }), "0.30 USD per order plus 2.5%, paid by you");
  assert.equal(api.feesText({}), "none");

  // the Orders tab: owners get Refund and Cancel, editors get neither; the page asks for nothing else
  const orders = { orders: [{ id: 1042, name: "Ana P.", email: "ana@example.com", status: "paid", provider: "stripe", total_cents: 11200,
    refunded_cents: 0, currency: "USD", lines: [{ qty: 2, name: "GA" }], tickets: [{ id: 8, type_name: "GA", status: "active" }] },
  { id: 1041, name: "Ben K.", email: "ben@example.com", status: "confirmed", provider: "", total_cents: 0, refunded_cents: 0, currency: "",
    lines: [], tickets: [{ id: 7, type_name: "RSVP", status: "active" }] }], next_cursor: "" };
  const orderApp = (can) => load({
    respond: (m, p) => {
      if (p === "/admin/orders") return reply(200, orders);
      if (p === "/admin/settings") return reply(200, { currency: "USD", currency_exponent: 2, can_refund: can });
      return quiet(m, p);
    },
  });
  for (const can of [true, false]) {
    ctx = await orderApp(can);
    ctx.els["#tab-orders"].handlers.click();
    await new Promise((r) => setTimeout(r, 10));
    assert.ok(ctx.routes.some((r) => r.p === "/admin/orders"));
    const rows = ctx.els["#or-rows"].children;
    assert.equal(rows.length, 2);
    const actions = rows[0].children[6].children.map((b) => b.textContent);
    same(actions, can ? ["Refund", "Cancel"] : []);
    assert.equal(ctx.els["#cp-form"].hidden, !can, "editors do not see the coupon form");
    assert.equal(rows[0].children[3].children[0], "112.00 USD");
    assert.equal(rows[0].children[4].children[0], "Stripe");
    assert.equal(rows[1].children[3].children[0], "Free");
    assert.equal(rows[1].children[6].children.map((b) => b.textContent).join(), can ? "Cancel" : "", "a free order can only be cancelled");
  }

  // cancelling a paid order keeps the money recoverable: Refund stays, until it is paid back or the order never was paid
  const paidThenCancelled = { id: 1043, name: "Cy", email: "cy@example.com", status: "cancelled", provider: "stripe", total_cents: 11200, refunded_cents: 0,
    currency: "USD", lines: [], tickets: [], paid_at: "2027-01-01T12:00:00Z" };
  const allOrders = orders.orders;
  for (const [o, want] of [[paidThenCancelled, ["Refund"]], [{ ...paidThenCancelled, refunded_cents: 11200 }, []], [{ ...paidThenCancelled, paid_at: null }, []]]) {
    orders.orders = [o];
    ctx = await orderApp(true);
    ctx.els["#tab-orders"].handlers.click();
    await new Promise((r) => setTimeout(r, 10));
    same(ctx.els["#or-rows"].children[0].children[6].children.map((b) => b.textContent), want);
  }
  orders.orders = allOrders;

  // a refund is two clicks and sends the amount in minor units
  const posts = [];
  ctx = await load({
    respond: (m, p) => {
      if (p === "/admin/orders") return reply(200, orders);
      if (p === "/admin/settings") return reply(200, { currency: "USD", currency_exponent: 2, can_refund: true });
      if (m === "POST") { posts.push(p); return reply(200, { ok: true }); }
      return quiet(m, p);
    },
  });
  ctx.els["#tab-orders"].handlers.click();
  await new Promise((r) => setTimeout(r, 10));
  ctx.els["#or-rows"].children[0].children[6].children[0].handlers.click();
  assert.equal(ctx.els["#or-refund"].hidden, false);
  assert.equal(ctx.els["#or-refund-amount"].value, "112.00");
  ctx.els["#or-refund-amount"].value = "5.00";
  const go = ctx.els["#or-refund-act"].children[0];
  await go.handlers.click();
  assert.equal(go.textContent, "Confirm refund");
  assert.equal(posts.length, 0, "first click: no call");
  clock += 1000;
  await go.handlers.click();
  same(posts, ["/admin/orders/1042/refund"]);

  console.log("ok");
})().catch((e) => { console.error(e); process.exit(1); });
