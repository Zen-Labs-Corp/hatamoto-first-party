// Run with: node jstest/ui_test.js
// Runs ui/app.js against a minimal DOM stub; no dependencies. Lives outside
// ui/ because everything under ui/ is served.
"use strict";
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
    this.checked = false;
    this.title = "";
    this.value = "";
    this.classes = new Set();
    this.classList = { toggle: (c, on) => (on ? this.classes.add(c) : this.classes.delete(c)) };
  }
  append(...kids) { this.children.push(...kids); }
  replaceChildren(...kids) { this.children = kids; }
  addEventListener(type, fn) { this.handlers[type] = fn; }
  click() { return this.handlers.click && this.handlers.click(); }
  fire(type, event) { return this.handlers[type] && this.handlers[type](event || {}); }
  text() {
    return (this.textContent || "") + this.children.map((c) => (typeof c === "string" ? c : c.text())).join(" ");
  }
  all(pred, out = []) {
    if (pred(this)) out.push(this);
    for (const c of this.children) if (typeof c !== "string") c.all(pred, out);
    return out;
  }
  buttons(label) { return this.all((n) => n.tag === "button" && n.textContent === label); }
}

const DATA = {
  ready: true,
  role: "owner",
  events: [
    {
      id: "y1", slug: "yoga", name: "Yoga in the park", rule: "Weekly, Tue, ends never", owned: true,
      dates: [
        { day: "2026-02-17", date: "2026-02-17", start: "18:00", end: "19:00", state: "normal" },
        { day: "2026-02-24", date: "2026-02-24", start: "18:00", state: "skipped" },
        { day: "2026-03-03", date: "2026-03-04", start: "19:00", state: "changed" },
      ],
    },
    {
      id: "b1", slug: "book-club", name: "Book club", rule: "Monthly, 2nd Thu, 12x", owned: false,
      dates: [{ day: "2026-02-12", date: "2026-02-12", start: "18:30", state: "normal" }],
    },
  ],
};

const SUBS = {
  role: "owner",
  enabled: true,
  total: 3,
  page: 1,
  per_page: 50,
  counts: { pending: 2, approved: 1, published: 1, rejected: 0, all: 4 },
  items: [
    { id: 7, status: "pending", created_at: "2027-02-10T09:30:00Z", title: "Open mic night", start_at: "2027-02-20T20:00:00+01:00", end_at: "2027-02-20T22:00:00+01:00", venue: "The Anchor", description: "Bring a song.", email: "visitor@example.org" },
    { id: 8, status: "pending", title: "Charity quiz", start_at: "2027-02-27T19:00:00Z", end_at: "", venue: "", description: "", email: "quiz@example.org" },
    { id: 9, status: "approved", title: "Old news", start_at: "2027-01-02", end_at: "", venue: "Hall", description: "", email: "old@example.org" },
  ],
};

const HOUR = 3600 * 1000;
const IMPORTS = {
  role: "owner",
  sources: [
    { name: "Town council", kind: "google", host: "calendar.google.com", category: "Civic", enabled: true, last_imported: null, schedule: "daily", scheduled_ok: true, last_new: new Date(Date.now() - 3 * HOUR - 60000).toISOString(), created_today: 12, url: "https://calendar.example.org/town.ics", imported: 200, cursor: "2026-03-01T09:00:00Z|google-town-council-0123456789abcdef", found: null, found_capped: false, remaining: null, counted_at: null },
    { name: "Riverside venue", kind: "outlook", host: "outlook.office365.com", category: "", enabled: false, last_imported: null, schedule: "off", scheduled_ok: true, last_new: null, created_today: 0, url: "https://outlook.example.org/river.ics" },
    { name: "Club", kind: "ical", host: "events.example.org", category: "", enabled: true, last_imported: null, schedule: "daily", scheduled_ok: false, last_new: null, created_today: 3, url: "https://events.example.org/club.ics" },
    { name: "Tickets", kind: "eventbrite", host: "", category: "", enabled: true, last_imported: null, schedule: "daily", scheduled_ok: true, last_new: null, created_today: 0 },
  ],
  daily_cap: 200,
  run_cap: 100,
  batch_cap: 200,
};

// The element ids ui/index.html defines and app.js reads.
// countAnswer is /admin/imports?count=<name>: the source with its feed counted
// (214 events, 14 left after the cursor).
function countAnswer(name, imports) {
  const src = imports.sources.find((s) => s.name === name);
  if (!src) return { error: "not found" };
  return { source: { ...src, found: 214, remaining: 14, counted_at: new Date().toISOString() } };
}

// monthAnswer is /admin/events?month=...: February 2026 whatever month was asked,
// five events on the 13th and an all-day one on the 2nd.
function monthAnswer(q, { truncated = false } = {}) {
  const items = [{ id: "m0", name: "Market", day: "2026-02-02", time: "", all_day: true }];
  for (let i = 1; i <= 5; i++) items.push({ id: `m${i}`, name: `Talk ${i}`, day: "2026-02-13", time: `1${i}:00`, all_day: false });
  const counts = { upcoming: 232, past: 155, submissions_pending: 14, repeating: 55 };
  if (q.with_drafts) counts.drafts = 93;
  return { ready: true, role: "owner", month: "2026-02", items, total: 6, truncated, day_counts: { "2026-02-02": 1, "2026-02-13": 5 }, page: 1, per_page: 1500, counts };
}

const IDS = [
  "#notice", "#hint", "#feed-row", "#feed-url",
  "#tab-upcoming", "#tab-past", "#tab-drafts", "#tab-submissions", "#tab-imports", "#tab-repeating",
  "#cnt-upcoming", "#cnt-past", "#cnt-drafts", "#cnt-submissions", "#cnt-repeating", "#ical", "#new-event",
  "#panel-events", "#q", "#venue", "#cat", "#novenue", "#view-list", "#view-month", "#chips", "#ev-state", "#list", "#ev-rows",
  "#month", "#m-prev", "#m-label", "#m-next", "#m-today", "#m-total", "#m-grid", "#ev-foot", "#ev-range", "#ev-pager",
  "#panel-submissions", "#sub-off", "#sub-pending", "#sub-approved", "#sub-rejected", "#sub-all", "#sub-q", "#sub-reject-all",
  "#sub-state", "#sub-table", "#sub-rows", "#sub-range", "#sub-pager",
  "#panel-imports", "#imp-owner-only", "#imp-state", "#imp-table", "#imp-rows", "#imp-next", "#imp-add", "#imp-detail", "#imp-url", "#imp-go", "#imp-confirm",
  "#panel-repeating", "#rep-q", "#state", "#events", "#rows", "#rep-range", "#rep-pager", "#duplicate", "#dup-event", "#dup-shift", "#dup-go",
  "#help",
];

