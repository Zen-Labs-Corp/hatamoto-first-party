// Downloads admin page. It runs in a sandboxed iframe and reaches the plugin
// only through the hatamoto bridge. Text is set with textContent and the
// sparklines are built with SVG DOM calls and presentation attributes only.
const $ = (sel) => document.querySelector(sel);
const SVG = "http://www.w3.org/2000/svg";
const LINK_PREFIX = "/p/downloads/f/";

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
  let data = {};
  try { data = JSON.parse(res.body || "{}"); } catch { /* not JSON */ }
  if (res.status >= 400) {
    const err = new Error(data.error || `HTTP ${res.status}`);
    err.data = data;
    throw err;
  }
  return data;
}

async function run(fn) {
  try { say(""); await fn(); } catch (e) { say(e.message, true); }
}

const num = (n) => Number(n).toLocaleString("en");

const SW = 120;
const SH = 24;
const SPAD = 2;

// sparkline draws the series as one polyline, or a flat line when all zero.
function sparkline(series) {
  const chart = svg("svg", { viewBox: `0 0 ${SW} ${SH}`, class: "spark", role: "img" });
  const max = Math.max(...series);
  if (max === 0) {
    chart.append(svg("line", { x1: SPAD, y1: SH - SPAD, x2: SW - SPAD, y2: SH - SPAD, class: "flat" }));
    return chart;
  }
  const x = (i) => SPAD + (i / Math.max(1, series.length - 1)) * (SW - 2 * SPAD);
  const y = (v) => SPAD + (1 - v / max) * (SH - 2 * SPAD);
  const points = series.map((v, i) => `${x(i).toFixed(1)},${y(v).toFixed(1)}`).join(" ");
  chart.append(svg("polyline", { points, class: "line" }));
  return chart;
}

// copyLink copies the link and says so on the button; when the clipboard is
// not available it shows the link in a field and selects it.
async function copyLink(button, id) {
  const link = LINK_PREFIX + id;
  try {
    await navigator.clipboard.writeText(link);
    button.textContent = "Copied";
    setTimeout(() => { button.textContent = "Copy link"; }, 2000);
  } catch {
    const field = el("input", { className: "fallback", readOnly: true, value: link });
    button.replaceWith(field);
    field.select();
  }
}

function row(f) {
  const button = el("button", { type: "button", textContent: "Copy link" });
  button.addEventListener("click", () => copyLink(button, f.id));
  return el("tr", {},
    el("td", { className: "name", textContent: f.filename }),
    el("td", { className: "num", textContent: f.size_label }),
    el("td", { className: "num", textContent: num(f.total) }),
    el("td", { className: "num", textContent: num(f.last30) }),
    el("td", {}, sparkline(f.series)),
    el("td", {}, button),
  );
}

let offset = 0;
let limit = 50;

function render(o) {
  $("#all").textContent = num(o.totals.all);
  $("#last30").textContent = num(o.totals.last30);
  $("#empty").hidden = o.total > 0;
  $("#files").hidden = o.files.length === 0;
  $("#rows").replaceChildren(...o.files.map(row));
  const pager = $("#pager");
  pager.hidden = o.total === 0;
  if (o.total > 0) {
    const from = o.files.length ? o.offset + 1 : o.offset;
    $("#range").textContent = `Files ${from}-${o.offset + o.files.length} of ${num(o.total)}`;
    $("#prev").disabled = o.offset === 0;
    $("#next").disabled = o.offset + o.limit >= o.total;
  }
  offset = o.offset;
  limit = o.limit;
}

async function load(to) {
  render(await api("GET", "/overview", null, { offset: String(to) }));
}

async function main() {
  await hatamoto.ready;
  $("#prev").addEventListener("click", () => run(() => load(Math.max(0, offset - limit))));
  $("#next").addEventListener("click", () => run(() => load(offset + limit)));
  await run(() => load(0));
}

main();
