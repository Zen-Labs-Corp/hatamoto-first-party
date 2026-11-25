// Run with: node jstest/seating_test.js
// Runs ui/seating.js against a minimal DOM stub; no dependencies.
"use strict";
const assert = require("node:assert/strict");

class Node {
  constructor(tag) {
    this.tag = tag;
    this.children = [];
    this.handlers = {};
    this.attrs = {};
    this.hidden = false;
    this.disabled = false;
    this.value = "";
    this.textContent = "";
    this.className = "";
  }
  append(...kids) { this.children.push(...kids); }
  replaceChildren(...kids) { this.children = kids; }
  setAttribute(k, v) { this.attrs[k] = v; }
  addEventListener(type, fn) { this.handlers[type] = fn; }
  click() { return this.handlers.click(); }
  all(pred, out = []) {
    if (pred(this)) out.push(this);
    for (const c of this.children) c.all(pred, out);
    return out;
  }
}

const IDS = ["#st-maps", "#st-new-name", "#st-add-map", "#st-ed", "#st-name", "#st-rename", "#st-sections", "#st-add-section", "#st-add-zone", "#st-block", "#st-form", "#st-grid"];
const els = {};
for (const id of IDS) els[id] = new Node(id);
globalThis.document = { querySelector: (s) => els[s] };
const seating = require("../ui/seating.js");

const el = (tag, props = {}, ...kids) => {
  const n = Object.assign(new Node(tag), props);
  for (const k of kids) n.append(typeof k === "string" ? Object.assign(new Node("#text"), { textContent: k }) : k);
  return n;
};
const calls = [];
const notices = [];
let replies = {};
const api = async (method, path, body) => {
  calls.push({ method, path, body });
  const r = replies[`${method} ${path}`];
  if (r instanceof Error) throw r;
  return r || { ok: true };
};
const confirmButton = (label, armed, hint, act) => { const b = el("button", { textContent: label }); b.addEventListener("click", act); return b; };
seating.init({ el, api, say: (m, e) => notices.push({ m, e: !!e }), confirmButton });

const MAP = {
  id: 3, name: "Main Hall", seats: 5,
  sections: [
    { id: 12, map_id: 3, kind: "grid", name: "Stalls", tier: "Std", position: 0, rows: ["A", "B", "C", "D", "E", "F", "G", "H", "I", "J"], seats_per_row: 20, capacity: 0 },
    { id: 13, map_id: 3, kind: "zone", name: "Standing", tier: "", position: 1, rows: [], seats_per_row: 0, capacity: 80 },
  ],
};
const SEATS = [[[1, 0, "A", 1, false], [2, 0, "A", 2, true], [3, 1, "B", 1, false]], []];