// eventsAnswer is /admin/events for a query: 5 events a day, 50 to a page.
function eventsAnswer(q, { role = "owner", total = 232, capped = false, ready = true } = {}) {
  const page = Number(q.page || 1);
  const from = (page - 1) * 50;
  const items = [];
  const day_counts = {};
  for (let i = from; i < Math.min(total, from + 50); i++) {
    const day = new Date(Date.UTC(2027, 2, 1 + Math.floor(i / 5))).toISOString().slice(0, 10);
    items.push({
      id: `e${i}`, slug: `event-${i}`, name: `Event ${i + 1}`, day, time: "09:00", all_day: i % 7 === 6,
      venue: i % 4 === 0 ? "" : "Town hall", city: i % 4 === 0 ? "" : "Brighton", no_venue: i % 4 === 0,
      categories: ["Music"], price: i % 3 === 0 ? "Free" : "", status: q.tab === "drafts" ? "draft" : "published",
      repeating: i === 1, imported: i === 2,
    });
    day_counts[day] = 5;
  }
  const counts = { upcoming: 232, past: 155, submissions_pending: 14, repeating: 55 };
  if (q.with_drafts || q.tab === "drafts") Object.assign(counts, capped ? { drafts: 500, drafts_capped: true } : { drafts: 93 });
  const out = { ready, role, items, total, day_counts, page, per_page: 50, counts };
  if (capped) out.total_capped = true;
  if (q.facets) out.facets = { venues: ["Blue Room", "Town hall"], categories: ["Music", "Talk"] };
  return out;
}

// load runs the page and opens a tab (the repeating one unless told).
async function load({ named = "old", data = DATA, refuse = false, status = 200, tab = "repeating", subs = SUBS, imports = IMPORTS, events = {}, raw, post, countStatus = 0 } = {}) {
  const els = {};
  for (const id of IDS) els[id] = new Node(id);
  els["#dup-shift"].value = "7";
  const calls = [];
  const named_calls = [];
  const routes = [];
  const document = { querySelector: (s) => els[s], createElement: (t) => new Node(t) };
  const bodies = { "/admin/repeating": data, "/admin/submissions": subs, "/admin/imports": (q) => (q.count ? countAnswer(q.count, imports) : imports), "/admin/events": (q) => (q.month ? monthAnswer(q, events) : eventsAnswer(q, events)) };
  const hatamoto = {
    ready: Promise.resolve(),
    route: async (method, p, body, query) => {
      // core's bridge sets each query value with toString: a string stays, anything else becomes its JSON text
      const sent = {};
      for (const [k, v] of Object.entries(query || {})) sent[k] = typeof v === "string" ? v : v == null ? "" : JSON.stringify(v);
      routes.push({ method, p, query: sent });
      if (raw !== undefined) return { status, body: raw };
      if (p === "/admin/imports" && sent.count && countStatus) return { status: countStatus, body: JSON.stringify({ error: "try again" }) };
      if (method === "POST" && post) return { status: post.status, body: JSON.stringify(post.body) };
      const answer = bodies[p];
      return { status, body: JSON.stringify(method === "GET" ? (typeof answer === "function" ? answer(sent) : answer) : { ok: true }) };
    },
    // named is how the core answers runInputs("edit_dates", values): "old" throws, as an older core does
    runInputs: (...args) => {
      if (args.length === 2) {
        named_calls.push(args);
        if (named === "old") throw new Error("values must be an object");
        if (named === "rejects") return Promise.reject(new Error("bridge closed"));
        return Promise.resolve(named);
      }
      const v = args[0];
      calls.push(v);
      return refuse ? Promise.reject(new Error("no run form on this page")) : Promise.resolve();
    },
  };
  els["#feed-row"].hidden = true;
  vm.runInNewContext(src, { document, hatamoto, Object, Number, Math, Date, String, Set, JSON, Error, Boolean });
  await new Promise((r) => setTimeout(r, 10));
  if (tab !== "upcoming") {
    routes.length = 0;
    els[`#tab-${tab}`].click();
    await new Promise((r) => setTimeout(r, 10));
  }
  return { els, calls, routes, named_calls };
}

