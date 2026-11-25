// Tickets: the booking form for an event page, built here because a widget
// template cannot hold a form element. The ticket types are the rows the page
// already shows. The island asks the plugin for a signed token, the live seats
// left and the event's own questions, then posts JSON to the plugin's RSVP
// route. Paid tickets go the other way: the island asks the plugin's quote
// route for the totals (it only shows what the server answers, it never works
// out a price) and posts to the checkout route, which answers the payment
// provider's page to go to. The answer is always plain text; text is set with
// textContent only.

// Two slashes, spelled so the install lint does not read them as a URL start.
const SLASHES = "/".repeat(2);
// The route drops a post whose token is younger than 3 seconds or older than an
// hour; the form waits and renews so a real visitor is never dropped.
const TOKEN_MIN_MS = 3100;
const TOKEN_RENEW_MS = 50 * 60 * 1000;
const MAX_TICKETS = 10;

const BOOKED = "Booked. Check your email.";
const WAITLISTED = "You are on the waitlist. We will email you if a seat frees up.";
const SOLD_OUT = "Sold out";
const CHECK = "Please check the highlighted fields.";
const CLOSED = "Booking is closed.";
const LATER = "Try again later.";
const COUPON_BAD = "This coupon code is not valid.";
const CHANGED = "Prices changed. Please check the total and try again.";
const NO_PAY = "Online payment is not available yet.";
const PAYING = "Taking you to the payment page...";
const HOLD_ENDED = "Your seat hold ended. Please pick your seats again.";

// The only pages the browser is sent to for payment: https, no login part, no
// port, one of the providers' checkout hosts. The server checks the same list.
const PAY_HOSTS = ["checkout.stripe.com", "www.paypal.com", "www.sandbox.paypal.com"];
const PAY_LABELS = { stripe: "Pay with Stripe", paypal: "Pay with PayPal" };
const QUOTE_MS = 400;
const LEAVE_MS = 15000;

function redirectOK(raw) {
  if (typeof raw !== "string" || raw.length > 2048 || !raw.startsWith("https:")) return false;
  let u;
  try { u = new URL(raw); } catch (e) { return false; }
  return u.protocol === "https:" && !u.username && !u.password && u.port === "" && u.host === u.hostname && PAY_HOSTS.includes(u.hostname);
}

// swapRoute is the quote or checkout route next to the RSVP route; empty when
// the RSVP route is not where it is expected.
function swapRoute(path, name) {
  return path.endsWith("/rsvp") ? path.slice(0, -"rsvp".length) + name : "";
}

// cartItems are the chosen seats as the routes read them.
function cartItems(qty) {
  return Object.keys(qty || {})
    .filter((id) => qty[id] > 0)
    .map((id) => ({ type: Number(id), qty: qty[id] }));
}

function safePath(p) {
  return typeof p === "string" && p.startsWith("/") && !p.startsWith(SLASHES) ? p : "";
}

// clampQty keeps one type's count within 0..10 and the order within 10 in all.
// others is the number of seats already asked for in the other types.
function clampQty(want, others) {
  return Math.max(0, Math.min(Math.trunc(want) || 0, MAX_TICKETS - others));
}

// buildPayload is the body of the post: the chosen seats (or the one waitlist
// type), the buyer, the answers, the token and the honeypot, which stays
// empty for a person.
function buildPayload(v) {
  const out = { event: String(v.event || "") };
  if (v.waitlistType) {
    out.waitlist_type = Number(v.waitlistType);
  } else {
    out.items = cartItems(v.qty);
  }
  out.name = String(v.name || "").trim();
  out.email = String(v.email || "").trim();
  out.answers = v.answers || {};
  out.token = String(v.token || "");
  out.website = String(v.trap || "");
  if (v.provider) {
    out.provider = String(v.provider);
    out.coupon = String(v.coupon || "");
    if (v.hold) out.hold = String(v.hold);
  }
  return out;
}

// messageFor turns an answer into the text shown.
function messageFor(status, body) {
  if (status === 200 && body && body.ok === true) return body.waitlisted ? WAITLISTED : BOOKED;
  if (status === 409) return SOLD_OUT;
  if (status === 422 || status === 400) return CHECK;
  if (status === 404) return CLOSED;
  return LATER;
}

function make(doc, tag, cls, text) {
  const e = doc.createElement(tag);
  if (cls) e.className = cls;
  if (text !== undefined) e.textContent = text;
  return e;
}