(async () => {
  // row labels as the owner types them
  assert.equal(seating.rowsText(["A", "B", "C", "D", "E", "F", "G", "H", "I", "J"]), "A-J");
  assert.equal(seating.rowsText(["K", "L", "M"]), "K-M");
  assert.equal(seating.rowsText(["A", "C", "D"]), "A,C-D");
  assert.equal(seating.rowsText(["AZ", "BA"]), "AZ-BA");
  assert.equal(seating.rowsText(["9", "10", "11"]), "9-11");
  assert.equal(seating.rowsText([]), "");

  // the one line per section, as in the brief
  assert.equal(seating.sectionText(MAP.sections[0]), "Section Stalls  rows A-J x 20 seats  price tier Std");
  assert.equal(seating.sectionText(MAP.sections[1]), "GA zone Standing  capacity 80");

  // section requests from form values
  assert.deepEqual(seating.sectionRequest({ id: 0, kind: "grid", name: " Stalls ", tier: " Std ", rows: "A-J", seatsPerRow: "20", position: 2 }), {
    body: { id: 0, kind: "grid", name: "Stalls", tier: "Std", rows: "A-J", seats_per_row: 20, capacity: 0, position: 2 },
  });
  assert.deepEqual(seating.sectionRequest({ id: 13, kind: "zone", name: "Standing", tier: "", capacity: "80", rows: "A", seatsPerRow: 9 }), {
    body: { id: 13, kind: "zone", name: "Standing", tier: "", rows: "", seats_per_row: 0, capacity: 80, position: 0 },
  });
  for (const bad of [
    { kind: "grid", name: "", rows: "A", seatsPerRow: 5 },
    { kind: "grid", name: "S", rows: "", seatsPerRow: 5 },
    { kind: "grid", name: "S", rows: "A", seatsPerRow: 0 },
    { kind: "grid", name: "S", rows: "A", seatsPerRow: 101 },
    { kind: "grid", name: "S", rows: "A", seatsPerRow: 2.5 },
    { kind: "zone", name: "Z", capacity: 0 },
    { kind: "zone", name: "Z", capacity: 100001 },
  ]) assert.ok(seating.sectionRequest(bad).error, JSON.stringify(bad));

  // open lists the maps; editing one draws sections and the seats
  replies = { "GET /admin/maps": { maps: [MAP] }, "GET /admin/maps/3": { map: MAP, seats: SEATS } };
  await seating.open();
  assert.equal(els["#st-maps"].children.length, 1);
  assert.equal(els["#st-ed"].hidden, true);
  await els["#st-maps"].children[0].children[2].click(); // Edit
  assert.equal(els["#st-ed"].hidden, false);
  assert.equal(els["#st-name"].value, "Main Hall");
  assert.equal(els["#st-sections"].children[0].children[0].textContent, "Section Stalls  rows A-J x 20 seats  price tier Std");

  // grid render: blocked seats are marked, nothing is clickable until block mode
  const seats = els["#st-grid"].all((n) => n.tag === "button");
  assert.equal(seats.length, 3);
  assert.deepEqual(seats.map((b) => b.textContent), ["o", "b", "o"]);
  assert.equal(seats[1].className, "st-seat st-blocked");
  assert.equal(seats[1].attrs["aria-label"], "Row A seat 2, blocked");
  assert.ok(seats.every((b) => b.disabled));

  // block mode: a click blocks a free seat, a click on a blocked seat frees it
  await els["#st-block"].click();
  assert.equal(els["#st-block"].textContent, "Done blocking");
  let live = els["#st-grid"].all((n) => n.tag === "button");
  assert.ok(live.every((b) => !b.disabled));
  calls.length = 0;
  await live[0].click();
  assert.deepEqual(calls[0], { method: "POST", path: "/admin/sections/12/seats", body: { seats: [1], blocked: true } });
  live = els["#st-grid"].all((n) => n.tag === "button");
  await live[1].click();
  assert.deepEqual(calls.filter((c) => c.method === "POST").at(-1).body, { seats: [2], blocked: false });
  await els["#st-block"].click();
  assert.equal(els["#st-block"].textContent, "Block seat");

  // add a section: the form posts the request with the next position
  await els["#st-add-section"].click();
  assert.equal(els["#st-form"].hidden, false);
  const inputs = els["#st-form"].all((n) => n.tag === "input");
  assert.equal(inputs.length, 4);
  const [name, rows, per, tier] = inputs;
  name.value = "Balcony"; name.handlers.input();
  rows.value = "K-M"; rows.handlers.input();
  per.value = "14"; per.handlers.input();
  tier.value = "VIP"; tier.handlers.input();
  calls.length = 0;
  replies["GET /admin/maps/3"] = { map: { ...MAP, seats: 33, sections: [...MAP.sections, { ...MAP.sections[0], id: 14, name: "Balcony" }] }, seats: SEATS };
  const save = els["#st-form"].all((n) => n.tag === "button" && n.textContent === "Save section")[0];
  await save.click();
  assert.deepEqual(calls[0], { method: "POST", path: "/admin/maps/3/sections", body: { id: 0, kind: "grid", name: "Balcony", tier: "VIP", rows: "K-M", seats_per_row: 14, capacity: 0, position: 2 } });
  assert.equal(els["#st-form"].hidden, true);
  // the list line is refreshed with the saved sections
  assert.equal(els["#st-maps"].children[0].children[1].textContent, "33 seats, 3 sections");

  // a refusal shows the server's text
  await els["#st-add-zone"].click();
  const zin = els["#st-form"].all((n) => n.tag === "input");
  zin[0].value = "Standing"; zin[0].handlers.input();
  zin[1].value = "0"; zin[1].handlers.input();
  await els["#st-form"].all((n) => n.tag === "button" && n.textContent === "Save section")[0].click();
  assert.equal(notices.at(-1).m, "Capacity is a whole number from 1 to 100000.");
  assert.equal(notices.at(-1).e, true);
  zin[1].value = "80"; zin[1].handlers.input();
  replies["POST /admin/maps/3/sections"] = new Error("This section has sold seats; add a new section instead");
  await els["#st-form"].all((n) => n.tag === "button" && n.textContent === "Save section")[0].click();
  assert.equal(notices.at(-1).m, "This section has sold seats; add a new section instead");

  // add a map posts its name and opens it
  delete replies["POST /admin/maps/3/sections"];
  replies["POST /admin/maps"] = { ok: true, id: 3 };
  els["#st-new-name"].value = "Main Hall";
  calls.length = 0;
  await els["#st-add-map"].click();
  assert.deepEqual(calls[0], { method: "POST", path: "/admin/maps", body: { name: "Main Hall" } });
  assert.equal(els["#st-new-name"].value, "");

  console.log("seating ok");
})().catch((e) => { console.error(e); process.exit(1); });
