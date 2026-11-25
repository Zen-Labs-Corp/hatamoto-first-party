// Run with: node jstest/tickets_test.js
const assert = require("node:assert/strict");
const s = require("../widgets/tickets/island.js");

// a minimal DOM: elements with children, attributes, classes and handlers
class Node {
  constructor(tag) {
    this.tag = tag;
    this.children = [];
    this.parent = null;
    this.attrs = {};
    this.handlers = {};
    this.value = "";
    this.checked = false;
    this.textContent = "";
    this.className = "";
    this.disabled = false;
    this.hidden = false;
    this.classes = new Set();
    this.classList = { toggle: (c, on) => (on ? this.classes.add(c) : this.classes.delete(c)) };
  }
  append(...kids) { for (const k of kids) { k.parent = this; this.children.push(k); } }
  replaceChildren(...kids) { this.children = []; this.append(...kids); }
  replaceWith(n) { n.parent = this.parent; this.parent.children[this.parent.children.indexOf(this)] = n; }
  setAttribute(k, v) { this.attrs[k] = String(v); }
  getAttribute(k) { return k in this.attrs ? this.attrs[k] : null; }
  addEventListener(type, fn) { this.handlers[type] = fn; }
  click() { return this.handlers.click(); }
  all(pred, out = []) {
    if (pred(this)) out.push(this);
    for (const c of this.children) c.all(pred, out);
    return out;
  }
  // only ".class" selectors, which is all the island uses
  querySelectorAll(sel) {
    const cls = sel.slice(1);
    return this.all((n) => n !== this && n.className.split(" ").includes(cls));
  }
  querySelector(sel) { return this.querySelectorAll(sel)[0] || null; }
}
const doc = { createElement: (t) => new Node(t) };

function row(id, event, name, extra = {}) {
  const r = new Node("div");
  r.className = "hm-event-tickets_ticket-type";
  Object.assign(r.attrs, { "data-type": String(id), "data-event": event }, extra);
  const left = new Node("span");
  left.className = "left";
  left.textContent = "5 left";
  const n = new Node("span");
  n.className = "name";
  n.textContent = name;
  r.append(n, left);
  return r;
}

function box(attrs = {}, rows = [row(5, "e1", "Standard"), row(6, "e1", "VIP", { "data-sold-out": "1", "data-waitlist": "1" })]) {
  const b = new Node("div");
  b.className = "hm-event-tickets_tickets";
  Object.assign(b.attrs, { "data-api": "/p/event-tickets/rsvp", "data-token-api": "/p/event-tickets/token", "data-heading": "Tickets" }, attrs);
  const types = new Node("div");
  types.className = "types";
  types.append(...rows);
  const note = new Node("p");
  note.className = "note";
  b.append(types, note);
  return b;
}

function answer(status, body) {
  return { ok: status >= 200 && status < 300, status, json: async () => body };
}

const TOKEN = {
  token: "1700000000000.abcd",
  types: [
    { id: 5, name: "Standard", left: 4, sold_out: false, waitlist: true },
    { id: 6, name: "VIP", left: 0, sold_out: true, waitlist: true },
  ],
  fields: [
    { key: "diet", label: "Diet", kind: "select", options: ["none", "vegan"], required: false },
    { key: "terms", label: "I agree", kind: "checkbox", required: true },
  ],
};
// a paid event: one free type and one priced type, both providers set up
const PAID = {
  ...TOKEN,
  currency: "USD",
  providers: ["stripe", "paypal"],
  types: [
    { id: 5, name: "Standard", left: 8, sold_out: false, waitlist: false, price_cents: 1000, price: "10.00 USD" },
    { id: 8, name: "Student", left: 8, sold_out: false, waitlist: false, price_cents: 0, price: "Free" },
    TOKEN.types[1],
  ],
  fields: [],
};
const QUOTE = {
  ok: true,
  coupon_ok: true,
  quote: { subtotal_cents: 2000, discount_cents: 200, fee_cents: 30, fee_absorbed: false, total_cents: 1830 },
  display: { subtotal: "20.00 USD", discount: "2.00 USD", fee: "0.30 USD", total: "18.30 USD" },
};
const THREE = { ...TOKEN, types: [{ id: 5, name: "Standard", left: 8, sold_out: false, waitlist: false }, { id: 8, name: "Student", left: 8, sold_out: false, waitlist: false }, TOKEN.types[1]] };

