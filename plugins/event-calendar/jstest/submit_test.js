// Run with: node jstest/submit_test.js
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const s = require("../widgets/submit/island.js");

// a minimal DOM: elements with children, attributes, classes and handlers
class Node {
  constructor(tag) {
    this.tag = tag;
    this.children = [];
    this.attrs = {};
    this.handlers = {};
    this.value = "";
    this.textContent = "";
    this.className = "";
    this.disabled = false;
    this.classes = new Set();
    this.classList = { toggle: (c, on) => (on ? this.classes.add(c) : this.classes.delete(c)) };
  }
  append(...kids) { this.children.push(...kids); }
  replaceChildren(...kids) { this.children = kids; }
  setAttribute(k, v) { this.attrs[k] = String(v); }
  getAttribute(k) { return k in this.attrs ? this.attrs[k] : null; }
  addEventListener(type, fn) { this.handlers[type] = fn; }
  all(pred, out = []) {
    if (pred(this)) out.push(this);
    for (const c of this.children) c.all(pred, out);
    return out;
  }
}
const doc = { createElement: (t) => new Node(t) };

function box(attrs = {}) {
  const b = new Node("div");
  Object.assign(b.attrs, { "data-api": "/p/event-calendar/submit", "data-token-api": "/p/event-calendar/submit/token", "data-heading": "Submit an event" }, attrs);
  return b;
}

function answer(status, body) {
  return { ok: status >= 200 && status < 300, status, json: async () => body };
}

// env with a script of answers per address and a clock the test moves
function setup({ attrs, post = answer(200, { ok: true }), token = answer(200, { token: "1700000000000.abcd" }) } = {}) {
  const calls = [];
  let clock = 1_000_000;
  const env = {
    doc,
    now: () => clock,
    fetch: async (url, opts) => {
      calls.push({ url, opts });
      const r = url.endsWith("/token") ? token : post;
      if (r instanceof Error) throw r;
      return r;
    },
    // a sleep moves the clock instead of waiting
    sleep: (fn, ms) => { clock += ms; fn(); },
  };
  const b = box(attrs);
  const form = s.mount(b, env);
  return { b, form, calls, env, advance: (ms) => { clock += ms; } };
}

const tick = () => new Promise((r) => setTimeout(r, 0));

function fill(form, v) {
  for (const [k, val] of Object.entries(v)) form.inputs[k].value = val;
}
const GOOD = { title: " Open mic ", start: "2027-02-20T20:00", end: "", venue: "The Anchor", description: "Bring a song.", email: "visitor@example.org" };