(async () => {
  let failures = 0;
  const check = (ok, msg) => { if (!ok) { failures++; console.error("FAIL: " + msg); } };
  const next = () => new Promise((r) => setTimeout(r, 5));

  // owner: list, expand, skip
  let { els, calls, routes } = await load();
  check(els["#events"].hidden === false, "table shown");
  let rows = els["#rows"];
  check(rows.children.length === 2, "one row per event while collapsed");
  check(rows.text().includes("Weekly, Tue, ends never") && rows.text().includes("Tue 17 Feb"), "rule and next date shown");
  check(rows.text().includes("Thu 12 Feb"), "second event next date");
  const chips0 = rows.children[0].all((n) => n.tag === "span" && n.textContent.match(/^\w{3} \d{2} \w{3}$/));
  check(chips0.map((c) => c.className).join(",") === ",sk,ch", "date chips: struck through when skipped, amber when changed: " + chips0.map((c) => c.className));
  check(chips0[2].textContent === "Wed 04 Mar", "a changed date shows where it falls now");
  rows.buttons("Edit dates")[0].click();
  rows = els["#rows"];
  const text = rows.text();
  check(text.includes("skipped") && text.includes("changed") && text.includes("normal"), "states shown");
  check(text.includes("Wed 04 Mar  7:00 pm"), "a changed date shows where it falls now");
  check(rows.buttons("Skip").length === 1 && rows.buttons("Restore").length === 2 && rows.buttons("Change time").length === 2, "buttons per state");
  await rows.buttons("Skip")[0].click();
  check(calls.length === 1 && calls[0].event === "yoga" && calls[0].action === "skip" && calls[0].date === "2026-02-17", "skip fills the run inputs");
  await next();
  check(els["#hint"].textContent.includes("Press Start") && !els["#hint"].textContent.includes("Open the Runs tab") && !els["#hint"].hidden, "hint after a filled run");
  await rows.buttons("Restore")[1].click();
  check(calls[1].action === "restore" && calls[1].date === "2026-03-03", "restore addresses the generated day, not the moved one");

  // change time editor
  els["#rows"].buttons("Change time")[0].click();
  const inputs = els["#rows"].all((n) => n.tag === "input");
  check(inputs.length === 2 && inputs[0].value === "18:00", "editor starts from the current time");
  inputs[0].value = "17:30";
  inputs[1].value = "19:00";
  await els["#rows"].buttons("Use these times")[0].click();
  const last = calls[calls.length - 1];
  check(last.action === "change" && last.start_at === "17:30" && last.end_at === "19:00" && last.date === "2026-02-17", "change fills times");

  // a not-owned event: hint, no buttons
  els["#rows"].buttons("Edit dates")[1].click();
  const hint = "Hand this event back in the editor (Who owns this page) to change single dates here, or edit the dates in the event itself.";
  check(els["#rows"].text().includes(hint), "not-owned hint verbatim");
  check(els["#rows"].all((n) => n.tag === "li" && n.text().includes("12 Feb")).every((li) => li.buttons("Skip").length === 0), "no buttons for a not-owned event");

  // duplicate: the row button opens the form with its event chosen
  check(els["#duplicate"].hidden, "the duplicate form starts hidden");
  els["#rows"].buttons("Duplicate")[1].click();
  check(!els["#duplicate"].hidden && els["#dup-event"].value === "book-club", "Duplicate opens the form for that event");
  await els["#dup-go"].click();
  const dup = calls[calls.length - 1];
  check(dup.action === "duplicate" && dup.shift_days === "7" && "event" in dup, "duplicate fills the one run: action, event and shift");

  // named form: started from the click, empty values left out
  let named_calls;
  ({ els, calls, named_calls } = await load({ named: { ok: true, run_id: "r1", url: "/admin/runs/r1" } }));
  els["#rows"].buttons("Edit dates")[0].click();
  const pending = els["#rows"].buttons("Skip")[0].click();
  check(named_calls.length === 1 && calls.length === 0, "the named form is called synchronously from the click");
  await pending;
  check(named_calls[0][0] === "edit_dates" && JSON.stringify(named_calls[0][1]) === JSON.stringify({ event: "yoga", action: "skip", date: "2026-02-17" }), "named values: " + JSON.stringify(named_calls[0]));
  check(els["#hint"].textContent.startsWith("Started.") && !els["#hint"].classes.has("error"), "hint after a started run: " + els["#hint"].textContent);
  els["#rows"].buttons("Change time")[0].click();
  const timeInputs = els["#rows"].all((n) => n.tag === "input");
  timeInputs[0].value = "17:30";
  timeInputs[1].value = "";
  await els["#rows"].buttons("Use these times")[0].click();
  check(JSON.stringify(named_calls[1][1]) === JSON.stringify({ event: "yoga", action: "change", date: "2026-02-17", start_at: "17:30" }), "change through the named form: " + JSON.stringify(named_calls[1]));

  // cancelled: quiet
  ({ els, calls, named_calls } = await load({ named: { ok: false, error: "cancelled" } }));
  els["#rows"].buttons("Edit dates")[0].click();
  await els["#rows"].buttons("Skip")[0].click();
  check(named_calls.length === 1 && els["#hint"].textContent === "" && !els["#hint"].classes.has("error") && calls.length === 0, "cancelled is ignored quietly");

  // another error: shown
  const busy = "That plugin already has a run in progress. Wait for it to end.";
  ({ els, calls, named_calls } = await load({ named: { ok: false, error: busy } }));
  els["#rows"].buttons("Edit dates")[0].click();
  await els["#rows"].buttons("Skip")[0].click();
  check(els["#hint"].textContent === busy && els["#hint"].classes.has("error") && calls.length === 0, "other errors are shown: " + els["#hint"].textContent);

  // an older core throws on the named form: the one-arg fill, with every key
  ({ els, calls, named_calls } = await load({ named: "old" }));
  els["#rows"].buttons("Edit dates")[0].click();
  await els["#rows"].buttons("Skip")[0].click();
  await next();
  check(named_calls.length === 1 && calls.length === 1 && calls[0].action === "skip" && calls[0].start_at === "" && calls[0].shift_days === "" && els["#hint"].textContent.includes("Press Start"), "fallback to the form fill: " + els["#hint"].textContent);
  // a rejected promise falls back too
  ({ els, calls } = await load({ named: "rejects" }));
  els["#rows"].buttons("Edit dates")[0].click();
  await els["#rows"].buttons("Restore")[0].click();
  await next();
  check(calls.length === 1 && calls[0].action === "restore", "a rejected named call falls back");

  // an editor never reaches runInputs
  ({ els, named_calls } = await load({ named: { ok: true }, data: { ...DATA, role: "editor" } }));
  check(els["#rows"].buttons("Edit dates").length === 0 && els["#rows"].buttons("Duplicate").length === 0 && els["#rows"].text().includes("Owner edits dates"), "an editor sees Owner edits dates and no Edit dates button");
  check(els["#rows"].buttons("Skip").length === 0 && named_calls.length === 0, "an editor has nothing to start");
  // the admin refuses the inputs (Settings tab): instruction line
  ({ els, calls } = await load({ refuse: true }));
  els["#rows"].buttons("Edit dates")[0].click();
  await els["#rows"].buttons("Skip")[0].click();
  await next();
  check(els["#hint"].textContent === "This screen cannot fill in the run. Open the plugin's Runs tab and use the same button there, then press Start.", "instruction on rejection: " + els["#hint"].textContent);

  // an editor cannot duplicate
  ({ els } = await load({ data: { ...DATA, role: "editor" } }));
  check(els["#dup-go"].disabled === true && els["#duplicate"].hidden, "editor cannot duplicate");

  // empty, not ready, error
  ({ els } = await load({ data: { ready: true, role: "owner", events: [] } }));
  check(els["#events"].hidden && els["#state"].textContent.includes("No repeating events"), "empty state");
  ({ els } = await load({ data: { ready: false, role: "owner", events: [] } }));
  check(els["#state"].textContent.includes("still being built"), "not ready state");
  ({ els } = await load({ data: { error: "nope" }, status: 503 }));
  check(els["#notice"].textContent === "nope" && els["#notice"].classes.has("error"), "error shown");

  // tabs: the first one loads on start, the others lazily and once
  ({ els, calls, routes } = await load({ tab: "upcoming" }));
  check(routes.length === 1 && routes[0].p === "/admin/events", "only the Events list loads at first");
  check(!els["#panel-events"].hidden && els["#panel-submissions"].hidden && els["#panel-imports"].hidden && els["#panel-repeating"].hidden, "Events panel shown first");
  els["#tab-imports"].click();
  await next();
  check(els["#panel-events"].hidden && !els["#panel-imports"].hidden && els["#tab-imports"].classes.has("on") && !els["#tab-upcoming"].classes.has("on"), "tab switch");
  check(routes.length === 3 && routes[1].p === "/admin/imports" && routes[2].query.count === "Town council", "Imports loads on first show, then counts one source");
  els["#tab-submissions"].click();
  els["#tab-imports"].click();
  await next();
  check(routes.length === 4 && routes[3].p === "/admin/submissions", "a tab loads once");
  els["#tab-repeating"].click();
  await next();
  check(routes.length === 5 && routes[4].p === "/admin/repeating" && !els["#panel-repeating"].hidden, "Repeating loads on first show");
  els["#tab-past"].click();
  await next();
  check(!els["#panel-events"].hidden && els["#panel-repeating"].hidden && routes[5].query.tab === "past", "Past reloads the shared Events panel");

  // submissions: rows, count, approve fills exactly the seven inputs
  ({ els, calls, routes } = await load({ tab: "submissions" }));
  check(els["#cnt-submissions"].textContent === "2" && els["#cnt-submissions"].classes.has("warn"), "pending count on the tab: " + els["#cnt-submissions"].textContent);
  let subRows = els["#sub-rows"];
  check(subRows.children.length === 3, "one row per submission");
  check(subRows.text().includes("Open mic night") && subRows.text().includes("Sat 20 Feb 8:00 pm") && subRows.text().includes("visitor@example.org") && subRows.text().includes("The Anchor"), "title, when as typed, venue and sender shown");
  check(subRows.text().includes("Sat 27 Feb 7:00 pm") && subRows.text().includes("Sat 02 Jan"), "a time with Z and a date only");
  check(routes[0].query.status === "pending", "the filter is sent as a query");
  check(subRows.buttons("Approve").length === 2 && subRows.buttons("Reject").length === 3 && subRows.buttons("Delete").length === 3, "buttons per status");
  await subRows.buttons("Approve")[0].click();
  const keys = Object.keys(calls[0]).sort().join(",");
  check(keys === "action,description,end_at,start_at,submission,title,venue_name", "approve fills the seven inputs: " + keys);
  check(calls[0].action === "approve" && calls[0].submission === "7" && calls[0].title === "Open mic night" && calls[0].start_at === "2027-02-20T20:00:00+01:00" && calls[0].venue_name === "The Anchor", "approve values");
  check(JSON.stringify(calls[0]).indexOf("@") === -1 && !("email" in calls[0]), "no email in the run inputs");
  await next();
  const hintText = els["#hint"].textContent;
  check(hintText.includes("Press Start") && hintText.includes("Changes") && !hintText.includes("@"), "approve hint: " + hintText);

  // reject posts and reloads
  routes.length = 0;
  await subRows.buttons("Reject")[0].click();
  await next();
  check(routes[0].method === "POST" && routes[0].p === "/admin/submissions/7/reject" && routes[1].method === "GET" && routes[1].p === "/admin/submissions", "reject posts, then reloads");
  routes.length = 0;
  await els["#sub-rows"].buttons("Delete")[1].click();
  await next();
  check(routes[0].method === "POST" && routes[0].p === "/admin/submissions/8/delete", "delete posts");

  // an editor: approve and delete disabled, reject works
  ({ els, calls, routes } = await load({ tab: "submissions", subs: { ...SUBS, role: "editor" } }));
  subRows = els["#sub-rows"];
  const locked = subRows.buttons("Approve");
  check(locked.length === 2 && locked.every((b) => b.disabled && b.title === "Ask the site owner"), "editor: approve disabled");
  check(subRows.buttons("Delete").length === 0, "editor: no Delete button");
  check(subRows.buttons("Reject").every((b) => !b.disabled), "editor may reject");
  routes.length = 0;
  await subRows.buttons("Reject")[0].click();
  await next();
  check(routes[0].p === "/admin/submissions/7/reject", "editor reject posts");

  // run inputs refused: the Runs tab instruction
  ({ els, calls } = await load({ tab: "submissions", refuse: true }));
  await els["#sub-rows"].buttons("Approve")[0].click();
  await next();
  check(els["#hint"].textContent.includes("same button") && els["#hint"].textContent.includes("Runs tab") && !els["#hint"].textContent.includes("type"), "refused approve shows the Runs-tab hint: " + els["#hint"].textContent);

  // empty and failing lists
  ({ els } = await load({ tab: "submissions", subs: { role: "owner", counts: {}, items: [] } }));
  check(els["#sub-table"].hidden && els["#sub-state"].textContent === "No submissions here." && els["#cnt-submissions"].textContent === "0", "no submissions");
  // the form is off: the tab says so; on, it does not
  ({ els } = await load({ tab: "submissions", subs: { ...SUBS, enabled: false } }));
  check(!els["#sub-off"].hidden, "a hint when submissions are off");
  ({ els } = await load({ tab: "submissions", subs: { ...SUBS, enabled: true } }));
  check(els["#sub-off"].hidden, "no hint when submissions are on");

  // reject all pending: owner only, and only on the second click
  ({ els, routes } = await load({ tab: "submissions" }));
  check(!els["#sub-reject-all"].disabled, "owner can reject all");
  await els["#sub-reject-all"].click();
  check(routes.length === 1, "the first click only asks");
  await els["#sub-reject-all"].click();
  await next();
  check(routes[1].method === "POST" && routes[1].p === "/admin/submissions/reject-all" && routes[2].p === "/admin/submissions", "second click posts, then reloads");
  ({ els } = await load({ tab: "submissions", subs: { ...SUBS, role: "editor" } }));
  check(els["#sub-reject-all"].disabled, "editor cannot reject all");
  ({ els } = await load({ tab: "submissions", status: 503, subs: { error: "unavailable" } }));
  check(els["#notice"].classes.has("error"), "a failing list shows an error");

  // imports: rows, schedule, progress, run now, edit, the any-url box
  ({ els, calls, routes } = await load({ tab: "imports" }));
  let impRows = els["#imp-rows"];
  const impText = impRows.text();
  const cells = (i) => impRows.children[i].children.map((td) => td.text().replace(/\s+/g, " ").trim());
  check(impRows.children.length === 4 && impText.includes("Town council") && impText.includes("Google"), "source rows");
  check(cells(0).slice(0, 4).join("|") === "Town council|Google|Daily|3 hours ago \u00b7 200 of 214 imported", "daily source: " + cells(0));
  check(cells(1).slice(2, 4).join("|") === "Off|Never", "off source: " + cells(1));
  check(cells(2)[2] === "Daily (Run now only)", "unlisted host: " + cells(2));
  check(cells(3)[1] === "Eventbrite" && cells(3)[2] === "Daily", "eventbrite source: " + cells(3));
  check(!impText.includes("calendar.example.org"), "the address is not shown in the table");
  check(els["#imp-owner-only"].hidden && els["#imp-state"].hidden, "an owner sees the table, not the owner-only note");
  check(impRows.children[0].children[3].all((n) => n.className === "st warn").length === 1, "a source with more waiting is flagged");
  check(routes.length === 2 && routes[1].query.count === "Town council" && !("count" in routes[0].query), "the feed of the source with imported events is counted once: " + JSON.stringify(routes.map((r) => r.query)));
  await impRows.buttons("Run now")[0].click();
  check(calls.length === 1 && calls[0].action === "import_source" && calls[0].name === "Town council" && !("url" in calls[0]) && !("after" in calls[0]), "run now fills the named source");
  await impRows.buttons("Run now")[3].click();
  check(calls.length === 2 && calls[1].action === "import_source" && calls[1].name === "Tickets", "run now works for a source without an address");
  impRows.buttons("Edit")[0].click();
  const detail = els["#imp-detail"].textContent;
  check(detail.includes("calendar.google.com") && detail.includes("Civic") && detail.includes("Settings tab") && !detail.includes("calendar.example.org"), "edit shows the details read-only: " + detail);
  els["#imp-add"].click();
  check(els["#imp-detail"].textContent === "Change or add sources on the plugin's Settings tab.", "add source points to Settings");

  // the next batch continues after the cursor, from the click
  const cursor = "2026-03-01T09:00:00Z|google-town-council-0123456789abcdef";
  check(impRows.buttons("Import the next 200").length === 1, "one next-batch button, on the source with more waiting");
  await impRows.buttons("Import the next 200")[0].click();
  check(calls.length === 3 && JSON.stringify(calls[2]) === JSON.stringify({ action: "import_source", name: "Town council", after: cursor }), "next batch fills action, name and after: " + JSON.stringify(calls[2]));
  check(!els["#imp-next"].hidden && els["#imp-next"].text().includes("Town council has 214 events") && els["#imp-next"].text().includes("brought in 200"), "callout: " + els["#imp-next"].text());
  await els["#imp-next"].buttons("Import the next 200")[0].click();
  check(calls.length === 4 && calls[3].after === cursor, "the callout button does the same");
  ({ els, calls } = await load({ tab: "imports", refuse: true }));
  await els["#imp-rows"].buttons("Import the next 200")[0].click();
  await next();
  check(els["#hint"].textContent.includes("action = import_source") && els["#hint"].textContent.includes("name = Town council") && els["#hint"].textContent.includes(`after = ${cursor}`), "a refused fill says what to type: " + els["#hint"].textContent);

  // before the count arrives, a full batch on the site already offers the next one; a finished source does not
  ({ els, calls, routes } = await load({ tab: "imports", countStatus: 503 }));
  check(routes.length === 2 && els["#imp-rows"].children[0].text().includes("200 imported") && !els["#imp-rows"].children[0].text().includes("of 214"), "a failed count keeps the row as it was");
  check(els["#imp-rows"].buttons("Import the next 200").length === 1 && els["#notice"].hidden, "and still offers the next batch, without an error");
  const done = { ...IMPORTS, sources: [{ ...IMPORTS.sources[0], found: 200, remaining: 0, counted_at: new Date().toISOString() }] };
  ({ els, routes } = await load({ tab: "imports", imports: done }));
  check(routes.length === 1 && els["#imp-rows"].buttons("Import the next 200").length === 0 && els["#imp-next"].hidden, "a recent count is not repeated, and nothing is left to import");
  check(els["#imp-rows"].children[0].text().includes("200 of 200 imported"), "finished source: " + els["#imp-rows"].children[0].text());

  // events before the cursor were never created: the batch is offered without "after"
  const behind = { ...IMPORTS, sources: [{ ...IMPORTS.sources[0], found: 214, remaining: 0, finished: false, counted_at: new Date().toISOString() }] };
  ({ els, calls } = await load({ tab: "imports", imports: behind }));
  check(els["#imp-rows"].buttons("Import the next 200").length === 1, "events left behind the cursor still offer a batch");
  await els["#imp-rows"].buttons("Import the next 200")[0].click();
  check(calls.length === 1 && JSON.stringify(calls[0]) === JSON.stringify({ action: "import_source", name: "Town council" }), "and the batch has no after: " + JSON.stringify(calls[0]));

  // a capped count (the first page of a feed) is never finished and never reads "200 of 50+"
  const capped = { ...IMPORTS, sources: [{ ...IMPORTS.sources[0], found: 50, found_capped: true, remaining: 0, finished: false, counted_at: new Date().toISOString() }] };
  ({ els } = await load({ tab: "imports", imports: capped }));
  check(els["#imp-rows"].buttons("Import the next 200").length === 1 && !els["#imp-next"].hidden, "a capped count still offers the next batch");
  check(els["#imp-rows"].children[0].text().includes("200 imported, more may remain") && !els["#imp-rows"].text().includes("of 50"), "capped copy: " + els["#imp-rows"].children[0].text());
  check(!els["#imp-next"].text().includes("50+") && els["#imp-next"].text().includes("more events"), "capped callout: " + els["#imp-next"].text());

  // any other iCal address: host confirmation, then the inputs
  ({ els, calls } = await load({ tab: "imports" }));
  els["#imp-url"].value = "webcal://Feeds.Example.org/cal/events.ics?token=abc";
  await els["#imp-go"].click();
  check(els["#imp-confirm"].textContent.includes("feeds.example.org") && els["#imp-confirm"].textContent.includes("Runs tab") && !els["#imp-confirm"].textContent.includes("token"), "host shown for confirmation: " + els["#imp-confirm"].textContent);
  const manual = calls[calls.length - 1];
  check(manual.action === "import_url" && manual.kind === "ical" && manual.name === "" && manual.url === "https://Feeds.Example.org/cal/events.ics?token=abc", "manual import inputs (webcal becomes https)");
  const before = calls.length;
  els["#imp-url"].value = "not an address";
  await els["#imp-go"].click();
  check(calls.length === before && els["#imp-confirm"].classes.has("error"), "a bad address starts nothing");

  // imports as an editor: owner only, from the data or from the refusal
  ({ els, calls } = await load({ tab: "imports", imports: { ...IMPORTS, role: "editor", sources: [] } }));
  check(!els["#imp-owner-only"].hidden && els["#imp-table"].hidden && els["#imp-go"].disabled, "editor: Imports are owner only");
  ({ els, routes } = await load({ tab: "imports", status: 403, raw: "forbidden" }));
  check(!els["#imp-owner-only"].hidden && els["#notice"].hidden && routes.length === 1, "a core 403 shows the same note");
  ({ els, routes } = await load({ tab: "imports", status: 403, raw: JSON.stringify({ error: "forbidden" }) }));
  check(!els["#imp-owner-only"].hidden && els["#notice"].hidden, "a core JSON 403 shows Owner only, no red notice");
  ({ els, calls } = await load({ tab: "imports", refuse: true }));
  await els["#imp-rows"].buttons("Run now")[0].click();
  await next();
  check(els["#hint"].textContent.includes("same button") && els["#hint"].textContent.includes("Runs tab") && !els["#hint"].textContent.includes("type"), "refused import shows the Runs-tab hint");
  ({ els } = await load({ tab: "imports", imports: { role: "owner", sources: [] } }));
  check(els["#imp-table"].hidden && els["#imp-state"].textContent.includes("Settings tab"), "no sources");

  // repeating: paging, search
  const manyRep = (q) => {
    const page = Number(q.page || 1);
    const items = [];
    for (let i = (page - 1) * 20; i < Math.min(55, page * 20); i++) {
      items.push({ id: `r${i}`, slug: `rep-${i}`, name: `Repeating ${i + 1}`, rule: "Weekly", owned: true, dates: [{ day: "2026-02-17", date: "2026-02-17", start: "18:00", state: "normal" }] });
    }
    return { ready: true, role: "owner", items, events: items, total: 55, page, per_page: 20 };
  };
  ({ els, routes } = await load({ tab: "repeating", data: manyRep }));
  check(routes[0].query.page === "1" && !("q" in routes[0].query) && els["#rep-range"].textContent === "1-20 of 55" && els["#rows"].children.length === 20, "first page of repeating events: " + els["#rep-range"].textContent);
  els["#rep-pager"].buttons("2")[0].click();
  await next();
  check(routes[1].query.page === "2" && els["#rep-range"].textContent === "21-40 of 55", "page 2: " + els["#rep-range"].textContent);
  els["#rep-pager"].buttons("3")[0].click();
  await next();
  check(els["#rep-range"].textContent === "41-55 of 55" && els["#rows"].children.length === 15, "last page: " + els["#rep-range"].textContent);
  els["#rep-q"].value = "yoga";
  els["#rep-q"].fire("keydown", { key: "Enter" });
  await next();
  check(routes[3].query.q === "yoga" && routes[3].query.page === "1", "search starts from page 1: " + JSON.stringify(routes[3].query));
  ({ els } = await load({ tab: "repeating", data: { ready: true, role: "owner", items: [], events: [], total: 0, page: 1, per_page: 20 } }));
  check(els["#events"].hidden && els["#state"].textContent.includes("No repeating events"), "an empty page");

  // submissions: filters, paging, search, roles
  const manySubs = (q) => {
    const status = q.status || "pending";
    const total = status === "all" ? 143 : 60;
    const page = Number(q.page || 1);
    const items = [];
    for (let i = (page - 1) * 50; i < Math.min(total, page * 50); i++) {
      items.push({ id: 100 + i, status: status === "all" ? "approved" : status, created_at: "2027-02-10T09:30:00Z", title: `Submission ${i + 1}`, start_at: "2027-03-01T19:00:00Z", end_at: "", venue: "", description: "", email: "a@example.org" });
    }
    return { role: "owner", enabled: true, total, page, per_page: 50, counts: { pending: 60, approved: 40, published: 3, rejected: 40, all: 143 }, items };
  };
  ({ els, calls, routes } = await load({ tab: "submissions", subs: manySubs }));
  check(routes[0].query.status === "pending" && routes[0].query.page === "1" && !("q" in routes[0].query), "first request: " + JSON.stringify(routes[0].query));
  check(els["#sub-pending"].classes.has("on") && els["#sub-pending"].text() === "Pending 60" && els["#sub-approved"].text() === "Approved 40" && els["#sub-all"].text() === "All 143", "status buttons carry counts (approved is the approved list only): " + els["#sub-approved"].text());
  check(els["#sub-pending"].children[1].className === "count" && els["#sub-approved"].children[1].className === "count q", "only Pending is highlighted");
  check(els["#sub-rows"].text().includes("Sent Wed 10 Feb") && els["#sub-rows"].text().includes("Pending"), "sent date and status pill: " + els["#sub-rows"].children[0].text());
  check(els["#sub-range"].textContent === "1-50 of 60", "range: " + els["#sub-range"].textContent);
  routes.length = 0;
  els["#sub-all"].click();
  await next();
  check(routes[0].query.status === "all" && routes[0].query.page === "1" && els["#sub-all"].classes.has("on") && !els["#sub-pending"].classes.has("on"), "All sends status all");
  check(els["#sub-range"].textContent === "1-50 of 143" && els["#sub-rows"].children[0].text().includes("Approved"), "all shows the total: " + els["#sub-range"].textContent);
  check(els["#sub-reject-all"].disabled, "Reject all pending is off outside the Pending filter");
  els["#sub-pager"].buttons("3")[0].click();
  await next();
  check(routes[1].query.page === "3" && routes[1].query.status === "all" && els["#sub-range"].textContent === "101-143 of 143", "page 3: " + els["#sub-range"].textContent);
  els["#sub-approved"].click();
  await next();
  check(routes[2].query.status === "approved" && routes[2].query.page === "1", "a filter goes back to page 1");
  els["#sub-rejected"].click();
  await next();
  check(routes[3].query.status === "rejected", "Rejected sends status rejected");
  els["#sub-q"].value = "quiz";
  els["#sub-q"].fire("keydown", { key: "Enter" });
  await next();
  check(routes[4].query.q === "quiz" && routes[4].query.status === "rejected" && routes[4].query.page === "1", "Enter searches title or email: " + JSON.stringify(routes[4].query));
  els["#sub-pending"].click();
  await next();
  check(!els["#sub-reject-all"].disabled, "Reject all pending is on for the Pending filter");

  // an editor: no Delete, Approve disabled, Reject enabled, Reject all disabled
  ({ els, routes } = await load({ tab: "submissions", subs: (q) => ({ ...manySubs(q), role: "editor" }) }));
  check(els["#sub-rows"].buttons("Delete").length === 0, "editor sees no Delete");
  check(els["#sub-rows"].buttons("Approve").every((b) => b.disabled && b.title === "Ask the site owner"), "editor: Approve disabled");
  check(els["#sub-rows"].buttons("Reject").length === 50 && els["#sub-rows"].buttons("Reject").every((b) => !b.disabled), "editor: Reject enabled");
  check(els["#sub-reject-all"].disabled && els["#sub-reject-all"].title === "Ask the site owner", "editor: Reject all disabled");

  // a refused delete
  ({ els, routes } = await load({ tab: "submissions", post: { status: 403, body: { error: "not allowed" } } }));
  await els["#sub-rows"].buttons("Delete")[0].click();
  await next();
  check(els["#notice"].textContent === "not allowed" && els["#notice"].classes.has("error"), "a refused delete shows not allowed");

  // Events list: first load, counts, rows grouped by day
  ({ els, routes } = await load({ tab: "upcoming" }));
  check(routes.length === 1 && routes[0].p === "/admin/events", "the Events list loads first");
  const first = routes[0].query;
  check(first.tab === "upcoming" && first.facets === "1" && first.with_drafts === "1" && first.page === "1" && !("month" in first), "first request: " + JSON.stringify(first));
  check(els["#cnt-upcoming"].textContent === "232" && els["#cnt-past"].textContent === "155" && els["#cnt-drafts"].textContent === "93" && els["#cnt-repeating"].textContent === "55", "tab counts");
  check(els["#cnt-submissions"].textContent === "14" && els["#cnt-submissions"].classes.has("warn") && !els["#cnt-submissions"].classes.has("q"), "pending submissions count is highlighted");
  check(els["#venue"].children.length === 3 && els["#cat"].children.length === 3, "facets fill the selects");
  check(els["#tab-upcoming"].classes.has("on") && !els["#tab-imports"].disabled, "Upcoming is active, Imports open for the owner");
  let evRows = els["#ev-rows"];
  check(evRows.children.length === 60, "50 events and 10 day headers: " + evRows.children.length);
  check(evRows.children[0].className === "day" && evRows.children[0].text().includes("Mon 01 Mar") && evRows.children[0].text().includes("5 events"), "day header with the day's count: " + evRows.children[0].text());
  check(evRows.children[1].text().includes("Free") && evRows.children[1].text().includes("No venue") && evRows.children[1].text().includes("Published"), "row: price, no venue, status: " + evRows.children[1].text());
  check(evRows.children[2].text().includes("Repeats") && evRows.children[3].text().includes("Imported") && evRows.children[2].text().includes("Town hall") && evRows.children[2].text().includes("Brighton"), "row: tags, venue and city");
  check(els["#ev-range"].textContent === "1-50 of 232", "range: " + els["#ev-range"].textContent);
  check(els["#ev-state"].hidden && !els["#list"].hidden, "no empty state");

  // paging
  const pagerNode = els["#ev-pager"];
  check(pagerNode.children[0].disabled && pagerNode.children[1].className === "on", "pager starts on page 1 with Previous disabled");
  check(pagerNode.children.map((c) => c.textContent).join(" ") === "\u2039 1 2 \u2026 5 \u203a", "pager: " + pagerNode.children.map((c) => c.textContent).join(" "));
  routes.length = 0;
  pagerNode.buttons("2")[0].click();
  await next();
  check(routes[0].query.page === "2" && routes[0].query.tab === "upcoming" && !("facets" in routes[0].query) && !("with_drafts" in routes[0].query), "page 2 request: " + JSON.stringify(routes[0].query));
  check(els["#ev-range"].textContent === "51-100 of 232", "page 2 range: " + els["#ev-range"].textContent);

  // search, chips
  routes.length = 0;
  els["#q"].value = "jazz";
  els["#q"].fire("keydown", { key: "Escape" });
  await next();
  check(routes.length === 0, "only Enter searches");
  els["#q"].fire("keydown", { key: "Enter" });
  await next();
  check(routes.length === 1 && routes[0].query.q === "jazz" && routes[0].query.page === "1", "Enter searches from page 1");
  els["#q"].fire("change");
  await next();
  check(routes.length === 1, "the same search is not sent twice");
  check(!els["#chips"].hidden && els["#chips"].children.length === 1 && els["#chips"].text().includes("jazz"), "a chip for the search: " + els["#chips"].text());
  els["#chips"].children[0].click();
  await next();
  check(!("q" in routes[1].query) && els["#chips"].hidden && els["#q"].value === "", "the chip clears the search");

  // venue, category and no venue
  els["#venue"].value = "Town hall";
  els["#venue"].fire("change");
  await next();
  els["#cat"].value = "Talk";
  els["#cat"].fire("change");
  await next();
  const both = routes[routes.length - 1].query;
  check(both.venue === "Town hall" && both.category === "Talk" && els["#chips"].children.length === 2, "venue and category filters: " + JSON.stringify(both));
  els["#novenue"].checked = true;
  els["#novenue"].fire("change");
  await next();
  check(routes[routes.length - 1].query.novenue === "1" && els["#chips"].text().includes("No venue set"), "the checkbox sends novenue 1");
  els["#novenue"].checked = false;
  els["#novenue"].fire("change");
  await next();
  check(!("novenue" in routes[routes.length - 1].query), "unticked sends no novenue");

  // Drafts: list only, no venue or category, capped count
  ({ els, routes } = await load({ tab: "upcoming", events: { capped: true } }));
  els["#tab-drafts"].click();
  await next();
  check(routes[1].query.tab === "drafts" && els["#venue"].disabled && els["#cat"].disabled && els["#view-month"].disabled, "Drafts disables venue, category and Month");
  check(els["#cnt-drafts"].textContent === "500+" && els["#ev-range"].textContent === "1-50 of 232+", "capped drafts show 500+: " + els["#cnt-drafts"].textContent + " / " + els["#ev-range"].textContent);
  check(els["#ev-rows"].children[1].text().includes("Draft"), "draft status pill");
  els["#tab-past"].click();
  await next();
  check(!els["#venue"].disabled && !els["#view-month"].disabled, "Past enables them again");

  // empty and not ready
  ({ els } = await load({ tab: "upcoming", events: { total: 0 } }));
  check(els["#list"].hidden && els["#ev-state"].textContent === "No events match these filters." && !els["#ev-state"].hidden, "empty state");
  ({ els } = await load({ tab: "upcoming", events: { total: 0, ready: false } }));
  check(els["#ev-state"].textContent.includes("being prepared"), "not ready state");

  // an editor cannot open Imports
  ({ els } = await load({ tab: "upcoming", events: { role: "editor" } }));
  check(els["#tab-imports"].disabled && els["#tab-imports"].title === "Owner only", "editor: Imports disabled as owner only");
  // core's 403 has no JSON body
  ({ els } = await load({ tab: "upcoming", status: 403, raw: "forbidden" }));
  check(els["#notice"].textContent === "not allowed" && els["#notice"].classes.has("error"), "a 403 without JSON reads as not allowed");

  // hints and the feed row
  ({ els } = await load({ tab: "upcoming" }));
  els["#ev-rows"].children[1].buttons("Edit")[0].click();
  check(els["#hint"].textContent.includes("Open Content") && els["#hint"].textContent.includes("open Event 1 there") && !els["#hint"].hidden, "Edit hint: " + els["#hint"].textContent);
  els["#new-event"].click();
  check(els["#hint"].textContent.includes("press New"), "New event hint");
  check(els["#feed-row"].hidden, "the feed row starts hidden");
  els["#ical"].click();
  check(!els["#feed-row"].hidden, "iCal feed shows the row");
  els["#ical"].click();
  check(els["#feed-row"].hidden, "and hides it again");

  // month grid
  ({ els, routes } = await load({ tab: "upcoming" }));
  routes.length = 0;
  els["#view-month"].click();
  await next();
  const mq = routes[0].query;
  check(/^\d{4}-\d{2}$/.test(mq.month) && !("page" in mq) && !("day" in mq) && mq.tab === "upcoming", "month request: " + JSON.stringify(mq));
  const now = new Date();
  check(mq.month === `${now.getFullYear()}-${String(now.getMonth() + 1).padStart(2, "0")}`, "starts on the current month");
  check(!els["#month"].hidden && els["#list"].hidden && els["#ev-foot"].hidden && els["#view-month"].classes.has("on") && !els["#view-list"].classes.has("on"), "month view shown, list hidden");
  check(els["#m-label"].textContent === "February 2026" && els["#m-total"].textContent === "6 events this month", "label and total: " + els["#m-label"].textContent + " / " + els["#m-total"].textContent);
  const grid = els["#m-grid"];
  const heads = grid.children.filter((c) => c.className === "h");
  const days = grid.children.filter((c) => c.className.startsWith("c"));
  check(heads.map((h) => h.textContent).join(",") === "Mon,Tue,Wed,Thu,Fri,Sat,Sun" && days.length === 42, "7 headers and 42 cells");
  check(days[0].className === "c out" && days[0].text().includes("26") && days[6].text().includes("1") && !days[6].className.includes("out"), "Monday first: the grid starts on 26 Jan and Sunday 1 Feb is in the month");
  const crowded = days.find((c) => c.buttons("+2 more").length === 1);
  check(crowded && crowded.all((n) => n.className === "e").length === 3, "three events and +2 more on the crowded day");
  check(crowded.all((n) => n.className === "e")[0].textContent === "11:00 Talk 1", "event pill: time and title");
  check(days.some((c) => c.all((n) => n.className === "e" && n.textContent === "Market").length === 1), "an all-day event shows its title only");
  check(days.every((c) => c.all((n) => n.className === "more").length === (c === crowded ? 1 : 0)), "only the crowded day has +N more");
  crowded.all((n) => n.className === "e")[1].click();
  check(els["#hint"].textContent.includes("open Talk 2 there"), "an event pill gives the Edit hint");
  routes.length = 0;
  crowded.buttons("+2 more")[0].click();
  await next();
  const dq = routes[0].query;
  check(dq.day === "2026-02-13" && dq.page === "1" && !("month" in dq) && dq.tab === "past", "+2 more lists that day: " + JSON.stringify(dq));
  check(els["#chips"].text().includes("Day: 13 Feb") && els["#list"].hidden === false && els["#month"].hidden, "the Day chip and the list view");
  check(els["#tab-past"].classes.has("on") && !els["#tab-upcoming"].classes.has("on"), "a past day switches to Past");
  els["#chips"].children[0].click();
  await next();
  check(!("day" in routes[1].query) && els["#chips"].hidden, "clearing the Day chip lists every day again");

  // month navigation
  ({ els, routes } = await load({ tab: "upcoming" }));
  els["#view-month"].click();
  await next();
  routes.length = 0;
  els["#m-next"].click();
  await next();
  check(routes[0].query.month === "2026-03", "next asks for the month after the one shown: " + routes[0].query.month);
  els["#m-prev"].click();
  await next();
  check(routes[1].query.month === "2026-01", "previous: " + routes[1].query.month);
  els["#m-today"].click();
  await next();
  check(routes[2].query.month === `${now.getFullYear()}-${String(now.getMonth() + 1).padStart(2, "0")}`, "Today returns to the current month");
  els["#view-list"].click();
  await next();
  check(routes[3].query.page === "1" && !("month" in routes[3].query) && !els["#list"].hidden && els["#month"].hidden, "List returns to the list");
  // filters and Drafts in month view
  els["#view-month"].click();
  await next();
  els["#novenue"].checked = true;
  els["#novenue"].fire("change");
  await next();
  check(routes[routes.length - 1].query.novenue === "1" && "month" in routes[routes.length - 1].query, "filters apply to the month");
  els["#tab-drafts"].click();
  await next();
  const dr = routes[routes.length - 1].query;
  check(dr.tab === "drafts" && !("month" in dr) && dr.page === "1" && els["#month"].hidden, "Drafts forces the list");
  // truncated month
  ({ els } = await load({ tab: "upcoming", events: { truncated: true } }));
  els["#view-month"].click();
  await next();
  check(els["#m-total"].textContent === "6 events this month (first 1500 shown)", "truncated month: " + els["#m-total"].textContent);

  if (failures) process.exit(1);
  console.log("ok");
})();
