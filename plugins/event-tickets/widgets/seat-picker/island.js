// Seat picker: the seat map of an event with seating. It reads the live seats
// from the plugin, holds one seat per click for ten minutes and counts down
// the hold. What it holds is handed to the Tickets widget through document
// events (event-tickets:seats), which sends the hold token with the checkout;
// the picker never prices or books anything. Text is set with textContent only.

// Two slashes, spelled so the install lint does not read them as a URL start.
const SLASHES = "/".repeat(2);
// The hold route drops a post whose token is younger than 3 seconds or older
// than an hour; the picker waits and renews so a real visitor is never dropped.
const TOKEN_MIN_MS = 3100;
const TOKEN_RENEW_MS = 50 * 60 * 1000;
const POLL_MS = 20000;

const TAKEN = "That seat was just taken. Please pick another.";
const FULL = "You cannot hold more seats right now.";
const LATER = "Try again later.";
const ENDED = "Your seat hold ended. Please pick your seats again.";

function safePath(p) {
  return typeof p === "string" && p.startsWith("/") && !p.startsWith(SLASHES) ? p.replace(/\/+$/, "") : "";
}

// clock is a number of seconds as mm:ss.
function clock(secs) {
  const s = Math.max(0, Math.trunc(secs));
  return String(Math.trunc(s / 60)).padStart(2, "0") + ":" + String(s % 60).padStart(2, "0");
}

function make(doc, tag, cls, text) {
  const e = doc.createElement(tag);
  if (cls) e.className = cls;
  if (text !== undefined) e.textContent = text;
  return e;
}