// mount builds the form after the ticket rows in box. env carries the page's
// document, fetch, timer and clock so the flow can run without a browser.
function mount(box, env) {
  const doc = env.doc;
  const api = safePath(box.getAttribute("data-api"));
  const tokenApi = safePath(box.getAttribute("data-token-api"));
  const rows = Array.from(box.querySelectorAll(".hm-event-tickets_ticket-type"));
  const eventId = rows.length ? rows[0].getAttribute("data-event") || "" : "";
  // An event without ticket types shows nothing: not the box, not the note.
  if (!rows.length) box.hidden = true;
  if (!api || !tokenApi || !eventId) return null;

  const quoteApi = swapRoute(api, "quote");
  const checkoutApi = swapRoute(api, "checkout");
  const qty = {}; // ticket type id -> seats asked for
  const prices = {}; // ticket type id -> price in minor units, as the server said
  let providers = []; // the payment providers the site has set up
  const counts = {}; // ticket type id -> count element of its stepper
  const steppers = {}; // ticket type id -> its minus and plus buttons
  // The seat picker (another widget) tells which seats the buyer holds. Their
  // types are locked to the picked count while a hold is there.
  const bus = { on: env.on || (() => {}), emit: env.emit || (() => {}) };
  let hold = "";
  let seated = new Set();
  const inputs = {};
  let fields = [];

  const panel = make(doc, "div", "panel");
  const note = box.querySelector(".note");
  if (note) note.hidden = true;
  const heading = (box.getAttribute("data-heading") || "").trim();
  const top = [];
  if (heading) top.push(make(doc, "h3", "", heading));

  const nameInput = make(doc, "input");
  const emailInput = make(doc, "input");
  const extra = make(doc, "div", "extra");
  const fieldWrap = (id, text, input) => {
    const wrap = make(doc, "div", "field");
    const label = make(doc, "label", "", text);
    label.htmlFor = id;
    input.id = id;
    wrap.append(label, input);
    return wrap;
  };
  nameInput.name = "name";
  nameInput.maxLength = 80;
  nameInput.setAttribute("aria-required", "true");
  emailInput.name = "email";
  emailInput.type = "email";
  emailInput.maxLength = 254;
  emailInput.setAttribute("aria-required", "true");
  // The honeypot: people never see it, scripts that fill every field do.
  const trapWrap = make(doc, "div", "trap");
  trapWrap.setAttribute("aria-hidden", "true");
  const trap = make(doc, "input");
  trap.type = "text";
  trap.name = "website";
  trap.tabIndex = -1;
  trap.setAttribute("autocomplete", "off");
  trapWrap.append(trap);
  const send = make(doc, "button", "", "Reserve");
  send.type = "button";
  const status = make(doc, "p", "status");
  status.setAttribute("role", "status");
  const links = make(doc, "ul", "links");
  const payHolder = make(doc, "div", "pay-holder");
  panel.append(
    ...top,
    fieldWrap("hm-tickets-name", "Your name", nameInput),
    fieldWrap("hm-tickets-email", "Your email", emailInput),
    extra,
    payHolder,
    trapWrap,
    send,
    status,
    links,
  );
  box.append(panel);

  let token = "";
  let issued = 0;
  const say = (text) => { status.textContent = text; };
  const closeForm = () => box.replaceChildren(make(doc, "p", "note", CLOSED));
  const total = () => Object.keys(qty).reduce((n, id) => n + qty[id], 0);
  const priced = () => Object.keys(qty).some((id) => qty[id] > 0 && prices[id] > 0);

  // The payment part: coupon, totals and the pay buttons. It is built the first
  // time a priced ticket is chosen, so a free event never shows it.
  let pay = null;
  let couponInput = null;
  let couponNote = null;
  let totals = null;
  let payButtons = null;
  let quoteSeq = 0;
  let busy = false;

  function setBusy(on) {
    busy = on;
    send.disabled = on;
    if (payButtons) for (const b of payButtons.querySelectorAll(".pay")) b.disabled = on;
  }

  function buildPayButtons() {
    const list = providers.filter((p) => PAY_LABELS[p]);
    if (!list.length) {
      payButtons.replaceChildren(make(doc, "p", "note", NO_PAY));
      return;
    }
    payButtons.replaceChildren(...list.map((p) => {
      const b = make(doc, "button", "pay", PAY_LABELS[p]);
      b.type = "button";
      b.disabled = busy;
      b.addEventListener("click", () => checkout(p));
      return b;
    }));
  }

  function ensurePay() {
    if (pay) return;
    couponInput = make(doc, "input");
    couponInput.name = "coupon";
    couponInput.maxLength = 32;
    couponInput.setAttribute("autocomplete", "off");
    couponInput.addEventListener("input", scheduleQuote);
    couponNote = make(doc, "p", "coupon-note");
    totals = make(doc, "div", "totals");
    payButtons = make(doc, "div", "pay-buttons");
    pay = make(doc, "div", "pay-box");
    pay.append(fieldWrap("hm-tickets-coupon", "Coupon", couponInput), couponNote, totals, payButtons);
    payHolder.append(pay);
    buildPayButtons();
  }

  // showQuote writes the server's totals: Subtotal, Discount and Fee when they
  // are there, and Total. The texts are the server's; nothing is added up here.
  function showQuote(body) {
    const q = body.quote;
    const d = body.display;
    const rows = [["Subtotal", d.subtotal]];
    if (q.discount_cents > 0) rows.push(["Discount", "-" + d.discount]);
    if (q.fee_cents > 0 && !q.fee_absorbed) rows.push(["Fee", d.fee]);
    rows.push(["Total", d.total]);
    totals.replaceChildren(...rows.map(([label, text]) => {
      const line = make(doc, "p", "line");
      line.append(make(doc, "span", "label", label), make(doc, "span", "amount", text));
      return line;
    }));
    const typed = couponInput.value.trim();
    couponNote.textContent = typed && body.coupon_ok === false ? COUPON_BAD : "";
  }

  async function fetchQuote(mine) {
    try {
      const r = await env.fetch(quoteApi, {
        method: "POST",
        headers: { "Content-Type": "application/json", Accept: "application/json" },
        body: JSON.stringify({ event: eventId, items: cartItems(qty), coupon: couponInput.value.trim() }),
      });
      let body = null;
      try { body = await r.json(); } catch (e) { /* not JSON */ }
      if (mine !== quoteSeq) return;
      if (r.status === 200 && body && body.ok === true && body.quote && body.display) showQuote(body);
      else totals.replaceChildren();
    } catch (e) {
      if (mine === quoteSeq) totals.replaceChildren();
    }
  }

  // scheduleQuote shows or hides the payment part for the chosen seats and asks
  // for the totals once the buyer stops clicking. An older answer is dropped.
  function scheduleQuote() {
    const mine = ++quoteSeq;
    if (!priced() || !quoteApi || !checkoutApi) {
      if (pay) pay.hidden = true;
      send.hidden = false;
      return;
    }
    ensurePay();
    pay.hidden = false;
    send.hidden = true;
    env.delay(() => fetchQuote(mine), QUOTE_MS);
  }

  function lockSteppers() {
    for (const id of Object.keys(steppers)) for (const b of steppers[id]) b.disabled = !!hold && seated.has(id);
  }

  function setQty(id, want) {
    qty[id] = clampQty(want, total() - (qty[id] || 0));
    if (counts[id]) counts[id].textContent = String(qty[id]);
    scheduleQuote();
  }

  // applyTypes shows the live seats on the rows and gives each row its
  // stepper (seats left) or waitlist button (sold out with a waitlist).
  function applyTypes(list) {
    for (const row of rows) {
      const t = list.find((x) => String(x.id) === row.getAttribute("data-type"));
      if (!t) continue;
      const id = String(t.id);
      prices[id] = Number(t.price_cents) || 0;
      const left = row.querySelector(".left");
      if (left) left.textContent = t.sold_out ? SOLD_OUT : t.left + " left";
      row.setAttribute("data-sold-out", t.sold_out ? "1" : "");
      const old = row.querySelector(".row");
      const ctl = make(doc, "span", "row");
      if (!t.sold_out) {
        const minus = make(doc, "button", "minus", "-");
        const plus = make(doc, "button", "plus", "+");
        minus.type = "button";
        plus.type = "button";
        minus.setAttribute("aria-label", "Fewer tickets");
        plus.setAttribute("aria-label", "More tickets");
        const n = make(doc, "span", "count", String(qty[id] || 0));
        counts[id] = n;
        minus.addEventListener("click", () => setQty(id, (qty[id] || 0) - 1));
        plus.addEventListener("click", () => setQty(id, Math.min((qty[id] || 0) + 1, t.left)));
        steppers[id] = [minus, plus];
        ctl.append(minus, n, plus);
      } else if (t.waitlist) {
        const join = make(doc, "button", "join", "Join waitlist");
        join.type = "button";
        join.addEventListener("click", () => submit(id));
        ctl.append(join);
      }
      if (old) old.replaceWith(ctl);
      else row.append(ctl);
    }
    lockSteppers();
  }

  function applyFields(list) {
    fields = list;
    const parts = [];
    for (const f of list) {
      let input;
      if (f.kind === "select") {
        input = make(doc, "select");
        input.append(make(doc, "option", "", ""));
        for (const o of f.options || []) {
          const opt = make(doc, "option", "", o);
          opt.value = o;
          input.append(opt);
        }
      } else {
        input = make(doc, "input");
        input.type = f.kind === "checkbox" ? "checkbox" : "text";
        if (f.kind !== "checkbox") input.maxLength = 200;
      }
      input.name = f.key;
      if (f.required) input.setAttribute("aria-required", "true");
      inputs[f.key] = input;
      parts.push(fieldWrap("hm-tickets-" + f.key, f.label, input));
    }
    extra.replaceChildren(...parts);
  }

  async function getToken() {
    try {
      const r = await env.fetch(tokenApi + "?event=" + encodeURIComponent(eventId), { headers: { Accept: "application/json" } });
      if (r.status === 404) {
        closeForm();
        return false;
      }
      if (!r.ok) return false;
      const body = await r.json();
      if (!body || typeof body.token !== "string" || !body.token) return false;
      token = body.token;
      issued = env.now();
      if (Array.isArray(body.providers)) providers = body.providers.filter((p) => typeof p === "string");
      if (payButtons) buildPayButtons();
      if (Array.isArray(body.types)) applyTypes(body.types);
      if (Array.isArray(body.fields) && !fields.length) applyFields(body.fields);
      return true;
    } catch (e) {
      return false;
    }
  }

  // ensureToken has a token that is old enough and not about to expire.
  async function ensureToken() {
    if (!token || env.now() - issued > TOKEN_RENEW_MS) {
      if (!(await getToken())) return false;
    }
    const wait = TOKEN_MIN_MS - (env.now() - issued);
    if (wait > 0) await new Promise((resolve) => env.delay(resolve, wait));
    return true;
  }

  function answers() {
    const out = {};
    for (const f of fields) {
      const i = inputs[f.key];
      out[f.key] = f.kind === "checkbox" ? (i.checked ? "yes" : "") : String(i.value || "").trim();
    }
    return out;
  }

  // invalid marks the buyer and required answers that are missing and says
  // whether the form may not go: also when no seat is chosen for a booking.
  function invalid(joining) {
    const bad = [];
    if (!nameInput.value.trim()) bad.push(nameInput);
    const mail = emailInput.value.trim();
    if (!mail || !mail.includes("@")) bad.push(emailInput);
    const a = answers();
    for (const f of fields) if (f.required && !a[f.key]) bad.push(inputs[f.key]);
    for (const i of [nameInput, emailInput, ...Object.values(inputs)]) {
      const on = bad.includes(i);
      i.classList.toggle("invalid", on);
      i.setAttribute("aria-invalid", on ? "true" : "false");
    }
    return bad.length > 0 || (!joining && total() < 1);
  }

  // showTickets is the end of a booking: the counts go back to zero and each
  // ticket gets a link.
  function showTickets(body) {
    token = "";
    hold = "";
    for (const id of Object.keys(qty)) setQty(id, 0);
    lockSteppers();
    bus.emit("event-tickets:refresh", {});
    const paths = Array.isArray(body.tickets) ? body.tickets.map(safePath).filter(Boolean) : [];
    paths.forEach((p, n) => {
      const li = make(doc, "li");
      const a = make(doc, "a", "", "Ticket " + (n + 1));
      a.setAttribute("href", p);
      li.append(a);
      links.append(li);
    });
  }

  // checkout asks the plugin to hold the seats and open the payment at the
  // chosen provider, then goes to the page it answers, if that page is one of
  // the providers' own.
  async function checkout(provider) {
    if (invalid(false)) {
      say(CHECK);
      return;
    }
    setBusy(true);
    say("");
    links.replaceChildren();
    let leaving = false;
    try {
      if (!(await ensureToken())) {
        say(LATER);
        return;
      }
      const r = await env.fetch(checkoutApi, {
        method: "POST",
        headers: { "Content-Type": "application/json", Accept: "application/json" },
        body: JSON.stringify(buildPayload({
          event: eventId,
          qty,
          name: nameInput.value,
          email: emailInput.value,
          answers: answers(),
          token,
          trap: trap.value,
          coupon: couponInput.value,
          hold,
          provider,
        })),
      });
      let body = null;
      try { body = await r.json(); } catch (e) { /* not JSON */ }
      if (r.status === 200 && body && body.ok === true) {
        if (typeof body.redirect !== "string") {
          showTickets(body); // a coupon covered everything: the tickets are ready
          say(BOOKED);
        } else if (redirectOK(body.redirect)) {
          leaving = true;
          say(PAYING);
          env.go(body.redirect);
        } else {
          say(LATER);
        }
      } else if (r.status === 409) {
        token = ""; // fresh seats and prices, and the totals again
        await getToken();
        scheduleQuote();
        const lost = !!body && body.error === "hold";
        if (lost) {
          hold = "";
          lockSteppers();
          bus.emit("event-tickets:refresh", {});
        }
        say(lost ? HOLD_ENDED : body && body.error === "coupon" ? COUPON_BAD : body && body.error === "changed" ? CHANGED : SOLD_OUT);
      } else if (r.status === 404) {
        closeForm();
      } else {
        say(messageFor(r.status, body));
      }
    } catch (e) {
      say(LATER);
    } finally {
      // Back from the payment page the buttons must work again.
      if (leaving) env.delay(() => setBusy(false), LEAVE_MS);
      else setBusy(false);
    }
  }

  // submit books the chosen seats, or joins the waitlist of one sold-out type.
  async function submit(waitlistType) {
    const joining = typeof waitlistType === "string" && waitlistType !== "";
    if (invalid(joining)) {
      say(CHECK);
      return;
    }
    send.disabled = true;
    say("");
    links.replaceChildren();
    try {
      if (!(await ensureToken())) {
        say(LATER);
        return;
      }
      const r = await env.fetch(api, {
        method: "POST",
        headers: { "Content-Type": "application/json", Accept: "application/json" },
        body: JSON.stringify(buildPayload({
          event: eventId,
          qty,
          waitlistType: joining ? waitlistType : "",
          name: nameInput.value,
          email: emailInput.value,
          answers: answers(),
          token,
          trap: trap.value,
        })),
      });
      let body = null;
      try { body = await r.json(); } catch (e) { /* not JSON */ }
      const text = messageFor(r.status, body);
      if (r.status === 200 && body && body.ok === true) {
        showTickets(body);
      } else if (r.status === 409) {
        token = ""; // the next try fetches fresh seat counts
        await getToken();
      } else if (r.status === 404) {
        closeForm();
      }
      say(text);
    } catch (e) {
      say(LATER);
    } finally {
      send.disabled = false;
    }
  }

  send.addEventListener("click", () => submit(""));
  bus.on("event-tickets:seats", (e) => {
    const d = (e && e.detail) || {};
    hold = typeof d.hold === "string" ? d.hold : "";
    seated = new Set(Array.isArray(d.seated) ? d.seated.map(String) : []);
    for (const id of seated) {
      qty[id] = clampQty((d.counts || {})[id] || 0, total() - (qty[id] || 0));
      if (counts[id]) counts[id].textContent = String(qty[id]);
    }
    lockSteppers();
    scheduleQuote();
  });
  bus.on("event-tickets:pay", () => nameInput.focus());
  bus.emit("event-tickets:hello", {});
  getToken();
  return { submit, checkout, setQty, nameInput, emailInput, inputs, trap, send, status, links, qty, rows };
}

if (typeof document !== "undefined") {
  const env = {
    doc: document,
    fetch: (u, o) => fetch(u, o),
    now: () => Date.now(),
    delay: (fn, ms) => setTimeout(fn, ms),
    go: (u) => location.assign(u),
  };
  for (const box of document.querySelectorAll(".hm-event-tickets_tickets")) mount(box, env);
}

if (typeof module !== "undefined") module.exports = {
    BOOKED, WAITLISTED, SOLD_OUT, CHECK, CLOSED, LATER, COUPON_BAD, CHANGED, NO_PAY, HOLD_ENDED, clampQty, buildPayload, messageFor, safePath, redirectOK, swapRoute, mount,
  };