const threeRows = () => [row(5, "e1", "Standard"), row(8, "e1", "Student"), row(6, "e1", "VIP", { "data-sold-out": "1", "data-waitlist": "1" })];

// env with a script of answers and a clock the test moves
function setup({ attrs, rows, bus = { handlers: {}, sent: [] }, post = answer(200, { ok: true, tickets: ["/p/event-tickets/ticket/a.b"] }), token = answer(200, TOKEN), quote = answer(200, QUOTE) } = {}) {
  const calls = [];
  const gone = []; // pages the browser was sent to
  let clock = 1_000_000;
  const env = {
    doc,
    now: () => clock,
    go: (u) => gone.push(u),
    fetch: async (url, opts) => {
      calls.push({ url, opts });
      const r = url.startsWith("/p/event-tickets/token") ? token : url === "/p/event-tickets/quote" ? quote : post;
      if (r instanceof Error) throw r;
      return r;
    },
    // a delay moves the clock instead of waiting
    delay: (fn, ms) => { clock += ms; fn(); },
  };
  env.on = (name, fn) => { bus.handlers[name] = fn; };
  env.emit = (name, detail) => bus.sent.push({ name, detail });
  const b = box(attrs, rows);
  const form = s.mount(b, env);
  return { b, form, calls, gone, advance: (ms) => { clock += ms; }, now: () => clock };
}

const tick = () => new Promise((r) => setTimeout(r, 0));
const posts = (calls) => calls.filter((c) => c.opts && c.opts.method === "POST");
const buttons = (b, cls) => b.all((n) => n.tag === "button" && n.className === cls);

function fillBuyer(form) {
  form.nameInput.value = " Ada ";
  form.emailInput.value = "ada@example.org";
  if (form.inputs.terms) form.inputs.terms.checked = true;
}