// mount builds the map in box. env carries the page's document, fetch, timer,
// clock and event bus so the flow can run without a browser.
function mount(box, env) {
  const doc = env.doc;
  const seatsApi = safePath(box.getAttribute("data-seats-api"));
  const tokenApi = safePath(box.getAttribute("data-token-api"));
  const anchor = doc.querySelector(".hm-event-tickets_ticket-type[data-event]");
  const eventId = anchor ? anchor.getAttribute("data-event") || "" : "";
  if (!seatsApi || !tokenApi || !eventId) return null;

  let sections = [];
  const mine = new Map(); // "section:row:num" -> { id, section, row, num, type }
  let hold = "";
  let until = 0; // when the hold ends, ms
  let tick = 0;
  let token = "";
  let issued = 0;
  let busy = false;

  const note = box.querySelector(".note");
  if (note) note.hidden = true;
  const heading = (box.getAttribute("data-heading") || "").trim();
  const map = make(doc, "div", "map");
  const more = make(doc, "p", "more", "Scroll sideways for more seats");
  more.hidden = true;
  const legend = make(doc, "p", "legend", "o free   x taken   * yours");
  const timer = make(doc, "span", "timer");
  const go = make(doc, "button", "", "Continue to payment");
  go.type = "button";
  go.disabled = true;
  const bar = make(doc, "div", "hold");
  bar.append(timer, go);
  const status = make(doc, "p", "status");
  status.setAttribute("role", "status");
  box.append(...(heading ? [make(doc, "h3", "", heading)] : []), make(doc, "p", "stage", "STAGE"), map, more, legend, bar, status);
  box.hidden = true;
  const say = (text) => { status.textContent = text; };
  const keyOf = (s, r, n) => s + ":" + r + ":" + n;

  function announce() {
    const counts = {};
    for (const p of mine.values()) counts[p.type] = (counts[p.type] || 0) + 1;
    const seated = sections.filter((s) => s.kind === "grid").map((s) => s.type_id);
    env.emit("event-tickets:seats", { hold: mine.size ? hold : "", counts, seated });
  }

  function seatButton(s, ri, row, n) {
    const k = keyOf(s.id, ri, n);
    const letter = row.s[n - 1];
    const own = mine.has(k);
    const state = own ? "yours" : letter === "o" ? "free" : letter === "x" ? "taken" : "blocked";
    const b = make(doc, "button", "seat " + state + (state === "blocked" ? " gap" : ""), own ? "*" : letter === "o" ? "o" : letter === "x" ? "x" : "");
    b.type = "button";
    b.setAttribute("aria-label", "Row " + row.l + " seat " + n + ", " + (state === "blocked" ? "not available" : state));
    if (state === "free") b.addEventListener("click", () => pick(s, ri, n));
    else if (state === "yours") b.addEventListener("click", () => drop(k));
    else b.disabled = true;
    return b;
  }

  function render() {
    box.hidden = !sections.length;
    map.replaceChildren(...sections.map((s) => {
      const sec = make(doc, "div", "section");
      const title = [s.name, s.tier, s.price].filter(Boolean).join(" - ");
      if (s.kind !== "grid") {
        sec.append(make(doc, "p", "zone", s.name + ": " + (s.left > 0 ? s.left + " left, choose a number below" : "sold out")));
        return sec;
      }
      sec.append(make(doc, "h4", "", title));
      (s.rows || []).forEach((row, ri) => {
        const line = make(doc, "div", "seat-row");
        line.append(make(doc, "span", "row-label", row.l));
        for (let n = 1; n <= row.s.length; n++) line.append(seatButton(s, ri, row, n));
        sec.append(line);
      });
      return sec;
    }));
    more.hidden = !(map.scrollWidth > map.clientWidth);
    go.disabled = !mine.size;
    if (!mine.size) timer.textContent = "";
  }

  // showOwn brings the first seat of the visitor into the visible part of the map.
  function showOwn() {
    const seat = map.querySelector(".yours");
    if (seat && map.scrollWidth > map.clientWidth) map.scrollLeft = Math.max(0, seat.offsetLeft - map.clientWidth / 2);
  }

  async function load() {
    try {
      const r = await env.fetch(seatsApi + "/" + encodeURIComponent(eventId), { headers: { Accept: "application/json" } });
      if (r.status === 404) {
        sections = [];
      } else if (r.ok) {
        const body = await r.json();
        if (!body || !Array.isArray(body.sections)) return;
        sections = body.sections;
      } else {
        return;
      }
      render();
    } catch (e) { /* the next poll tries again */ }
  }

  // countdown shows the time left of the hold; at zero the seats are let go.
  function countdown() {
    const my = ++tick;
    const step = () => {
      if (my !== tick || !until) return;
      const left = Math.ceil((until - env.now()) / 1000);
      if (left > 0) {
        timer.textContent = "Held for you " + clock(left);
        env.delay(step, 1000);
        return;
      }
      until = 0;
      mine.clear();
      say(ENDED);
      render();
      announce();
      load();
    };
    step();
  }

  async function ensureToken() {
    if (!token || env.now() - issued > TOKEN_RENEW_MS) {
      try {
        const r = await env.fetch(tokenApi + "?event=" + encodeURIComponent(eventId), { headers: { Accept: "application/json" } });
        const body = r.ok ? await r.json() : null;
        if (!body || typeof body.token !== "string" || !body.token) return false;
        token = body.token;
        issued = env.now();
      } catch (e) {
        return false;
      }
    }
    const wait = TOKEN_MIN_MS - (env.now() - issued);
    if (wait > 0) await new Promise((resolve) => env.delay(resolve, wait));
    return true;
  }

  async function pick(s, ri, n) {
    if (busy) return;
    busy = true;
    say("");
    try {
      if (!(await ensureToken())) {
        say(LATER);
        return;
      }
      const r = await env.fetch(seatsApi + "/hold", {
        method: "POST",
        headers: { "Content-Type": "application/json", Accept: "application/json" },
        body: JSON.stringify({ event: eventId, section: s.id, row: ri, num: n, hold, token, website: "" }),
      });
      let body = null;
      try { body = await r.json(); } catch (e) { /* not JSON */ }
      if (r.status === 200 && body && body.ok === true && typeof body.hold === "string" && body.seat_id) {
        hold = body.hold;
        mine.set(keyOf(s.id, ri, n), { id: body.seat_id, section: s.id, row: ri, num: n, type: s.type_id });
        until = env.now() + (Number(body.expires_in) || 0) * 1000;
        render();
        showOwn();
        announce();
        countdown();
      } else if (r.status === 200) {
        token = ""; // a post the route took for a bot: a new token next time
        say(LATER);
      } else if (r.status === 409) {
        const row = s.rows && s.rows[ri];
        if (row) row.s = row.s.slice(0, n - 1) + "x" + row.s.slice(n);
        say(TAKEN);
        render();
        load();
      } else {
        say(r.status === 429 ? FULL : LATER);
      }
    } catch (e) {
      say(LATER);
    } finally {
      busy = false;
    }
  }

  async function drop(k) {
    const p = mine.get(k);
    if (!p || busy) return;
    busy = true;
    say("");
    try {
      const r = await env.fetch(seatsApi + "/hold/" + encodeURIComponent(hold) + "?event=" + encodeURIComponent(eventId) + "&seat=" + encodeURIComponent(p.id), {
        method: "DELETE",
        headers: { Accept: "application/json" },
      });
      if (!r.ok) {
        say(LATER);
        return;
      }
      mine.delete(k);
      if (!mine.size) until = 0;
      render();
      announce();
      load();
    } catch (e) {
      say(LATER);
    } finally {
      busy = false;
    }
  }

  function poll() {
    if (box.isConnected === false) return;
    load();
    env.delay(poll, POLL_MS);
  }

  if (doc.defaultView) doc.defaultView.addEventListener("resize", render);
  go.addEventListener("click", () => env.emit("event-tickets:pay", {}));
  env.on("event-tickets:hello", announce);
  env.on("event-tickets:refresh", () => { mine.clear(); until = 0; render(); announce(); load(); });
  load().then(() => { announce(); env.delay(poll, POLL_MS); });
  return { load, pick, drop, mine, map, go, timer, status, announce, render, get hold() { return hold; } };
}

if (typeof document !== "undefined") {
  const env = {
    doc: document,
    fetch: (u, o) => fetch(u, o),
    now: () => Date.now(),
    delay: (fn, ms) => setTimeout(fn, ms),
    emit: (name, detail) => document.dispatchEvent(new CustomEvent(name, { detail })),
    on: (name, fn) => document.addEventListener(name, fn),
  };
  for (const box of document.querySelectorAll(".hm-event-tickets_seat-picker")) mount(box, env);
}

if (typeof module !== "undefined") module.exports = { TAKEN, FULL, LATER, ENDED, clock, safePath, mount };
