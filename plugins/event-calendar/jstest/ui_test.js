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
    this.value = "";
    this.classes = new Set();
    this.classList = { toggle: (c, on) => (on ? this.classes.add(c) : this.classes.delete(c)) };
  }
  append(...kids) { this.children.push(...kids); }
  replaceChildren(...kids) { this.children = kids; }
  addEventListener(type, fn) { this.handlers[type] = fn; }
  click() { return this.handlers.click && this.handlers.click(); }
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
  counts: { pending: 2, approved: 0, published: 1, rejected: 0 },
  items: [
    { id: 7, status: "pending", title: "Open mic night", start_at: "2027-02-20T20:00:00+01:00", end_at: "2027-02-20T22:00:00+01:00", venue: "The Anchor", description: "Bring a song.", email: "visitor@example.org" },
    { id: 8, status: "pending", title: "Charity quiz", start_at: "2027-02-27T19:00:00Z", end_at: "", venue: "", description: "", email: "quiz@example.org" },
    { id: 9, status: "approved", title: "Old news", start_at: "2027-01-02", end_at: "", venue: "Hall", description: "", email: "old@example.org" },
  ],
};

const HOUR = 3600 * 1000;
const IMPORTS = {
  role: "owner",
  sources: [
    { name: "Town council", kind: "google", host: "calendar.google.com", category: "Civic", enabled: true, last_imported: new Date(Date.now() - 3 * HOUR - 60000).toISOString(), url: "https://calendar.example.org/town.ics" },
    { name: "Riverside venue", kind: "outlook", host: "outlook.office365.com", category: "", enabled: false, last_imported: null, url: "https://outlook.example.org/river.ics" },
  ],
};

const IDS = [
  "#notice", "#hint", "#state", "#events", "#rows", "#duplicate", "#dup-event", "#dup-shift", "#dup-go",
  "#tab-submissions", "#tab-imports", "#tab-repeating", "#panel-submissions", "#panel-imports", "#panel-repeating",
  "#sub-status", "#sub-off", "#sub-reject-all", "#sub-state", "#sub-table", "#sub-rows", "#imp-state", "#imp-table", "#imp-rows", "#imp-detail", "#imp-url", "#imp-go", "#imp-confirm",
];

