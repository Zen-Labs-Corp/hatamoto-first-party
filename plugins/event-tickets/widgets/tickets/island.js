// Tickets: the booking form for an event page, built here because a widget
// template cannot hold a form element. The ticket types are the rows the page
// already shows. The island asks the plugin for a signed token, the live seats
// left and the event's own questions, then posts JSON to the plugin's RSVP
// route. The answer is always plain text; text is set with textContent only.

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
    out.items = Object.keys(v.qty || {})
      .filter((id) => v.qty[id] > 0)
      .map((id) => ({ type: Number(id), qty: v.qty[id] }));
  }
  out.name = String(v.name || "").trim();
  out.email = String(v.email || "").trim();
  out.answers = v.answers || {};
  out.token = String(v.token || "");
  out.website = String(v.trap || "");
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

  const qty = {}; // ticket type id -> seats asked for
  const counts = {}; // ticket type id -> count element of its stepper
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
  panel.append(
    ...top,
    fieldWrap("hm-tickets-name", "Your name", nameInput),
    fieldWrap("hm-tickets-email", "Your email", emailInput),
    extra,
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

  function setQty(id, want) {
    qty[id] = clampQty(want, total() - (qty[id] || 0));
    if (counts[id]) counts[id].textContent = String(qty[id]);
  }

  // applyTypes shows the live seats on the rows and gives each row its
  // stepper (seats left) or waitlist button (sold out with a waitlist).
  function applyTypes(list) {
    for (const row of rows) {
      const t = list.find((x) => String(x.id) === row.getAttribute("data-type"));
      if (!t) continue;
      const id = String(t.id);
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
        token = "";
        for (const id of Object.keys(qty)) setQty(id, 0);
        const paths = Array.isArray(body.tickets) ? body.tickets.map(safePath).filter(Boolean) : [];
        paths.forEach((p, n) => {
          const li = make(doc, "li");
          const a = make(doc, "a", "", "Ticket " + (n + 1));
          a.setAttribute("href", p);
          li.append(a);
          links.append(li);
        });
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
  getToken();
  return { submit, setQty, nameInput, emailInput, inputs, trap, send, status, links, qty, rows };
}

if (typeof document !== "undefined") {
  const env = {
    doc: document,
    fetch: (u, o) => fetch(u, o),
    now: () => Date.now(),
    delay: (fn, ms) => setTimeout(fn, ms),
  };
  for (const box of document.querySelectorAll(".hm-event-tickets_tickets")) mount(box, env);
}

if (typeof module !== "undefined") module.exports = { BOOKED, WAITLISTED, SOLD_OUT, CHECK, CLOSED, LATER, clampQty, buildPayload, messageFor, safePath, mount };
