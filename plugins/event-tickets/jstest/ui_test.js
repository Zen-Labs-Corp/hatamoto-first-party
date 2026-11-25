// Run with: node jstest/ui_test.js
// Runs ui/app.js against a minimal DOM stub; no dependencies. Lives outside
// ui/ because everything under ui/ is served.
"use strict";
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const vm = require("node:vm");

const src = fs.readFileSync(path.join(__dirname, "..", "ui", "app.js"), "utf8");
const checkinSrc = fs.readFileSync(path.join(__dirname, "..", "ui", "checkin.js"), "utf8");
const seatingSrc = fs.readFileSync(path.join(__dirname, "..", "ui", "seating.js"), "utf8");

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
  "#tab-seating", "#panel-seating", "#st-maps", "#st-new-name", "#st-add-map", "#st-ed", "#st-name", "#st-rename", "#st-sections", "#st-add-section", "#st-add-zone", "#st-block", "#st-form", "#st-grid",
  "#notice", "#tab-events", "#tab-attendees", "#tab-settings", "#panel-events", "#panel-attendees", "#panel-settings",
  "#ev-search", "#ev-go", "#ev-state", "#ev-table", "#ev-rows", "#ev-more", "#ed", "#ed-title", "#ed-types", "#ed-map-row", "#ed-add",
  "#ed-fields", "#ed-add-field", "#ed-save-fields", "#ed-close", "#at-event", "#at-state", "#at-table", "#at-rows",
  "#wl-state", "#wl-table", "#wl-rows", "#at-more", "#at-csv", "#st-rows",
  "#tab-orders", "#panel-orders", "#ed-bundle-box", "#cp-form", "#ed-bundles", "#ed-add-bundle", "#ed-save-bundles", "#ed-split-box", "#ed-splits", "#ed-add-split", "#ed-save-splits",
  "#tab-payouts", "#panel-payouts", "#po-period", "#po-state", "#po-held", "#po-table", "#po-rows", "#po-csv", "#po-mark", "#po-mark-title",
  "#po-mark-date", "#po-mark-ref", "#po-mark-act", "#po-mark-close", "#or-status", "#or-event", "#or-search", "#or-go",
  "#or-state", "#or-table", "#or-rows", "#or-more", "#or-refund", "#or-refund-title", "#or-refund-amount", "#or-refund-cur",
  "#or-refund-tickets", "#or-refund-act", "#or-refund-close", "#cp-state", "#cp-table", "#cp-rows", "#cp-title", "#cp-code", "#cp-kind",
  "#cp-value", "#cp-max", "#cp-until", "#cp-event", "#cp-active", "#cp-save", "#cp-reset", "#or-fees", "#or-bundles",
  "#tab-checkin", "#panel-checkin", "#ck-event", "#ck-count", "#ck-scan", "#ck-search", "#ck-go", "#ck-camera-hint", "#ck-match-table",
  "#ck-matches", "#ck-match-state", "#ck-log", "#ck-queue", "#ck-warn",
  "#tab-emails", "#panel-emails", "#em-confirm", "#em-changed", "#em-reminder", "#em-hours", "#em-thanks", "#em-save", "#em-quota",
  "#cm-state", "#cm-table", "#cm-rows", "#cm-new",
  "#cm-form", "#cm-subject", "#cm-format", "#cm-body", "#cm-event", "#cm-type", "#cm-checked", "#cm-before", "#cm-rsvp", "#cm-notbought",
  "#cm-count", "#cm-count-out", "#cm-when", "#cm-at", "#cm-send", "#cm-cancel",
];

let clock = 1000000;
class FakeDate extends Date { static now() { return clock; } }