(async () => {
  // pure helpers
  assert.equal(s.clampQty(3, 0), 3);
  assert.equal(s.clampQty(11, 0), 10);
  assert.equal(s.clampQty(5, 7), 3);
  assert.equal(s.clampQty(-2, 0), 0);
  assert.equal(s.clampQty(NaN, 0), 0);
  assert.equal(s.safePath("/p/x"), "/p/x");
  assert.equal(s.safePath("//evil.test"), "");
  assert.equal(s.safePath("javascript:x"), "");
  assert.equal(s.messageFor(200, { ok: true, tickets: [] }), "Booked. Check your email.");
  assert.equal(s.messageFor(200, { ok: true, waitlisted: true }), "You are on the waitlist. We will email you if a seat frees up.");
  assert.equal(s.messageFor(409, null), "Sold out");
  assert.equal(s.messageFor(422, { ok: false }), "Please check the highlighted fields.");
  assert.equal(s.messageFor(404, null), "Booking is closed.");
  assert.equal(s.messageFor(503, null), "Try again later.");
  assert.equal(s.messageFor(500, null), "Try again later.");

  // mount: token asked for the event of the first row, rows updated live, form built
  {
    const { b, form, calls } = setup();
    await tick();
    assert.equal(calls.length, 1);
    assert.equal(calls[0].url, "/p/event-tickets/token?event=e1");
    assert.equal(b.querySelector(".note").hidden, true);
    assert.equal(form.rows[0].querySelector(".left").textContent, "4 left");
    assert.equal(form.rows[1].querySelector(".left").textContent, "Sold out");
    assert.equal(buttons(b, "plus").length, 1);
    assert.equal(buttons(b, "join").length, 1);
    const labels = b.all((n) => n.tag === "label").map((n) => n.textContent);
    assert.deepEqual(labels, ["Your name", "Your email", "Diet", "I agree"]);
    assert.equal(form.inputs.diet.tag, "select");
    assert.equal(form.inputs.terms.type, "checkbox");
    assert.equal(form.trap.name, "website");
    assert.equal(form.trap.tabIndex, -1);
    assert.equal(form.trap.attrs.autocomplete, "off");
    assert.equal(b.all((n) => n.tag === "h3")[0].textContent, "Tickets");
    for (const l of b.all((n) => n.tag === "label")) assert.ok(b.all((n) => n.id === l.htmlFor).length === 1);
    assert.equal(b.all((n) => n.tag === "form").length, 0);
  }

  // unusable boxes build nothing
  assert.equal(setup({ attrs: { "data-api": "//evil.test/x" } }).form, null);
  assert.equal(setup({ attrs: { "data-token-api": "" } }).form, null);
  assert.equal(setup({ rows: [] }).form, null);
  // no ticket types: the box, with its "Turn on JavaScript" note, is hidden
  assert.equal(setup({ rows: [] }).b.hidden, true);
  assert.equal(setup().b.hidden, false);

  // booking closed: the token route answers 404
  {
    const { b, calls } = setup({ token: answer(404, null) });
    await tick();
    assert.equal(calls.length, 1);
    assert.equal(b.all((n) => n.tag === "input" || n.tag === "button").length, 0);
    assert.equal(b.children[0].textContent, "Booking is closed.");
  }

  // payload for two types, with answers, token and an empty honeypot
  {
    const { b, form, calls } = setup({ rows: threeRows(), token: answer(200, THREE) });
    await tick();
    const plus = buttons(b, "plus");
    plus[0].click();
    plus[0].click();
    plus[1].click();
    fillBuyer(form);
    form.inputs.diet.value = "vegan";
    await form.submit("");
    const sent = posts(calls);
    assert.equal(sent.length, 1);
    assert.equal(sent[0].url, "/p/event-tickets/rsvp");
    assert.equal(sent[0].opts.headers["Content-Type"], "application/json");
    assert.deepEqual(JSON.parse(sent[0].opts.body), {
      event: "e1",
      items: [{ type: 5, qty: 2 }, { type: 8, qty: 1 }],
      name: "Ada",
      email: "ada@example.org",
      answers: { diet: "vegan", terms: "yes" },
      token: TOKEN.token,
      website: "",
    });
    assert.equal(form.status.textContent, "Booked. Check your email.");
    // one link per ticket, counts back to zero, the form is ready again
    const a = form.links.all((n) => n.tag === "a");
    assert.equal(a.length, 1);
    assert.equal(a[0].textContent, "Ticket 1");
    assert.equal(a[0].getAttribute("href"), "/p/event-tickets/ticket/a.b");
    assert.deepEqual(Object.values(form.qty), [0, 0]);
    assert.equal(form.send.disabled, false);
  }

  // the order never exceeds 10 tickets
  {
    const { b, form } = setup({ rows: threeRows(), token: answer(200, THREE) });
    await tick();
    const plus = buttons(b, "plus");
    for (let i = 0; i < 12; i++) plus[0].click();
    assert.equal(form.qty["5"], 8); // the type has 8 left
    for (let i = 0; i < 12; i++) plus[1].click();
    assert.equal(form.qty["8"], 2);
    assert.equal(form.qty["5"] + form.qty["8"], 10);
    const minus = buttons(b, "minus");
    minus[0].click();
    assert.equal(form.qty["5"], 7);
    for (let i = 0; i < 20; i++) minus[1].click();
    assert.equal(form.qty["8"], 0);
  }

  // the waitlist: its own payload, no items
  {
    const { b, form, calls } = setup({ post: answer(200, { ok: true, waitlisted: true }) });
    await tick();
    fillBuyer(form);
    await buttons(b, "join")[0].click();
    const sent = posts(calls);
    assert.equal(sent.length, 1);
    const body = JSON.parse(sent[0].opts.body);
    assert.equal(body.waitlist_type, 6);
    assert.ok(!("items" in body));
    assert.equal(form.status.textContent, "You are on the waitlist. We will email you if a seat frees up.");
    assert.equal(form.links.children.length, 0);
  }

  // the honeypot is copied as it is
  {
    const { b, form, calls } = setup();
    await tick();
    buttons(b, "plus")[0].click();
    fillBuyer(form);
    form.trap.value = "spam";
    await form.submit("");
    assert.equal(JSON.parse(posts(calls)[0].opts.body).website, "spam");
  }

  // missing buyer, required answer or seat: marked, nothing posted
  {
    const { b, form, calls } = setup();
    await tick();
    await form.submit("");
    assert.equal(form.status.textContent, "Please check the highlighted fields.");
    assert.ok(form.nameInput.classes.has("invalid") && form.emailInput.classes.has("invalid") && form.inputs.terms.classes.has("invalid"));
    fillBuyer(form); // good buyer, but no seat chosen
    await form.submit("");
    assert.equal(form.status.textContent, "Please check the highlighted fields.");
    assert.ok(!form.nameInput.classes.has("invalid"));
    assert.equal(posts(calls).length, 0);
    buttons(b, "plus")[0].click();
  }

  // messages per status
  for (const [status, body, text] of [
    [409, { ok: false, error: "sold out" }, "Sold out"],
    [422, { ok: false }, "Please check the highlighted fields."],
    [404, null, "Booking is closed."],
    [429, { ok: false }, "Try again later."],
    [503, null, "Try again later."],
  ]) {
    const { b, form, calls } = setup({ post: answer(status, body) });
    await tick();
    buttons(b, "plus")[0].click();
    fillBuyer(form);
    await form.submit("");
    if (status === 404) {
      assert.equal(b.children[0].textContent, text);
    } else {
      assert.equal(form.status.textContent, text);
    }
    if (status === 409) {
      // fresh seat counts are fetched after a sold out answer
      assert.equal(calls.filter((c) => c.url.startsWith("/p/event-tickets/token")).length, 2);
    }
    assert.equal(form.send.disabled, false);
  }

  // a network error is plain too
  {
    const { b, form } = setup({ post: new Error("offline") });
    await tick();
    buttons(b, "plus")[0].click();
    fillBuyer(form);
    await form.submit("");
    assert.equal(form.status.textContent, "Try again later.");
  }

  // an unsafe ticket path is dropped, a safe one kept
  {
    const { b, form } = setup({ post: answer(200, { ok: true, tickets: ["//evil.test/t", "javascript:x", "/p/event-tickets/ticket/ok.1", 7] }) });
    await tick();
    buttons(b, "plus")[0].click();
    fillBuyer(form);
    await form.submit("");
    const a = form.links.all((n) => n.tag === "a");
    assert.equal(a.length, 1);
    assert.equal(a[0].getAttribute("href"), "/p/event-tickets/ticket/ok.1");
  }

  // the post waits until the token is 3.1 s old
  {
    const { b, form, calls, now } = setup();
    await tick();
    buttons(b, "plus")[0].click();
    fillBuyer(form);
    const t0 = now();
    await form.submit("");
    assert.equal(posts(calls).length, 1);
    assert.ok(now() - t0 >= 0 && now() >= 1_000_000 + 3100);
  }

  // an old token is renewed before the post
  {
    const { b, form, calls, advance } = setup();
    await tick();
    buttons(b, "plus")[0].click();
    fillBuyer(form);
    advance(51 * 60 * 1000);
    await form.submit("");
    assert.equal(calls.filter((c) => c.url.startsWith("/p/event-tickets/token")).length, 2);
    assert.equal(posts(calls).length, 1);
  }

  // the redirect allowlist: https, a provider checkout host, no login part, no port
  const SL = "/".repeat(2);
  for (const ok of ["https:" + SL + "checkout.stripe.com/c/pay/cs_test_1", "https:" + SL + "www.paypal.com/checkoutnow?token=A1", "https:" + SL + "www.sandbox.paypal.com/checkoutnow?token=A1"]) {
    assert.equal(s.redirectOK(ok), true, ok);
  }
  for (const bad of [
    "http:" + SL + "checkout.stripe.com/c/pay/x", "https:" + SL + "evil.test/c/pay/x", "https:" + SL + "checkout.stripe.com.evil.test/x",
    "https:" + SL + "checkout.stripe.com@evil.test/x", "https:" + SL + "user:pw@checkout.stripe.com/x", "https:" + SL + "evil.test\\@checkout.stripe.com/x",
    "https:" + SL + "checkout.stripe.com:8443/x", "https:" + SL + "paypal.com/x", "https:" + SL + "api.stripe.com/v1/x", "javascript:alert(1)",
    "data:text/html,x", SL + "checkout.stripe.com/x", "/p/event-tickets/ticket/a.b", "checkout.stripe.com", "", null, undefined, 42, {}, ["https:" + SL + "checkout.stripe.com/x"],
    "https:" + SL + "checkout.stripe.com./x", "https:" + SL + "CHECKOUT.STRIPE.COM.evil.test/x", " https:" + SL + "checkout.stripe.com/x",
  ]) {
    assert.equal(s.redirectOK(bad), false, String(bad));
  }
  assert.equal(s.swapRoute("/p/event-tickets/rsvp", "quote"), "/p/event-tickets/quote");
  assert.equal(s.swapRoute("/p/event-tickets/other", "quote"), "");

  // the payload of a payment adds the provider and the coupon and nothing about a price
  {
    const body = s.buildPayload({ event: "e1", qty: { 5: 2 }, name: "A", email: "a@b.c", token: "t", provider: "stripe", coupon: "SAVE10" });
    assert.deepEqual(Object.keys(body).sort(), ["answers", "coupon", "email", "event", "items", "name", "provider", "token", "website"]);
    assert.ok(!("provider" in s.buildPayload({ event: "e1", qty: { 5: 1 } })), "the free payload is unchanged");
  }

  // a free event never shows a payment part
  {
    const { b, form } = setup();
    await tick();
    buttons(b, "plus")[0].click();
    assert.equal(b.all((n) => n.className === "pay-box").length, 0);
    assert.equal(form.send.hidden, false);
  }

  // a paid cart: totals come from the server, two pay buttons, Reserve is hidden
  {
    const { b, form, calls, gone } = setup({ rows: threeRows(), token: answer(200, PAID), post: answer(200, { ok: true, redirect: "https:" + SL + "checkout.stripe.com/c/pay/cs_test_1" }) });
    await tick();
    const plus = buttons(b, "plus");
    plus[1].click(); // the free type: no payment part yet
    assert.equal(b.all((n) => n.className === "pay-box").length, 0);
    plus[0].click(); // the priced type
    await tick();
    const box = b.all((n) => n.className === "pay-box")[0];
    assert.ok(box && !box.hidden);
    assert.equal(form.send.hidden, true);
    const quoteCalls = calls.filter((c) => c.url === "/p/event-tickets/quote");
    assert.ok(quoteCalls.length >= 1);
    assert.deepEqual(JSON.parse(quoteCalls[quoteCalls.length - 1].opts.body), { event: "e1", items: [{ type: 5, qty: 1 }, { type: 8, qty: 1 }], coupon: "" });
    const lines = b.all((n) => n.className === "line").map((n) => n.children.map((k) => k.textContent).join(": "));
    assert.deepEqual(lines, ["Subtotal: 20.00 USD", "Discount: -2.00 USD", "Fee: 0.30 USD", "Total: 18.30 USD"]);
    assert.deepEqual(buttons(b, "pay").map((n) => n.textContent), ["Pay with Stripe", "Pay with PayPal"]);
    assert.equal(b.all((n) => n.tag === "label").map((n) => n.textContent).includes("Coupon"), true);

    // a coupon is sent to the quote and to the checkout; an unknown one is said so
    const coupon = b.all((n) => n.tag === "input" && n.name === "coupon")[0];
    coupon.value = "nope";
    coupon.handlers.input();
    await tick();
    const last = () => calls.filter((c) => c.url === "/p/event-tickets/quote").pop();
    assert.equal(JSON.parse(last().opts.body).coupon, "nope");
    assert.equal(b.querySelector(".coupon-note").textContent, "");
    const badCoupon = answer(200, { ...QUOTE, coupon_ok: false });
    const again = setup({ rows: threeRows(), token: answer(200, PAID), quote: badCoupon });
    await tick();
    buttons(again.b, "plus")[0].click();
    again.b.all((n) => n.name === "coupon")[0].value = "nope";
    again.b.all((n) => n.name === "coupon")[0].handlers.input();
    await tick();
    assert.equal(again.b.querySelector(".coupon-note").textContent, s.COUPON_BAD);

    // paying: the checkout post, then the provider's page
    fillBuyer(form);
    await buttons(b, "pay")[0].click();
    const sent = posts(calls).filter((c) => c.url === "/p/event-tickets/checkout");
    assert.equal(sent.length, 1);
    const body = JSON.parse(sent[0].opts.body);
    assert.equal(body.provider, "stripe");
    assert.equal(body.coupon, "nope");
    assert.deepEqual(body.items, [{ type: 5, qty: 1 }, { type: 8, qty: 1 }]);
    assert.ok(!/price|amount|total|unit/.test(Object.keys(body).join()), "no price key is ever sent");
    assert.deepEqual(gone, ["https:" + SL + "checkout.stripe.com/c/pay/cs_test_1"]);
    assert.equal(form.status.textContent, "Taking you to the payment page...");
    assert.equal(posts(calls).filter((c) => c.url === "/p/event-tickets/rsvp").length, 0, "a paid cart never goes to the RSVP route");
  }

  // the PayPal button posts provider paypal
  {
    const { b, form, calls, gone } = setup({ token: answer(200, PAID), post: answer(200, { ok: true, redirect: "https:" + SL + "www.paypal.com/checkoutnow?token=A1" }) });
    await tick();
    buttons(b, "plus")[0].click();
    await tick();
    fillBuyer(form);
    await buttons(b, "pay")[1].click();
    assert.equal(JSON.parse(posts(calls).pop().opts.body).provider, "paypal");
    assert.equal(gone.length, 1);
  }

  // a page that is not a provider's is never opened
  for (const evil of ["https:" + SL + "evil.test/pay", "http:" + SL + "checkout.stripe.com/x", "javascript:alert(1)", "/p/event-tickets/ticket/a.b", 7]) {
    const { b, form, gone } = setup({ token: answer(200, PAID), post: answer(200, { ok: true, redirect: evil }) });
    await tick();
    buttons(b, "plus")[0].click();
    await tick();
    fillBuyer(form);
    await buttons(b, "pay")[0].click();
    assert.deepEqual(gone, [], String(evil));
    if (typeof evil === "string") assert.equal(form.status.textContent, "Try again later.", String(evil));
    assert.equal(buttons(b, "pay")[0].disabled, false, "the buttons work again");
  }

  // a missing buyer stops the payment before any post
  {
    const { b, form, calls } = setup({ token: answer(200, PAID) });
    await tick();
    buttons(b, "plus")[0].click();
    await tick();
    await buttons(b, "pay")[0].click();
    assert.equal(form.status.textContent, "Please check the highlighted fields.");
    assert.equal(posts(calls).filter((c) => c.url !== "/p/event-tickets/quote").length, 0);
  }

  // answers of the checkout: a price that changed, a coupon that went, sold out, closed, too many
  for (const [status, body, text] of [
    [409, { ok: false, error: "changed", changed: [5] }, s.CHANGED],
    [409, { ok: false, error: "coupon" }, s.COUPON_BAD],
    [409, { ok: false, error: "sold out", sold_out: [5] }, "Sold out"],
    [422, { ok: false }, "Please check the highlighted fields."],
    [429, { ok: false }, "Try again later."],
    [503, null, "Try again later."],
  ]) {
    const { b, form, calls, gone } = setup({ token: answer(200, PAID), post: answer(status, body) });
    await tick();
    buttons(b, "plus")[0].click();
    await tick();
    fillBuyer(form);
    await buttons(b, "pay")[0].click();
    assert.equal(form.status.textContent, text, String(status));
    assert.deepEqual(gone, []);
    if (status === 409) assert.equal(calls.filter((c) => c.url.startsWith("/p/event-tickets/token")).length, 2, "fresh prices are fetched");
  }
  {
    const { b, form } = setup({ token: answer(200, PAID), post: answer(404, null) });
    await tick();
    buttons(b, "plus")[0].click();
    await tick();
    fillBuyer(form);
    await buttons(b, "pay")[0].click();
    assert.equal(b.children[0].textContent, "Booking is closed.");
  }

  // a coupon that covers everything: the checkout answers tickets, not a page
  {
    const { b, form, gone } = setup({ token: answer(200, PAID), post: answer(200, { ok: true, tickets: ["/p/event-tickets/ticket/a.b", "//evil.test/t"] }) });
    await tick();
    buttons(b, "plus")[0].click();
    await tick();
    fillBuyer(form);
    await buttons(b, "pay")[0].click();
    assert.equal(form.status.textContent, "Booked. Check your email.");
    assert.deepEqual(form.links.all((n) => n.tag === "a").map((n) => n.getAttribute("href")), ["/p/event-tickets/ticket/a.b"]);
    assert.deepEqual(gone, []);
    assert.equal(form.send.hidden, false, "back to the plain form");
  }

  // no provider set up: a note instead of buttons
  {
    const { b } = setup({ token: answer(200, { ...PAID, providers: [] }) });
    await tick();
    buttons(b, "plus")[0].click();
    await tick();
    assert.equal(buttons(b, "pay").length, 0);
    assert.equal(b.all((n) => n.textContent === s.NO_PAY).length, 1);
    // a provider name the island does not know gets no button
    const odd = setup({ token: answer(200, { ...PAID, providers: ["stripe", "evilpay"] }) });
    await tick();
    buttons(odd.b, "plus")[0].click();
    await tick();
    assert.deepEqual(buttons(odd.b, "pay").map((n) => n.textContent), ["Pay with Stripe"]);
  }

  // two quick clicks leave one set of totals (an older quote answer is dropped)
  {
    const slow = setup({ token: answer(200, PAID) });
    await tick();
    buttons(slow.b, "plus")[0].click();
    buttons(slow.b, "plus")[0].click();
    await tick();
    const lines = slow.b.all((n) => n.className === "line");
    assert.equal(lines.length, 4);
  }

  // seat picker: the seats event locks the seated type, the hold goes to the checkout only
  {
    const bus = { handlers: {}, sent: [] };
    const { b, form, calls } = setup({ bus, rows: threeRows(), token: answer(200, PAID), post: answer(200, { ok: true, redirect: "https://checkout.stripe.com/c/x" }) });
    await tick();
    assert.deepEqual(bus.sent[0], { name: "event-tickets:hello", detail: {} });
    buttons(b, "plus")[0].click(); // an unpicked count on the seated type (best available)
    bus.handlers["event-tickets:seats"]({ detail: { hold: "H.1", counts: { 5: 2 }, seated: [5] } });
    await tick();
    assert.equal(form.qty[5], 2);
    assert.equal(buttons(b, "plus")[0].disabled, true);
    assert.equal(buttons(b, "minus")[0].disabled, true);
    assert.equal(buttons(b, "plus")[1].disabled, false, "unseated types stay free");
    assert.equal(b.all((n) => n.className === "count")[0].textContent, "2");
    fillBuyer(form);
    await buttons(b, "pay")[0].click();
    const sent = JSON.parse(posts(calls).at(-1).opts.body);
    assert.equal(sent.hold, "H.1");
    assert.deepEqual(sent.items, [{ type: 5, qty: 2 }]);
    // a released hold frees the stepper again and the count goes to zero
    bus.handlers["event-tickets:seats"]({ detail: { hold: "", counts: {}, seated: [5] } });
    assert.equal(form.qty[5], 0);
    assert.equal(buttons(b, "plus")[0].disabled, false);
    // the pay event focuses the name field
    let focused = false;
    form.nameInput.focus = () => { focused = true; };
    bus.handlers["event-tickets:pay"]();
    assert.ok(focused);
  }

  // the RSVP payload never carries a hold
  assert.equal("hold" in s.buildPayload({ event: "e1", qty: { 5: 1 }, hold: "H.1" }), false);
  assert.equal(s.buildPayload({ event: "e1", qty: { 5: 1 }, provider: "stripe", hold: "H.1" }).hold, "H.1");
  assert.equal("hold" in s.buildPayload({ event: "e1", qty: { 5: 1 }, provider: "stripe", hold: "" }), false);

  // a lapsed hold: 409 "hold" says so and asks the picker to start over
  {
    const bus = { handlers: {}, sent: [] };
    const { b, form } = setup({ bus, token: answer(200, PAID), post: answer(409, { ok: false, error: "hold" }) });
    await tick();
    bus.handlers["event-tickets:seats"]({ detail: { hold: "H.1", counts: { 5: 1 }, seated: [5] } });
    await tick();
    fillBuyer(form);
    await buttons(b, "pay")[0].click();
    assert.equal(form.status.textContent, "Your seat hold ended. Please pick your seats again.");
    assert.equal(bus.sent.at(-1).name, "event-tickets:refresh");
    assert.equal(buttons(b, "plus")[0].disabled, false);
  }

  console.log("tickets island ok");
})().catch((e) => { console.error(e); process.exit(1); });