// load runs the page and opens a tab (the repeating one unless told).
async function load({ data = DATA, refuse = false, status = 200, tab = "repeating", subs = SUBS, imports = IMPORTS } = {}) {
  const els = {};
  for (const id of IDS) els[id] = new Node(id);
  els["#dup-shift"].value = "7";
  const calls = [];
  const routes = [];
  const document = { querySelector: (s) => els[s], createElement: (t) => new Node(t) };
  const bodies = { "/admin/repeating": data, "/admin/submissions": subs, "/admin/imports": imports };
  const hatamoto = {
    ready: Promise.resolve(),
    route: async (method, p, body, query) => {
      // core's bridge sets each query value with toString: a string stays, anything else becomes its JSON text
      const sent = {};
      for (const [k, v] of Object.entries(query || {})) sent[k] = typeof v === "string" ? v : v == null ? "" : JSON.stringify(v);
      routes.push({ method, p, query: sent });
      return { status, body: JSON.stringify(method === "GET" ? bodies[p] : { ok: true }) };
    },
    runInputs: (v) => {
      calls.push(v);
      return refuse ? Promise.reject(new Error("no run form on this page")) : Promise.resolve();
    },
  };
  vm.runInNewContext(src, { document, hatamoto, Object, Number, Math, Date, String, Set, JSON, Error, Boolean });
  await new Promise((r) => setTimeout(r, 10));
  if (tab !== "submissions") {
    els[`#tab-${tab}`].click();
    await new Promise((r) => setTimeout(r, 10));
  }
  return { els, calls, routes };
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
  check(rows.text().includes("Weekly, Tue, ends never") && rows.text().includes("Tue 17 Feb  6:00 pm"), "rule and next date shown");
  check(rows.text().includes("Thu 12 Feb  6:30 pm"), "second event next date");
  rows.buttons("v dates")[0].click();
  rows = els["#rows"];
  const text = rows.text();
  check(text.includes("skipped") && text.includes("changed") && text.includes("normal"), "states shown");
  check(text.includes("Wed 04 Mar  7:00 pm"), "a changed date shows where it falls now");
  check(rows.buttons("Skip").length === 1 && rows.buttons("Restore").length === 2 && rows.buttons("Change time").length === 2, "buttons per state");
  await rows.buttons("Skip")[0].click();
  check(calls.length === 1 && calls[0].event === "yoga" && calls[0].action === "skip" && calls[0].date === "2026-02-17", "skip fills the run inputs");
  await next();
  check(els["#hint"].textContent.includes("press Start") && !els["#hint"].hidden, "hint after a filled run");
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
  els["#rows"].buttons("v dates")[0].click();
  const hint = "Hand this event back in the editor (Who owns this page) to change single dates here, or edit the dates in the event itself.";
  check(els["#rows"].text().includes(hint), "not-owned hint verbatim");
  check(els["#rows"].all((n) => n.tag === "li" && n.text().includes("12 Feb")).every((li) => li.buttons("Skip").length === 0), "no buttons for a not-owned event");

  // duplicate
  await els["#dup-go"].click();
  const dup = calls[calls.length - 1];
  check(dup.action === "duplicate" && dup.shift_days === "7" && "event" in dup, "duplicate fills the one run: action, event and shift");

  // the admin refuses the inputs (Settings tab): instruction line
  ({ els, calls } = await load({ refuse: true }));
  els["#rows"].buttons("v dates")[0].click();
  await els["#rows"].buttons("Skip")[0].click();
  await next();
  check(els["#hint"].textContent === 'Open the Runs tab, type these into the "Edit repeat dates or duplicate" form and press Start: event=yoga, action=skip, date=2026-02-17', "instruction on rejection: " + els["#hint"].textContent);

  // an editor sees disabled buttons
  ({ els } = await load({ data: { ...DATA, role: "editor" } }));
  els["#rows"].buttons("v dates")[0].click();
  const all = els["#rows"].all((n) => n.tag === "button" && n.textContent !== "v dates" && n.textContent !== "^ dates");
  check(all.length > 0 && all.every((b) => b.disabled && b.title === "Ask the site owner"), "editor buttons disabled");
  check(els["#dup-go"].disabled === true, "editor cannot duplicate");
  check(els["#state"].textContent.includes("Ask the site owner"), "editor note");

  // empty, not ready, error
  ({ els } = await load({ data: { ready: true, role: "owner", events: [] } }));
  check(els["#events"].hidden && els["#state"].textContent.includes("No repeating events"), "empty state");
  ({ els } = await load({ data: { ready: false, role: "owner", events: [] } }));
  check(els["#state"].textContent.includes("still being built"), "not ready state");
  ({ els } = await load({ data: { error: "nope" }, status: 503 }));
  check(els["#notice"].textContent === "nope" && els["#notice"].classes.has("error"), "error shown");

  // tabs: the first one loads on start, the others lazily and once
  ({ els, calls, routes } = await load({ tab: "submissions" }));
  check(routes.length === 1 && routes[0].p === "/admin/submissions", "only the Submissions tab loads at first");
  check(!els["#panel-submissions"].hidden && els["#panel-imports"].hidden && els["#panel-repeating"].hidden, "Submissions panel shown first");
  els["#tab-imports"].click();
  await next();
  check(els["#panel-submissions"].hidden && !els["#panel-imports"].hidden && els["#tab-imports"].classes.has("active") && !els["#tab-submissions"].classes.has("active"), "tab switch");
  check(routes.length === 2 && routes[1].p === "/admin/imports", "Imports loads on first show");
  els["#tab-submissions"].click();
  els["#tab-imports"].click();
  await next();
  check(routes.length === 2, "a tab loads once");
  els["#tab-repeating"].click();
  await next();
  check(routes.length === 3 && routes[2].p === "/admin/repeating" && !els["#panel-repeating"].hidden, "Repeating loads on first show");

  // submissions: rows, count, approve fills exactly the seven inputs
  ({ els, calls, routes } = await load({ tab: "submissions" }));
  check(els["#tab-submissions"].textContent === "Submissions (2)", "pending count on the tab: " + els["#tab-submissions"].textContent);
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
  check(hintText.includes("Runs tab") && hintText.includes("Start") && hintText.includes("Changes") && !hintText.includes("@"), "approve hint: " + hintText);

  // reject posts and reloads
  routes.length = 0;
  await subRows.buttons("Reject")[0].click();
  await next();
  check(routes[0].method === "POST" && routes[0].p === "/admin/submissions/7/reject" && routes[1].method === "GET" && routes[1].p === "/admin/submissions", "reject posts, then reloads");
  routes.length = 0;
  await els["#sub-rows"].buttons("Delete")[1].click();
  await next();
  check(routes[0].method === "POST" && routes[0].p === "/admin/submissions/8/delete", "delete posts");

  // the filter
  routes.length = 0;
  els["#sub-status"].value = "rejected";
  els["#sub-status"].handlers.change();
  await next();
  check(routes[0].query.status === "rejected", "changing the filter reloads with it");

  // an editor: approve and delete disabled, reject works
  ({ els, calls, routes } = await load({ tab: "submissions", subs: { ...SUBS, role: "editor" } }));
  subRows = els["#sub-rows"];
  const locked = [...subRows.buttons("Approve"), ...subRows.buttons("Delete")];
  check(locked.length === 5 && locked.every((b) => b.disabled && b.title === "Ask the site owner"), "editor: approve and delete disabled");
  check(subRows.buttons("Reject").every((b) => !b.disabled), "editor may reject");
  routes.length = 0;
  await subRows.buttons("Reject")[0].click();
  await next();
  check(routes[0].p === "/admin/submissions/7/reject", "editor reject posts");

  // run inputs refused: the Runs tab instruction
  ({ els, calls } = await load({ tab: "submissions", refuse: true }));
  await els["#sub-rows"].buttons("Approve")[0].click();
  await next();
  check(els["#hint"].textContent.startsWith("Open the Runs tab, type these into the") && els["#hint"].textContent.includes("action=approve") && els["#hint"].textContent.includes("submission=7"), "refused approve shows the Runs-tab hint: " + els["#hint"].textContent);

  // empty and failing lists
  ({ els } = await load({ tab: "submissions", subs: { role: "owner", counts: {}, items: [] } }));
  check(els["#sub-table"].hidden && els["#sub-state"].textContent === "No submissions here." && els["#tab-submissions"].textContent === "Submissions", "no submissions");
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

  // imports: rows, last imported, run now, edit, the any-url box
  ({ els, calls } = await load({ tab: "imports" }));
  let impRows = els["#imp-rows"];
  const impText = impRows.text();
  check(impRows.children.length === 2 && impText.includes("Town council") && impText.includes("Google") && impText.includes("manual") && !impText.includes("every 6"), "source rows");
  check(impText.includes("3 hours ago") && impText.includes("never") && impText.includes("off"), "last imported and a disabled source");
  check(!impText.includes("calendar.example.org"), "the address is not shown in the table");
  await impRows.buttons("Run now")[0].click();
  check(calls.length === 1 && calls[0].action === "import_url" && calls[0].url === "https://calendar.example.org/town.ics" && calls[0].kind === "google" && calls[0].name === "Town council", "run now fills the import inputs");
  impRows.buttons("Edit")[0].click();
  const detail = els["#imp-detail"].textContent;
  check(detail.includes("calendar.google.com") && detail.includes("Civic") && detail.includes("Settings tab") && !detail.includes("calendar.example.org"), "edit shows the details read-only: " + detail);

  // any other iCal address: host confirmation, then the inputs
  els["#imp-url"].value = "webcal://Feeds.Example.org/cal/events.ics?token=abc";
  await els["#imp-go"].click();
  check(els["#imp-confirm"].textContent.includes("feeds.example.org") && els["#imp-confirm"].textContent.includes("Runs tab") && !els["#imp-confirm"].textContent.includes("token"), "host shown for confirmation: " + els["#imp-confirm"].textContent);
  const manual = calls[calls.length - 1];
  check(manual.action === "import_url" && manual.kind === "ical" && manual.name === "" && manual.url === "webcal://Feeds.Example.org/cal/events.ics?token=abc", "manual import inputs");
  const before = calls.length;
  els["#imp-url"].value = "not an address";
  await els["#imp-go"].click();
  check(calls.length === before && els["#imp-confirm"].classes.has("error"), "a bad address starts nothing");

  // imports as an editor, refused inputs, empty list
  ({ els, calls } = await load({ tab: "imports", imports: { ...IMPORTS, role: "editor", sources: IMPORTS.sources.map(({ url, ...rest }) => rest) } }));
  check(els["#imp-rows"].buttons("Run now").every((b) => b.disabled && b.title === "Ask the site owner") && els["#imp-go"].disabled, "editor: run now disabled");
  check(!els["#imp-rows"].buttons("Edit")[0].disabled, "editor may read the details");
  ({ els, calls } = await load({ tab: "imports", refuse: true }));
  await els["#imp-rows"].buttons("Run now")[0].click();
  await next();
  check(els["#hint"].textContent.startsWith("Open the Runs tab, type these into the") && els["#hint"].textContent.includes("action=import_url"), "refused import shows the Runs-tab hint");
  ({ els } = await load({ tab: "imports", imports: { role: "owner", sources: [] } }));
  check(els["#imp-table"].hidden && els["#imp-state"].textContent.includes("Settings tab"), "no sources");

  if (failures) process.exit(1);
  console.log("ok");
})();