// load runs the page; respond(method, path) returns {status, body}.
async function load({ respond, timers = [], camera = { available: false } }) {
  const els = {};
  for (const id of IDS) els[id] = new Node(id);
  const routes = [];
  const bodies = [];
  const document = { querySelector: (s) => els[s], createElement: (t) => new Node(t), execCommand: () => true };
  const hatamoto = {
    ready: Promise.resolve(),
    camera,
    route: async (method, p, body, query) => {
      routes.push({ method, p });
      bodies.push(body);
      return respond(method, p);
    },
  };
  const mod = { exports: {} };
  // The page scripts share one context, like the browser: checkin.js first.
  const window = { addEventListener() {} };
  Object.defineProperty(window, "localStorage", { get() { throw new Error("SecurityError"); } });
  const context = vm.createContext({
    document, hatamoto, window, module: mod, setTimeout: (fn) => timers.push(fn),
    Date: FakeDate, crypto: globalThis.crypto, Uint8Array, encodeURIComponent, Object, Number, String, Set, JSON, Error, Boolean, Array, Map, Promise,
  });
  vm.runInContext(checkinSrc, context);
  vm.runInContext(seatingSrc, context);
  vm.runInContext(src, context);
  await new Promise((r) => setTimeout(r, 10));
  routes.length = 0;
  return { els, routes, bodies, api: mod.exports };
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

  // a scan answer "already" reads ALREADY CHECKED IN; a refused one says why
  ctx = await load({ respond: quiet });
  ctx.api.state.checkinEvent = "1";
  ctx.api.showScanResult({ event: "2", body: { scan: "t" } }, { ok: true, result: "already", attendee: { id: 5, name: "Ben K.", type_name: "VIP", checked_in_at: "2026-01-01T18:55:00Z" } });
  let row = ctx.els["#ck-log"].children[0];
  assert.match(row.children[2].textContent, /^ALREADY CHECKED IN \d\d:\d\d$/);
  assert.equal(row.children[0].textContent, "Ben K.");
  assert.equal(row.children.length, 3, "no Undo on an already-checked-in row");
  ctx.api.showScanResult({ event: "2", body: { scan: "t" } }, { ok: true, result: "ok", attendee: { id: 6, name: "Ana P.", type_name: "GA", checked_in_at: "2026-01-01T19:02:00Z" } });
  row = ctx.els["#ck-log"].children[0];
  assert.match(row.children[2].textContent, /^OK \d\d:\d\d$/);
  assert.equal(row.children.length, 4, "OK rows have Undo");
  ctx.api.showScanResult({ event: "2", body: { scan: "t" } }, { ok: true, result: "refused", reason: "other_event" });
  assert.equal(ctx.els["#ck-log"].children[0].children[2].textContent, "REFUSED: ticket is for another event");
  assert.equal(ctx.els["#ck-log"].children.length, 3);

  // a final error is not REFUSED: it says to scan again and when it was scanned
  ctx.api.showScanResult({ event: "2", body: { scan: "t" }, at: Date.UTC(2026, 0, 1, 19, 2) }, { result: "error" });
  assert.match(ctx.els["#ck-log"].children[0].children[2].textContent, /^NOT SENT: scan again or search \(scanned \d\d:\d\d\)$/);
  // a retry-confirmed scan reads OK, not ALREADY
  ctx.api.showScanResult({ event: "2", body: { scan: "t" } }, { result: "ok", confirmed: true, attendee: { id: 7, name: "Cy", type_name: "GA", checked_in_at: "2026-01-01T19:02:00Z" } });
  assert.match(ctx.els["#ck-log"].children[0].children[2].textContent, /^OK \(confirmed on retry\) \d\d:\d\d$/);

  // the "Server not reachable" notice goes away when a later run drains the queue
  let up = false;
  const stallTimers = [];
  ctx = await load({ timers: stallTimers, respond: (m, p) => (p.endsWith("/checkin") ? (up ? reply(200, { ok: true, result: "ok", attendee: { id: 1, name: "Ana" } }) : reply(404, {})) : quiet(m, p)) });
  ctx.api.ckQueue.add("1", { scan: "t" });
  await ctx.api.ckQueue.sync();
  assert.match(ctx.els["#notice"].textContent, /Server not reachable \(404\)/);
  up = true;
  await stallTimers[0]();
  assert.equal(ctx.api.ckQueue.size(), 0);
  assert.ok(ctx.els["#notice"].hidden, "stall notice cleared once the queue drained");

  // a cancelled capture right after another one is core's cooldown: say so, never retry
  let captures = 0;
  ctx = await load({ respond: quiet, camera: { available: true, capture: async () => { captures++; return { ok: false, error: "cancelled" }; } } });
  ctx.api.state.checkinEvent = "1";
  await ctx.api.scanClick();
  assert.ok(ctx.els["#notice"].hidden, "the first cancel is silent");
  clock += 1000;
  await ctx.api.scanClick();
  assert.match(ctx.els["#notice"].textContent, /Wait a few seconds/);
  assert.equal(captures, 2, "no automatic retry");
  clock += 6000;
  ctx.els["#notice"].hidden = true;
  await ctx.api.scanClick();
  assert.ok(ctx.els["#notice"].hidden, "after the cooldown a cancel is silent again");

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

  // payouts: UTC months, newest first; the default is the month that just ended
  const months = api.periodOptions(Date.UTC(2026, 0, 15));
  assert.equal(months.length, 24);
  same(months.slice(0, 2), [{ value: "2026-01", label: "January 2026" }, { value: "2025-12", label: "December 2025" }]);
  assert.equal(api.defaultPeriod(Date.UTC(2026, 6, 1, 0, 0, 0)), "2026-06");
  assert.equal(api.defaultPeriod(Date.UTC(2026, 0, 31, 23, 59, 59)), "2025-12");
  assert.equal(api.shareText({ kind: "percent", value: 8000 }, 2), "80%");
  assert.equal(api.shareText({ kind: "percent", value: 1250 }, 2), "12.5%");
  assert.equal(api.shareText({ kind: "fixed", value: 200 }, 2), "2.00 / ticket");
  assert.equal(api.shareText({ kind: "mixed" }, 2), "mixed");
  api.state.payouts = { period: "2026-06" };
  same(api.markRequest({ email: "a@example.org", currency: "USD", owed: 319680 }, "2026-07-02", " TR-88 "),
    { period: "2026-06", email: "a@example.org", currency: "USD", paid_on: "2026-07-02", reference: "TR-88", owed_cents: 319680 });

  // payout splits: percent as basis points, fixed as minor units, by string math
  for (const [text, bp] of [["80", 8000], ["12.5", 1250], ["12.25", 1225], ["0.01", 1], ["100", 10000], [" 5 ", 500]]) assert.equal(api.parsePercent(text), bp, text);
  for (const bad of ["100.01", "0", "0.00", "abc", "", "-5", "1.234", "101"]) assert.throws(() => api.parsePercent(bad), /Percent must be/, bad);
  const split = { name: " Jazz Society ", email: " pay@example.org ", kind: "percent", text: "80" };
  same(api.splitsBody([split], 2), { rules: [{ name: "Jazz Society", email: "pay@example.org", kind: "percent", value: 8000 }] });
  assert.equal(api.splitsBody([{ ...split, kind: "fixed", text: "2.00" }], 2).rules[0].value, 200);
  assert.equal(api.splitsBody([{ ...split, kind: "fixed", text: "500" }], 0).rules[0].value, 500);
  assert.throws(() => api.splitsBody([{ ...split, kind: "fixed", text: "x" }], 2), /fixed amount/);

  // fees in words
  assert.equal(api.feesText({ currency: "USD", fee_fixed_cents: 100, fee_per: "ticket", fee_percent_bp: 0, fee_payer: "buyer" }), "1.00 USD per paid ticket, passed to the buyer");
  assert.equal(api.feesText({ currency: "USD", fee_fixed_cents: 30, fee_per: "order", fee_percent_bp: 250, fee_payer: "organizer" }), "0.30 USD per order plus 2.5%, paid by you");
  assert.equal(api.feesText({}), "none");
  assert.equal(api.bundlesText([{ min_qty: 4, kind: "percent", value: 15, all_types: false, type_names: ["GA"] }]), "4+ GA tickets -> 15% off");
  assert.equal(api.bundlesText([]), "none");

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

  // the Payouts tab lists payees, marks one paid in two clicks and sends the amount on screen
  const payees = [
    { name: "Jazz Society", email: "pay@example.org", currency: "USD", events: 3, gross: 420000, refunds: 12000, fees: 8400, share: { kind: "percent", value: 8000 }, owed: 319680, paid: null },
    { name: "Co-host Ana", email: "ana@example.org", currency: "USD", events: 1, gross: 145000, refunds: 0, fees: 2900, share: { kind: "percent", value: 1000 }, owed: 14210,
      paid: { paid_on: "2026-07-02", reference: "TR-88", paid_cents: 14210, changed: false } },
  ];
  const marks = [];
  ctx = await load({
    respond: (m, p) => {
      if (p === "/admin/payouts") return reply(200, { ok: true, period: "1969-12", payees, held_back: 2, exponents: { USD: 2 } });
      if (p === "/admin/settings") return reply(200, { currency: "USD", currency_exponent: 2, can_refund: true });
      if (m === "POST") { marks.push(p); return reply(200, { ok: true }); }
      return quiet(m, p);
    },
  });
  assert.equal(ctx.els["#tab-payouts"].hidden, false);
  ctx.els["#tab-payouts"].handlers.click();
  await new Promise((r) => setTimeout(r, 10));
  const prow = ctx.els["#po-rows"].children;
  assert.equal(prow.length, 2);
  same(prow[0].children.map((c) => c.textContent || c.children[0]).slice(0, 7), ["Jazz Society", "3", "4200.00 USD", "120.00 USD", "84.00 USD", "80%", "3196.80 USD"]);
  assert.equal(prow[1].children[7].children[0], "Paid 2 Jul #TR-88 ");
  assert.equal(prow[1].children[7].children[1].textContent, "Undo");
  assert.equal(ctx.els["#po-held"].hidden, false);
  assert.equal(ctx.els["#po-csv"].value, "/api/v1/p/event-tickets/admin/payouts.csv?period=1969-12");
  prow[0].children[7].children[0].handlers.click();
  assert.equal(ctx.els["#po-mark"].hidden, false);
  assert.match(ctx.els["#po-mark-title"].textContent, /Jazz Society.*3196\.80 USD/);
  const markGo = ctx.els["#po-mark-act"].children[0];
  await markGo.handlers.click();
  assert.equal(marks.length, 0, "first click: no call");
  clock += 1000;
  await markGo.handlers.click();
  same(marks, ["/admin/payouts/mark"]);
  assert.equal(ctx.els["#po-mark"].hidden, true);

  // Emails tab: owner only, automations, campaign rows
  {
    const calls = [];
    const campaigns = [
      { id: 9, subject: "Last tickets", segment: { event_id: "e1", not_bought: true }, status: "sending", send_at: "2027-03-01T10:00:00Z", total: 10, sent: 4, opens: 2, clicks: 1, buys: 1 },
      { id: 7, subject: "Later", segment: {}, status: "scheduled", send_at: "2027-03-05T14:30:00Z", total: 0, sent: 0, opens: 0, clicks: 0, buys: null },
      { id: 8, subject: "Hello", segment: {}, status: "sent", send_at: "2027-02-01T10:00:00Z", total: 0, sent: 0, opens: 0, clicks: 0, buys: null },
    ];
    const emailsCtx = (canRefund) => load({
      respond: (m, p) => {
        if (p === "/admin/settings") return reply(200, { currency: "USD", currency_exponent: 2, can_refund: canRefund });
        if (p === "/admin/events") return reply(200, { events: [{ id: "e1", name: "Summer Jazz", types: 1, taken: 0, capacity: 5, waiting: 0 }] });
        if (p === "/admin/emails") return reply(200, { ok: true, automations: { changed: true, reminder: true, reminder_hours: 24, thanks: false }, quota: 360, bulk_per_run: 22, site_origin_ok: true, campaigns });
        if (m === "POST") { calls.push(p); return reply(200, { ok: true }); }
        return quiet(m, p);
      },
    });
    let ec = await emailsCtx(false);
    assert.equal(ec.els["#tab-emails"].hidden, true, "editors do not see the Emails tab");
    ec = await emailsCtx(true);
    assert.equal(ec.els["#tab-emails"].hidden, false);
    ec.els["#tab-emails"].handlers.click();
    await new Promise((r) => setTimeout(r, 10));
    assert.equal(ec.els["#em-changed"].checked, true);
    assert.equal(ec.els["#em-thanks"].checked, false);
    assert.equal(ec.els["#em-hours"].value, "24");
    assert.equal(ec.els["#em-quota"].textContent, "Sends up to 22 emails every 5 minutes (site quota 360 per hour).");
    const crow = ec.els["#cm-rows"].children;
    assert.equal(crow.length, 3);
    assert.equal(crow[1].children[2].children[0], "Scheduled 2027-03-05 14:30 UTC");
    same(crow[0].children.slice(0, 7).map((c) => c.textContent || c.children[0]), ["Last tickets", "Summer Jazz, not bought", "Sending", "4 / 10", "2 (50%)", "1 (25%)", "1"]);
    assert.equal(crow[0].children[7].children[0].textContent, "Cancel");
    assert.equal(crow[2].children[7].children.length, 0, "a sent campaign has no cancel button");
    same(crow[2].children.slice(1, 7).map((c) => c.textContent || c.children[0]), ["Everyone with a ticket", "Sent", "0 / 0", "0 (-)", "0 (-)", "n/a"]);
    ec.els["#em-thanks"].checked = true;
    ec.els["#em-hours"].value = "48";
    same(ec.api.automationsBody(), { changed: true, reminder: true, reminder_hours: 48, thanks: true });
    await ec.els["#em-save"].handlers.click();
    same(calls, ["/admin/emails/automations"]);
    ec.els["#em-hours"].value = "0";
    await ec.els["#em-save"].handlers.click();
    assert.equal(calls.length, 1, "bad hours are not sent");
    assert.equal(ec.api.percentText(1, 3), "33%");
    assert.equal(ec.api.percentText(0, 0), "-");
    assert.equal(ec.api.segmentText({ event_id: "x", checked_in: "no", bought_before: "2027-01-15" }, []), "One event, not checked in, bought before 2027-01-15");
    assert.equal(ec.api.segmentText({ rsvp_only: true }), "RSVP only");
  }

  // New campaign form: requests, count, send
  {
    const base = { subject: " Hello ", format: "text", body: "Hi", event_id: "", type_id: "", checked_in: "", bought_before: "", rsvp_only: false, not_bought: false, when: "now", at: "" };
    same(api.campaignRequest(base), { body: { subject: "Hello", format: "text", body: "Hi",
      segment: { event_id: "", type_id: 0, checked_in: "", bought_before: "", rsvp_only: false, not_bought: false }, send_at: "" } });
    const at = api.campaignRequest({ ...base, event_id: "e1", type_id: "5", checked_in: "yes", bought_before: "2027-01-15", rsvp_only: true, when: "at", at: "2030-05-01T10:30" });
    same(at.body.segment, { event_id: "e1", type_id: 5, checked_in: "yes", bought_before: "2027-01-15", rsvp_only: true, not_bought: false });
    assert.equal(at.body.send_at, new Date("2030-05-01T10:30").toISOString());
    assert.match(api.campaignRequest({ ...base, subject: "  " }).error, /subject/);
    assert.match(api.campaignRequest({ ...base, body: "" }).error, /message/);
    assert.match(api.campaignRequest({ ...base, event_id: "e1", type_id: "5", not_bought: true }).error, /Not bought/);
    assert.match(api.campaignRequest({ ...base, not_bought: true }).error, /Choose an event/);
    assert.match(api.campaignRequest({ ...base, checked_in: "no" }).error, /Choose an event/);
    assert.match(api.campaignRequest({ ...base, when: "at", at: "" }).error, /future/);
    assert.match(api.campaignRequest({ ...base, when: "at", at: "1970-01-01T00:00" }).error, /future/);
    same(api.segmentRequest({ event_id: "e1", not_bought: true }).body.not_bought, true);
    assert.ok(api.segmentRequest({ type_id: "3" }).error);

    const posts = [];
    const fc = await load({
      respond: (m, p) => {
        if (p === "/admin/settings") return reply(200, { can_refund: true });
        if (p === "/admin/emails") return reply(200, { ok: true, automations: {}, quota: 360, bulk_per_run: 22, site_origin_ok: true, campaigns: [] });
        if (p === "/admin/campaigns/count") { posts.push(p); return reply(200, { ok: true, count: 42 }); }
        if (p === "/admin/campaigns") { posts.push(p); return reply(200, { ok: true, id: 1 }); }
        return quiet(m, p);
      },
    });
    fc.els["#tab-emails"].handlers.click();
    await new Promise((r) => setTimeout(r, 10));
    fc.els["#cm-new"].handlers.click();
    assert.equal(fc.els["#cm-form"].hidden, false);
    fc.els["#cm-subject"].value = "News";
    fc.els["#cm-body"].value = "Hello";
    fc.els["#cm-format"].value = "text";
    await fc.els["#cm-count"].handlers.click();
    assert.equal(fc.els["#cm-count-out"].textContent, "42 recipients (unsubscribed people left out)");
    fc.els["#cm-notbought"].checked = true;
    fc.els["#cm-notbought"].handlers.change();
    assert.equal(fc.els["#cm-type"].disabled, true);
    fc.els["#cm-notbought"].checked = false;
    fc.els["#cm-notbought"].handlers.change();
    assert.equal(fc.els["#cm-type"].disabled, false);
    const sendBtn = fc.els["#cm-send"].children[0];
    await sendBtn.handlers.click();
    same(posts, ["/admin/campaigns/count"], "first click only arms the send");
    clock += 1000;
    await sendBtn.handlers.click();
    same(posts, ["/admin/campaigns/count", "/admin/campaigns"]);
    assert.equal(fc.els["#cm-form"].hidden, true);
  }

  // the type row has a seats select (none + every section); a bound type shows the server's capacity
  {
    const maps = [{ id: 3, name: "Main Hall", sections: [
      { id: 12, name: "Stalls", kind: "grid", tier: "Std" }, { id: 13, name: "Balcony", kind: "grid", tier: "VIP" }, { id: 14, name: "Standing", kind: "zone", tier: "" }] }];
    const typesReply = { ok: true, types: [] };
    const tc = await load({ respond: (m, p) => (p.endsWith("/types") ? reply(200, typesReply) : quiet(m, p)) });
    const kidsOf = (n, tag, out = []) => { for (const c of n.children) if (c && typeof c === "object") { if (c.tag === tag) out.push(c); kidsOf(c, tag, out); } return out; };
    tc.api.state.settings = { currency: "USD" };
    tc.api.state.editing = { id: "e1", name: "Show", types: [], fields: [], bundles: [], splits: [], maps, mapPick: "", defaultCapacity: 50 };
    same(tc.api.sectionOptions(maps).map((o) => o[1]), ["none", "Main Hall / Stalls (Std)", "Main Hall / Balcony (VIP)", "Main Hall / Standing"]);

    // "Add types for map": one unsaved row per unbound section, a grid after its tier, a zone after itself
    assert.equal(tc.api.addMapTypes("3"), 3);
    same(tc.api.state.editing.types.map((t) => [t.id, t.name, t.section_id, t.price_cents, t.on_sale]), [[0, "Std", 12, 0, true], [0, "VIP", 13, 0, true], [0, "Standing", 14, 0, true]]);
    assert.equal(tc.els["#ed-map-row"].hidden, false);
    // a tier already used by another type falls back to the section name
    tc.api.state.editing.types = [];
    maps[0].sections.push({ id: 15, name: "Circle", kind: "grid", tier: "Std" }, { id: 16, name: "Pit", kind: "zone", tier: "Std" });
    tc.api.addMapTypes("3");
    same(tc.api.state.editing.types.map((t) => t.name), ["Std", "VIP", "Standing", "Circle", "Pit"]);
    maps[0].sections.length = 3;
    tc.api.state.editing.types = [];
    tc.api.addMapTypes("3");
    // a section that already has a type gets no second one
    tc.api.state.editing.types = [{ id: 9, name: "Std", capacity: 200, taken: 0, waitlist: false, on_sale: true, position: 0, price_cents: 1000, section_id: 12 }];
    assert.equal(tc.api.addMapTypes("3"), 2);
    tc.api.state.editing.types = tc.api.state.editing.types.slice(0, 1);

    // a bound type: select pre-set, capacity disabled, saving without a change sends no section
    const bound = tc.api.typeRow(tc.api.state.editing.types[0]);
    const [selBound] = kidsOf(bound, "select");
    assert.equal(selBound.value, "12");
    const [capBound] = kidsOf(bound, "input").filter((i) => i.type === "number");
    assert.equal(capBound.disabled, true);
    assert.equal(capBound.value, "200");
    tc.bodies.length = 0;
    await kidsOf(bound, "button").find((b) => b.textContent === "Save").handlers.click();
    assert.ok(!("section_id" in tc.bodies[0]), "unchanged binding is not sent");

    // a new row bound to a section sends section_id; choosing none on a bound type sends 0
    const fresh = tc.api.typeRow({ id: 0, name: "VIP", capacity: 50, taken: 0, waitlist: false, on_sale: true, position: 1, price_cents: 0, section_id: 13 });
    tc.bodies.length = 0;
    await kidsOf(fresh, "button").find((b) => b.textContent === "Save").handlers.click();
    assert.equal(tc.bodies[0].section_id, 13);
    selBound.value = "";
    selBound.handlers.change();
    assert.equal(capBound.disabled, false);
    tc.bodies.length = 0;
    await kidsOf(bound, "button").find((b) => b.textContent === "Save").handlers.click();
    assert.equal(tc.bodies[0].section_id, 0);

    // a refusal is shown as the server worded it
    typesReply.ok = false;
    const refuse = await load({ respond: (m, p) => (p.endsWith("/types") ? reply(422, { error: "A seat section needs a price above zero" }) : quiet(m, p)) });
    refuse.api.state.settings = { currency: "USD" };
    refuse.api.state.editing = { id: "e1", name: "Show", types: [], fields: [], bundles: [], splits: [], maps, mapPick: "" };
    const row2 = refuse.api.typeRow({ id: 0, name: "Std", capacity: 50, taken: 0, waitlist: false, on_sale: true, position: 0, price_cents: 0, section_id: 12 });
    await kidsOf(row2, "button").find((b) => b.textContent === "Save").handlers.click();
    assert.equal(refuse.els["#notice"].textContent, "A seat section needs a price above zero");
  }

  console.log("ok");
})().catch((e) => { console.error(e); process.exit(1); });