(async () => {
  // pure helpers
  assert.deepEqual(s.missingFields({ title: "x", start: "y", email: "z" }), []);
  assert.deepEqual(s.missingFields({ title: " ", start: "", email: "z" }), ["title", "start"]);
  assert.deepEqual(Object.keys(s.buildPayload(GOOD, "t", "")).sort(), ["description", "email", "end", "start", "title", "token", "venue", "website"]);
  assert.equal(s.buildPayload(GOOD, "t", "").title, "Open mic");
  assert.equal(s.buildPayload({}, "t", "bot").website, "bot");
  assert.equal(s.messageFor(200, { ok: true }), "Thank you. We will look at your event soon.");
  assert.equal(s.messageFor(422, { ok: false, error: "check the highlighted fields" }), "Please check the highlighted fields.");
  assert.equal(s.messageFor(503, { ok: false, error: "try again later" }), "Try again later.");
  assert.equal(s.messageFor(500, null), "Try again later.");
  assert.equal(s.safePath("/p/x"), "/p/x");
  assert.equal(s.safePath("//evil.test"), "");
  assert.equal(s.safePath("javascript:x"), "");

  // the form: six labelled fields, the honeypot, the button
  {
    const { b, form, calls } = setup();
    await tick();
    assert.equal(calls.length, 1);
    assert.equal(calls[0].url, "/p/event-calendar/submit/token");
    const labels = b.all((n) => n.tag === "label").map((n) => n.textContent);
    assert.deepEqual(labels, ["Title", "Start", "End", "Venue", "Description", "Your email"]);
    assert.equal(b.all((n) => n.tag === "h3")[0].textContent, "Submit an event");
    assert.equal(form.inputs.start.type, "datetime-local");
    assert.equal(form.inputs.description.tag, "textarea");
    assert.equal(form.trap.name, "website");
    assert.equal(form.trap.tabIndex, -1);
    assert.equal(form.trap.attrs.autocomplete, "off");
    assert.equal(form.send.textContent, "Send");
    // the labels point at the inputs
    for (const l of b.all((n) => n.tag === "label")) assert.ok(b.all((n) => n.id === l.htmlFor).length === 1);
    // no form element, no markup from strings
    assert.equal(b.all((n) => n.tag === "form").length, 0);
  }

  // no heading, no unsafe address: nothing is built
  assert.equal(setup({ attrs: { "data-heading": "" } }).b.all((n) => n.tag === "h3").length, 0);
  assert.equal(setup({ attrs: { "data-api": "//evil.test/x" } }).form, null);
  assert.equal(setup({ attrs: { "data-token-api": "" } }).form, null);

  // required fields: marked, nothing posted
  {
    const { form, calls } = setup();
    await tick();
    fill(form, { venue: "x" });
    await form.submit();
    assert.equal(form.status.textContent, "Please check the highlighted fields.");
    assert.ok(form.inputs.title.classes.has("invalid") && form.inputs.start.classes.has("invalid") && form.inputs.email.classes.has("invalid"));
    assert.ok(!form.inputs.venue.classes.has("invalid"));
    assert.equal(calls.filter((c) => c.opts && c.opts.method === "POST").length, 0);
  }

  // a good post: JSON body, thanks, form cleared
  {
    const { form, calls } = setup();
    await tick();
    fill(form, GOOD);
    await form.submit();
    const post = calls.find((c) => c.opts && c.opts.method === "POST");
    assert.equal(post.url, "/p/event-calendar/submit");
    assert.equal(post.opts.headers["Content-Type"], "application/json");
    assert.deepEqual(JSON.parse(post.opts.body), {
      title: "Open mic", start: "2027-02-20T20:00", end: "", venue: "The Anchor", description: "Bring a song.",
      email: "visitor@example.org", token: "1700000000000.abcd", website: "",
    });
    assert.equal(form.status.textContent, "Thank you. We will look at your event soon.");
    assert.equal(form.inputs.title.value, "");
    assert.equal(form.send.disabled, false);
    assert.ok(!form.inputs.title.classes.has("invalid"));
  }

  // a filled honeypot is sent as it is
  {
    const { form, calls } = setup();
    await tick();
    fill(form, GOOD);
    form.trap.value = "spam";
    await form.submit();
    assert.equal(JSON.parse(calls.at(-1).opts.body).website, "spam");
  }

  // the token is waited for (3 s old at least) and renewed after 50 minutes
  {
    const { form, calls, advance } = setup();
    await tick();
    fill(form, GOOD);
    advance(1000);
    await form.submit();
    assert.equal(calls.filter((c) => c.url.endsWith("/token")).length, 1, "a young token is kept");
    advance(51 * 60 * 1000);
    fill(form, GOOD);
    await form.submit();
    assert.equal(calls.filter((c) => c.url.endsWith("/token")).length, 2, "an old token is renewed");
  }

  // answers: rejected, busy, network errors
  {
    const { form } = setup({ post: answer(422, { ok: false, error: "check the highlighted fields" }) });
    await tick();
    fill(form, GOOD);
    await form.submit();
    assert.equal(form.status.textContent, "Please check the highlighted fields.");
    assert.equal(form.inputs.title.value, " Open mic ", "a rejected form keeps what was typed");
  }
  {
    const { form } = setup({ post: answer(503, { ok: false, error: "try again later" }) });
    await tick();
    fill(form, GOOD);
    await form.submit();
    assert.equal(form.status.textContent, "Try again later.");
    assert.equal(form.send.disabled, false);
  }
  {
    const { form } = setup({ post: new Error("offline") });
    await tick();
    fill(form, GOOD);
    await form.submit();
    assert.equal(form.status.textContent, "Try again later.");
  }
  {
    const { form } = setup({ token: new Error("offline") });
    await tick();
    fill(form, GOOD);
    await form.submit();
    assert.equal(form.status.textContent, "Try again later.");
  }
  {
    const { form } = setup({ token: answer(503, { ok: false }) });
    await tick();
    fill(form, GOOD);
    await form.submit();
    assert.equal(form.status.textContent, "Try again later.");
  }

  // the island is lint-clean
  const src = fs.readFileSync(path.join(__dirname, "../widgets/submit/island.js"), "utf8");
  assert.ok(Buffer.byteLength(src) <= 32768);
  for (const re of [/\beval\s*\(/, /\bnew\s+Function\b/, /\bdocument\s*\.\s*write/, /\bimport\s*\(/, /\bimportScripts\b/, /https?:\/\//i, /["'`]\/\/[A-Za-z0-9]/, /\binnerHTML\b/, /localStorage|sessionStorage|XMLHttpRequest/]) {
    assert.ok(!re.test(src), `island.js matches ${re}`);
  }

  console.log("submit island: ok");
})();
