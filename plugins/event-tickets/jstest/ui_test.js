// Run with: node jstest/ui_test.js
// Runs ui/app.js against a minimal DOM stub; no dependencies. Lives outside
// ui/ because everything under ui/ is served.
"use strict";
const assert = require("node:assert/strict");
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
    this.textContent = "";
    this.classes = new Set();
    this.classList = { toggle: (c, on) => (on ? this.classes.add(c) : this.classes.delete(c)) };
  }
  append(...kids) { this.children.push(...kids); }
  replaceChildren(...kids) { this.children = kids; }
  addEventListener(type, fn) { this.handlers[type] = fn; }
  select() {}
}

const IDS = [
  "#notice", "#tab-events", "#tab-attendees", "#tab-settings", "#panel-events", "#panel-attendees", "#panel-settings",
  "#ev-search", "#ev-go", "#ev-state", "#ev-table", "#ev-rows", "#ev-more", "#ed", "#ed-title", "#ed-types", "#ed-add",
  "#ed-fields", "#ed-add-field", "#ed-save-fields", "#ed-close", "#at-event", "#at-state", "#at-table", "#at-rows",
  "#wl-state", "#wl-table", "#wl-rows", "#at-more", "#at-csv", "#st-rows",
];

// load runs the page; respond(method, path) returns {status, body}.
async function load({ respond, confirmAnswer = true }) {
  const els = {};
  for (const id of IDS) els[id] = new Node(id);
  const routes = [];
  const document = { querySelector: (s) => els[s], createElement: (t) => new Node(t), execCommand: () => true };
  const hatamoto = {
    ready: Promise.resolve(),
    route: async (method, p, body, query) => {
      routes.push({ method, p });
      return respond(method, p);
    },
  };
  const mod = { exports: {} };
  vm.runInNewContext(src, {
    document, hatamoto, module: mod, confirm: () => confirmAnswer,
    encodeURIComponent, Object, Number, String, Set, JSON, Error, Boolean,
  });
  await new Promise((r) => setTimeout(r, 10));
  routes.length = 0;
  return { els, routes, api: mod.exports };
}

const reply = (status, obj) => ({ status, body: JSON.stringify(obj) });
const quiet = (method, p) => reply(200, p.endsWith("/settings") ? {} : { events: [], attendees: [], waitlist: [], ok: true });

(async () => {
  // the CSV path is built from an encoded event id
  let { api } = await load({ respond: quiet });
  assert.equal(api.csvPath("12"), "/api/v1/p/event-tickets/admin/events/12/attendees.csv");
  assert.equal(api.csvPath("a/b c?"), "/api/v1/p/event-tickets/admin/events/a%2Fb%20c%3F/attendees.csv");

  // a resend the route refuses shows the route's message
  let ctx = await load({ respond: (m, p) => (p.endsWith("/resend") ? reply(429, { error: "resent recently; try again in a few minutes" }) : quiet(m, p)) });
  await ctx.api.resendAttendee("7");
  assert.equal(ctx.els["#notice"].textContent, "resent recently; try again in a few minutes");
  assert.ok(ctx.els["#notice"].classes.has("error") && !ctx.els["#notice"].hidden);
  assert.deepEqual(ctx.routes[0], { method: "POST", p: "/admin/attendees/7/resend" });

  // cancel calls the route only after the editor confirms
  ctx = await load({ respond: quiet, confirmAnswer: false });
  await ctx.api.cancelAttendee("7");
  assert.equal(ctx.routes.filter((r) => r.p.endsWith("/cancel")).length, 0, "declined: no call");
  ctx = await load({ respond: quiet, confirmAnswer: true });
  await ctx.api.cancelAttendee("7");
  assert.deepEqual(ctx.routes[0], { method: "POST", p: "/admin/attendees/7/cancel" });

  console.log("ok");
})().catch((e) => { console.error(e); process.exit(1); });
