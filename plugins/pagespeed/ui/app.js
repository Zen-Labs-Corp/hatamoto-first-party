// PageSpeed admin page. It runs in a sandboxed iframe and reaches the plugin
// only through the hatamoto bridge. Text is set with textContent and the
// sparklines are built with SVG DOM calls and presentation attributes only.
const $ = (sel) => document.querySelector(sel);
const SVG = "http://www.w3.org/2000/svg";

const LABELS = { good: "Good", "needs-improvement": "Needs improvement", poor: "Poor" };
const METRICS = ["lcp", "inp", "cls"];
const NOTICES = {
  no_api_key: "Add an API key in Settings to load field data.",
  no_site_address: "Set the site address in Settings > General or the Public URL setting.",
  entities_unavailable: "The page list could not be read, so only the home page is tracked. List the pages in Settings, or allow reading entities.",
  no_data: "First measurements run within the next minutes, or use Measure now.",
};

let data = null;
let strategy = "mobile";

function el(tag, props = {}, ...kids) {
  const node = Object.assign(document.createElement(tag), props);
  for (const kid of kids) node.append(kid);
  return node;
}

function svg(tag, attrs = {}) {
  const node = document.createElementNS(SVG, tag);
  for (const [k, v] of Object.entries(attrs)) node.setAttribute(k, String(v));
  return node;
}

function say(msg, isError) {
  const n = $("#notice");
  n.textContent = msg;
  n.classList.toggle("error", Boolean(isError));
  n.hidden = !msg;
}

async function api(method, path, body, query) {
  const res = await hatamoto.route(method, path, body ?? null, query);
  let out = {};
  try { out = JSON.parse(res.body || "{}"); } catch { /* not JSON */ }
  if (res.status >= 400) throw new Error(out.error || `HTTP ${res.status}`);
  return out;
}

async function run(fn) {
  try { say(""); await fn(); } catch (e) { say(e.message, true); }
}

function format(metric, v) {
  if (metric === "cls") return v.toFixed(2).replace(/0$/, "").replace(/\.$/, ".0");
  if (metric === "lcp") return `${(v / 1000).toFixed(1)} s`;
  return `${Math.round(v)} ms`;
}

function badge(cat) {
  return el("span", { className: `badge ${cat}`, textContent: LABELS[cat] || "" });
}

// valueCell shows a p75 value, its badge and a 90-day sparkline.
function valueNode(metric, value, points) {
  const box = el("div", { className: "metric" });
  if (!value) {
    box.append(el("span", { className: "muted", textContent: "No data" }));
    return box;
  }
  box.append(el("span", { className: "value", textContent: format(metric, value.value) }), " ", badge(value.cat));
  if (points && points.length) box.append(sparkline(metric, points));
  return box;
}

const W = 96;
const H = 24;
const PAD = 2;

function sparkline(metric, points) {
  const root = svg("svg", { viewBox: `0 0 ${W} ${H}`, width: W, height: H, class: `spark ${metric}`, role: "img" });
  root.append(svg("title"));
  root.firstChild.textContent = `${metric.toUpperCase()}, last ${points.length} days with data`;
  const vs = points.map((p) => p.v);
  const lo = Math.min(...vs);
  const hi = Math.max(...vs);
  const span = hi - lo || 1;
  const x = (i) => (points.length === 1 ? W / 2 : PAD + (i * (W - 2 * PAD)) / (points.length - 1));
  const y = (v) => H - PAD - ((v - lo) * (H - 2 * PAD)) / span;
  if (points.length === 1) {
    root.append(svg("circle", { cx: x(0), cy: y(vs[0]), r: 2, class: "dot" }));
  } else {
    root.append(svg("polyline", { points: vs.map((v, i) => `${x(i).toFixed(1)},${y(v).toFixed(1)}`).join(" "), class: "line" }));
  }
  return root;
}

function labNode(d) {
  if (d.lab_status === "ok" && d.lab_score !== null) {
    const tier = d.lab_score >= 90 ? "good" : d.lab_score >= 50 ? "needs-improvement" : "poor";
    return el("span", { className: `score ${tier}`, textContent: String(d.lab_score) });
  }
  const text = { timeout: "Lab timed out", error: "Lab failed" }[d.lab_status] || "Waiting";
  return el("span", { className: "muted", textContent: text });
}

function fillOrigin() {
  const box = $("#origin");
  box.replaceChildren();
  const o = data.origin[strategy];
  if (!o || o.status !== "ok") {
    box.append(el("span", { className: "muted", textContent: o ? "Not enough field data" : "No measurement yet" }));
    return;
  }
  for (const m of METRICS) {
    const wrap = el("span", { className: "origin-metric" });
    wrap.append(el("strong", { textContent: m.toUpperCase() }), " ");
    wrap.append(valueNode(m, o[m], null));
    box.append(wrap);
  }
}

function row(entry) {
  const d = entry[strategy];
  const tr = el("tr");
  tr.append(el("td", {}, el("code", { textContent: entry.path })));
  if (d.field_status === "ok") {
    for (const m of METRICS) tr.append(el("td", {}, valueNode(m, d[m], d.series[m])));
  } else {
    const text = { none: "Not enough field data", error: "Field data unavailable" }[d.field_status] || "Waiting for the first measurement";
    tr.append(el("td", { colSpan: 3, className: "muted", textContent: text }));
  }
  tr.append(el("td", {}, labNode(d)));
  const btn = el("button", { type: "button", textContent: "Measure now" });
  btn.addEventListener("click", () => run(async () => {
    btn.disabled = true;
    btn.textContent = "Measuring...";
    try {
      await api("POST", "/measure-now", { url: entry.path });
    } finally {
      btn.disabled = false;
      btn.textContent = "Measure now";
    }
    await load();
  }));
  tr.append(el("td", {}, btn));
  return tr;
}

function render() {
  const box = $("#notices");
  box.replaceChildren(...data.notices.map((n) => el("p", { className: "notice", textContent: NOTICES[n] || n })));
  $("#urls").hidden = data.urls.length === 0;
  fillOrigin();
  $("#urls tbody").replaceChildren(...data.urls.map(row));
  const last = data.last_run;
  const when = last.at ? `${last.at.replace("T", " ").replace("Z", "")} UTC` : "not yet";
  $("#footer").textContent = `Last run: ${when}, ${last.done}/${last.total} URLs done. Thresholds: web.dev`;
}

async function load() {
  data = await api("GET", "/overview");
  const sel = $("#strategy");
  if (!data.strategies.includes(strategy)) strategy = data.default_strategy;
  sel.replaceChildren(...data.strategies.map((s) => el("option", { value: s, textContent: s[0].toUpperCase() + s.slice(1), selected: s === strategy })));
  $("#strategy-box").hidden = data.strategies.length < 2;
  render();
}

$("#strategy").addEventListener("change", (e) => {
  strategy = e.target.value;
  render();
});

hatamoto.ready.then(() => run(load));
