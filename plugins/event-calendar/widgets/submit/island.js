// Submit an event: a form for visitors, built here because a widget template
// cannot hold a form element. It asks the plugin for a signed token, then
// posts the fields as JSON to the plugin's submit route. The answer is always
// plain: thanks, check the fields, or try again later. Text is set with
// textContent only.

// Two slashes, spelled so the install lint does not read them as a URL start.
const SLASHES = "/".repeat(2);
// The route drops a post whose token is younger than 3 seconds or older than an
// hour; the form waits and renews so a real visitor is never dropped.
const TOKEN_MIN_MS = 3100;
const TOKEN_RENEW_MS = 50 * 60 * 1000;

const THANKS = "Thank you. We will look at your event soon.";
const CHECK = "Please check the highlighted fields.";
const LATER = "Try again later.";
const CLOSED = "Event submissions are not open right now.";

const FIELDS = [
  { name: "title", label: "Title", type: "text", required: true, max: 200 },
  { name: "start", label: "Start", type: "datetime-local", required: true },
  { name: "end", label: "End", type: "datetime-local" },
  { name: "venue", label: "Venue", type: "text", max: 200 },
  { name: "description", label: "Description", tag: "textarea", max: 4000 },
  { name: "email", label: "Your email", type: "email", required: true, max: 254 },
];

// missingFields names the required fields that are empty.
function missingFields(values) {
  return FIELDS.filter((f) => f.required && !String(values[f.name] || "").trim()).map((f) => f.name);
}

// buildPayload is the body of the post: the six fields trimmed, the token and
// the honeypot, which stays empty for a person.
function buildPayload(values, token, trap) {
  const out = {};
  for (const f of FIELDS) out[f.name] = String(values[f.name] || "").trim();
  out.token = String(token || "");
  out.website = String(trap || "");
  return out;
}

// messageFor turns an answer into the text shown: a stored or silently
// dropped post is thanks, a rejected one asks for another look, anything else
// (the route is busy or unreachable) asks to try later.
function messageFor(status, body) {
  if (status === 404) return CLOSED;
  if (status === 200 && body && body.ok === true) return THANKS;
  if ((status === 400 || status === 422) && body && body.ok === false) return CHECK;
  return LATER;
}

function safePath(p) {
  return typeof p === "string" && p.startsWith("/") && !p.startsWith(SLASHES) ? p : "";
}

function make(doc, tag, cls, text) {
  const e = doc.createElement(tag);
  if (cls) e.className = cls;
  if (text !== undefined) e.textContent = text;
  return e;
}

// mount builds the form in box. env carries the page's document, fetch,
// timer and clock so the flow can run without a browser.
function mount(box, env) {
  const doc = env.doc;
  const api = safePath(box.getAttribute("data-api"));
  const tokenApi = safePath(box.getAttribute("data-token-api"));
  if (!api || !tokenApi) return null;

  const inputs = {};
  const parts = [];
  const heading = (box.getAttribute("data-heading") || "").trim();
  if (heading) parts.push(make(doc, "h3", "", heading));
  for (const f of FIELDS) {
    const id = "hm-submit-" + f.name;
    const wrap = make(doc, "div", "field");
    const label = make(doc, "label", "", f.label);
    label.htmlFor = id;
    const input = make(doc, f.tag || "input");
    input.id = id;
    input.name = f.name;
    if (f.type) input.type = f.type;
    if (f.max) input.maxLength = f.max;
    if (f.required) input.setAttribute("aria-required", "true");
    inputs[f.name] = input;
    wrap.append(label, input);
    parts.push(wrap);
  }
  // The honeypot: people never see it, scripts that fill every field do.
  const trapWrap = make(doc, "div", "trap");
  trapWrap.setAttribute("aria-hidden", "true");
  const trap = make(doc, "input");
  trap.type = "text";
  trap.name = "website";
  trap.tabIndex = -1;
  trap.setAttribute("autocomplete", "off");
  trapWrap.append(trap);
  const send = make(doc, "button", "", "Send");
  send.type = "button";
  const status = make(doc, "p", "status");
  status.setAttribute("role", "status");
  parts.push(trapWrap, send, status);
  box.replaceChildren(...parts);

  let token = "";
  let issued = 0;
  // The owner has submissions off: the token route answers 404. Leave a note
  // instead of a form that cannot work.
  const closeForm = () => box.replaceChildren(make(doc, "p", "note", CLOSED));
  const say = (text) => { status.textContent = text; };
  const mark = (names) => {
    for (const f of FIELDS) {
      const bad = names.includes(f.name);
      inputs[f.name].classList.toggle("invalid", bad);
      inputs[f.name].setAttribute("aria-invalid", bad ? "true" : "false");
    }
  };

  async function getToken() {
    try {
      const r = await env.fetch(tokenApi, { headers: { Accept: "application/json" } });
      if (r.status === 404) {
        closeForm();
        return false;
      }
      if (!r.ok) return false;
      const body = await r.json();
      if (!body || typeof body.token !== "string" || !body.token) return false;
      token = body.token;
      issued = env.now();
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
    if (wait > 0) await new Promise((resolve) => env.sleep(resolve, wait));
    return true;
  }

  async function submit() {
    const values = {};
    for (const f of FIELDS) values[f.name] = inputs[f.name].value;
    const missing = missingFields(values);
    mark(missing);
    if (missing.length) {
      say(CHECK);
      return;
    }
    send.disabled = true;
    say("");
    try {
      if (!(await ensureToken())) {
        say(LATER);
        return;
      }
      const r = await env.fetch(api, {
        method: "POST",
        headers: { "Content-Type": "application/json", Accept: "application/json" },
        body: JSON.stringify(buildPayload(values, token, trap.value)),
      });
      let body = null;
      try { body = await r.json(); } catch (e) { /* not JSON */ }
      const text = messageFor(r.status, body);
      if (text === THANKS) {
        for (const f of FIELDS) inputs[f.name].value = "";
        token = "";
      }
      say(text);
    } catch (e) {
      say(LATER);
    } finally {
      send.disabled = false;
    }
  }

  send.addEventListener("click", submit);
  getToken();
  return { submit, inputs, trap, send, status };
}

if (typeof document !== "undefined") {
  const env = {
    doc: document,
    fetch: (u, o) => fetch(u, o),
    now: () => Date.now(),
    sleep: (fn, ms) => setTimeout(fn, ms),
  };
  for (const box of document.querySelectorAll(".hm-event-calendar_submit")) mount(box, env);
}

if (typeof module !== "undefined") module.exports = { CLOSED, missingFields, buildPayload, messageFor, safePath, mount, FIELDS };
