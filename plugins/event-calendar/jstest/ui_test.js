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

async function load({ data = DATA, refuse = false, status = 200 } = {}) {
  const els = {};
  for (const id of ["#notice", "#hint", "#state", "#events", "#rows", "#duplicate", "#dup-event", "#dup-shift", "#dup-go"]) {
    els[id] = new Node(id);
  }
  els["#dup-shift"].value = "7";
  const calls = [];
  const document = { querySelector: (s) => els[s], createElement: (t) => new Node(t) };
  const hatamoto = {
    ready: Promise.resolve(),
    route: async (method, p) => ({ status, body: JSON.stringify(data), method, p }),
    runInputs: (v) => {
      calls.push(v);
      return refuse ? Promise.reject(new Error("no run form on this page")) : Promise.resolve();
    },
  };
  vm.runInNewContext(src, { document, hatamoto, Object, Number, Math, Date, String, Set, JSON, Error });
  await new Promise((r) => setTimeout(r, 10));
  return { els, calls };
}

(async () => {
  let failures = 0;
  const check = (ok, msg) => { if (!ok) { failures++; console.error("FAIL: " + msg); } };
  const next = () => new Promise((r) => setTimeout(r, 5));

  // owner: list, expand, skip
  let { els, calls } = await load();
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
  check(els["#hint"].textContent.includes("Press Start") && !els["#hint"].hidden, "hint after a filled run");
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
  check(els["#hint"].textContent === 'Open the Runs tab, pick "Edit repeat dates or duplicate" and enter: event=yoga, action=skip, date=2026-02-17', "instruction on rejection: " + els["#hint"].textContent);

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

  if (failures) process.exit(1);
  console.log("ok");
})();
