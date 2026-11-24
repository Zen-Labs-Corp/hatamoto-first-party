// Checks the moderation admin page (ui/) without a browser. No dependencies:
//   node moderation_ui_test.js
// Lives outside ui/ because everything under ui/ is served to owner sessions.
"use strict";
const fs = require("fs");
const path = require("path");
const vm = require("vm");

const read = (f) => fs.readFileSync(path.join(__dirname, "ui", f), "utf8");
const files = { "index.html": read("index.html"), "app.js": read("app.js"), "app.css": read("app.css") };
let failures = 0;

function check(name, cond) {
  if (!cond) { failures++; console.error("FAIL: " + name); } else console.log("ok: " + name);
}

// ---- static ----------------------------------------------------------------

const banned = ["innerHTML", "outerHTML", "insertAdjacentHTML", "document.write", "eval(", "new Function", "style=", ".style.", ".style[", "cssText",
  "onclick=", "onload=", "onchange=", "onsubmit=", "javascript:", "fetch(", "XMLHttpRequest", "<style"];
for (const [name, src] of Object.entries(files)) {
  for (const bad of banned) check(`${name} has no ${bad}`, !src.includes(bad));
  check(`${name} has no remote host`, !/https?:\/\//.test(src));
}
const scripts = files["index.html"].match(/<script\b[^>]*>/g) || [];
check("two script tags", scripts.length === 2);
check("bridge.js first", /src="\/plugin-ui\/_sdk\/bridge\.js"/.test(scripts[0] || ""));
check("app.js second", /src="app\.js"/.test(scripts[1] || ""));
check("no inline script body", !/<script[^>]*>\s*[^<\s]/.test(files["index.html"]));
for (const route of ["/admin/queue", "/admin/log", "/admin/items/kill", "/admin/items/restore", "/admin/users/", "/ban", "/unban", "/admin/domains", "/admin/domains/remove",
  "/admin/nets/ban", "/admin/nets/unban", "/admin/jobs"]) {
  check(`app.js uses ${route}`, files["app.js"].includes(route));
}

// ---- dynamic ---------------------------------------------------------------

function node(tag) {
  const n = {
    tag, children: [], textContent: "", value: "", hidden: false, attributes: {}, listeners: {}, className: "",
    append(...kids) { this.children.push(...kids); },
    replaceChildren(...kids) { this.children = kids; },
    setAttribute(k, v) { this.attributes[k] = v; },
    addEventListener(type, fn) { this.listeners[type] = fn; },
    classList: { toggle() {} },
  };
  return n;
}

function walk(n, fn) {
  if (typeof n === "string") return fn(n, null);
  fn(n.textContent, n);
  for (const kid of n.children) walk(kid, fn);
}
const texts = (n) => { const out = []; walk(n, (t) => { if (t) out.push(t); }); return out; };
const find = (n, tag, text) => { let hit = null; walk(n, (_, x) => { if (x && !hit && x.tag === tag && x.textContent === text) hit = x; }); return hit; };

const fixtures = {
  "GET /admin/queue:queue": { items: [
    { kind: "story", id: "11111111-1111-1111-1111-111111111111", item_no: 7, title: "<b>x</b> story", by: "<i>bob</i>", flags: 5, dead: false, cause: "" },
    { kind: "comment", id: "22222222-2222-2222-2222-222222222222", item_no: 7, title: "a comment", by: "", flags: 2, dead: true, cause: "flags" },
  ] },
  "GET /admin/queue:users": { users: [{ name: "<b>x</b>", mode: "dead", reason: "spam", created_at: "2026-01-02T03:04:05Z" }] },
  "GET /admin/queue:domains": { domains: [{ domain: "example.com", reason: "spam", created_at: "2026-01-02T03:04:05Z" }] },
  "GET /admin/queue:nets": { bans: [{ net_key: "AbCdEfGh12345678", reason: "flood", expires_at: null }], noisy: [{ net_key: "ZyXwVuTs87654321", events: 90 }] },
  "GET /admin/log:": { log: [{ at: "2026-01-02T03:04:05Z", actor: "owner", action: "ban", target: "user:<b>x</b>", detail: "dead" }] },
};

async function boot() {
  const calls = [];
  const cache = {};
  const document = {
    createElement: node,
    querySelector(sel) { return cache[sel] || (cache[sel] = node("slot")); },
  };
  const hatamoto = {
    ready: Promise.resolve({}),
    route: async (method, p, body, query) => {
      calls.push({ method, path: p, body, query });
      const key = `${method} ${p}:${(query && query.view) || ""}`;
      const data = fixtures[key] || { ok: true, path: "/item/9" };
      return { status: 200, body: JSON.stringify(data) };
    },
  };
  const ctx = vm.createContext({ document, hatamoto, globalThis: null, __hnTest: {}, encodeURIComponent, Date, Number, String, JSON, Promise, Object, Boolean, Error });
  ctx.globalThis = ctx;
  vm.runInContext(files["app.js"], ctx);
  const flush = () => new Promise((r) => setImmediate(r));
  await flush();
  return { ctx, cache, calls, flush, document };
}

(async () => {
  const t = await boot();
  const tabs = ["queue", "users", "domains", "nets", "jobs", "log"];
  const labels = ["Queue", "Users", "Domains", "Networks", "Jobs", "Log"];
  check("six tabs in the page", labels.every((l, i) => files["index.html"].includes(`id="tab-${tabs[i]}" type="button" role="tab" aria-selected="${i === 0}">${l}</button>`)));
  check("render functions exposed", Object.keys(t.ctx.__hnTest.render || {}).length === 6);
  check("queue loaded at start", t.calls.length === 1 && t.calls[0].path === "/admin/queue" && t.calls[0].query.view === "queue");

  const queue = t.cache["#body-queue"].children[0];
  const queueText = texts(queue);
  check("queue rows render", queueText.includes("#7") && queueText.includes("5 flags") && queueText.includes("dead (flags)"));
  check("markup stays text", queueText.includes("<b>x</b> story") && queueText.includes("<i>bob</i>") && !queue.children.some((c) => c.tag === "b"));
  check("no ban button without author", (() => { let n = 0; walk(queue, (_, x) => { if (x && x.textContent === "ban user") n++; }); return n === 1; })());

  const before = t.calls.length;
  find(queue, "button", "kill").listeners.click();
  await t.flush();
  const kill = t.calls[before];
  check("kill posts JSON", kill.method === "POST" && kill.path === "/admin/items/kill" && kill.body === JSON.stringify({ kind: "story", id: "11111111-1111-1111-1111-111111111111" }));
  check("queue reloaded after kill", t.calls[t.calls.length - 1].path === "/admin/queue");

  const n0 = t.calls.length;
  find(queue, "button", "ban user").listeners.click();
  await t.flush();
  check("ban user path is encoded", t.calls[n0].path === "/admin/users/" + encodeURIComponent("<i>bob</i>") + "/ban" && JSON.parse(t.calls[n0].body).mode === "dead");

  for (const name of tabs) {
    t.cache[`#tab-${name}`].listeners.click();
    await t.flush();
    check(`tab ${name} shown`, t.cache[`#panel-${name}`].hidden === false && t.cache[`#tab-${name}`].attributes["aria-selected"] === "true"
      && tabs.every((o) => o === name || t.cache[`#panel-${o}`].hidden === true));
  }
  const users = texts(t.cache["#body-users"]);
  check("users render", users.includes("<b>x</b>") && users.includes("spam") && users.some((s) => s.includes("2026")));
  check("domains render", texts(t.cache["#body-domains"]).includes("example.com"));
  const nets = texts(t.cache["#body-nets"]);
  check("networks render", nets.includes("AbCdEfGh12345678") && nets.includes("ZyXwVuTs87654321") && nets.includes("90") && nets.includes("never"));
  check("jobs form renders", find(t.cache["#body-jobs"], "button", "post job") !== null);
  check("log renders", texts(t.cache["#body-log"]).includes("user:<b>x</b>"));

  const n1 = t.calls.length;
  find(t.cache["#body-users"], "button", "unban").listeners.click();
  await t.flush();
  check("unban posts an empty object", t.calls[n1].path === "/admin/users/" + encodeURIComponent("<b>x</b>") + "/unban" && t.calls[n1].body === "{}");

  const n2 = t.calls.length;
  find(t.cache["#body-nets"], "button", "unblock").listeners.click();
  await t.flush();
  check("unblock posts the key", JSON.parse(t.calls[n2].body).net_key === "AbCdEfGh12345678");

  const n3 = t.calls.length;
  find(t.cache["#body-domains"], "button", "remove").listeners.click();
  await t.flush();
  check("remove posts the domain", t.calls[n3].path === "/admin/domains/remove" && JSON.parse(t.calls[n3].body).domain === "example.com");

  find(t.cache["#body-jobs"], "button", "post job").listeners.click();
  await t.flush();
  const job = t.calls.find((c) => c.path === "/admin/jobs");
  check("job posts", job && job.method === "POST" && Object.keys(JSON.parse(job.body)).join() === "title,url,text,by");
  check("job notice names the item", t.cache["#notice"].textContent === "Posted: /item/9");

  if (failures) { console.error(`${failures} failed`); process.exit(1); }
  console.log("all passed");
})().catch((e) => { console.error(e); process.exit(1); });
