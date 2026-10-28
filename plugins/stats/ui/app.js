// Install stats admin page. It runs in a sandboxed iframe and reaches the
// plugin only through the hatamoto bridge. Text is set with textContent and the
// charts are built with SVG DOM calls and presentation attributes only.
const $ = (sel) => document.querySelector(sel);
const SVG = "http://www.w3.org/2000/svg";

function el(tag, props = {}, ...kids) {
  const node = Object.assign(document.createElement(tag), props);
  for (const kid of kids) node.append(kid);
  return node;
}

function svg(tag, attrs = {}, text) {
  const node = document.createElementNS(SVG, tag);
  for (const [k, v] of Object.entries(attrs)) node.setAttribute(k, String(v));
  if (text !== undefined) node.textContent = text;
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

const ROW = 22;
const LABEL_W = 84;
const BAR_W = 100;
const MAX_LABEL = 12; // characters that fit before the bar

// shorten cuts a label to MAX_LABEL characters with an ellipsis.
function shorten(text) {
  const chars = Array.from(text);
  return chars.length <= MAX_LABEL ? text : `${chars.slice(0, MAX_LABEL - 1).join("")}…`;
}

// label draws text with the full text as a hover title when it was shortened.
function label(x, y, text) {
  const node = svg("text", { x, y, class: "label" }, shorten(text));
  if (shorten(text) !== text) node.append(svg("title", {}, text));
  return node;
}

// barRows draws one row per bucket: label, bar, count.
function barRows(container, buckets) {
  container.replaceChildren();
  if (!buckets.length) {
    container.append(el("p", { className: "muted", textContent: "No pings yet." }));
    return;
  }
  const max = Math.max(1, ...buckets.map((b) => b.count));
  const chart = svg("svg", { viewBox: `0 0 ${LABEL_W + BAR_W + 40} ${buckets.length * ROW}`, class: "chart", role: "img" });
  buckets.forEach((b, i) => {
    const y = i * ROW;
    const w = (b.count / max) * BAR_W;
    chart.append(
      label(0, y + 15, b.label),
      svg("rect", { x: LABEL_W, y: y + 3, width: w, height: ROW - 8, class: "bar" }),
      svg("text", { x: LABEL_W + w + 4, y: y + 15, class: "count" }, String(b.count)),
    );
  });
  container.append(chart);
}

const W = 400;
const H = 140;
const PAD_L = 34;
const PAD_R = 8;
const PAD_T = 8;
const PAD_B = 22;

const MIN_GAP = 14; // keep month labels clear of the y axis and the right edge

// monthTicks returns one short month name per calendar month, placed at the
// first of the month on the time axis (the points are evenly spaced weeks, so
// the labels are about 30 days apart). Labels too close to either end are left out.
function monthTicks(points, x) {
  const t0 = Date.parse(`${points[0].date}T00:00:00Z`);
  const span = Date.parse(`${points[points.length - 1].date}T00:00:00Z`) - t0;
  if (span <= 0) return [];
  const ticks = [];
  const start = new Date(t0);
  for (let m = new Date(Date.UTC(start.getUTCFullYear(), start.getUTCMonth(), 1)); m.getTime() <= t0 + span; m.setUTCMonth(m.getUTCMonth() + 1)) {
    if (m.getTime() < t0) continue;
    const at = x(((m.getTime() - t0) / span) * (points.length - 1));
    if (at < PAD_L + MIN_GAP || at > W - MIN_GAP) continue;
    ticks.push({ name: m.toLocaleString("en", { month: "short", timeZone: "UTC" }), at: at.toFixed(1) });
  }
  return ticks;
}

// trend draws the points as one polyline with one month label per month.
function trend(container, points) {
  container.replaceChildren();
  if (!points.length || points.every((p) => p.active === 0)) {
    container.append(el("p", { className: "muted", textContent: "No pings yet." }));
    return;
  }
  const max = Math.max(1, ...points.map((p) => p.active));
  const x = (i) => PAD_L + (points.length === 1 ? 0 : (i / (points.length - 1)) * (W - PAD_L - PAD_R));
  const y = (v) => PAD_T + (1 - v / max) * (H - PAD_T - PAD_B);
  const chart = svg("svg", { viewBox: `0 0 ${W} ${H}`, class: "chart trend", role: "img" });
  chart.append(
    svg("line", { x1: PAD_L, y1: y(0), x2: W - PAD_R, y2: y(0), class: "axis" }),
    svg("line", { x1: PAD_L, y1: y(max), x2: PAD_L, y2: y(0), class: "axis" }),
    svg("text", { x: PAD_L - 4, y: y(max) + 4, class: "tick end" }, String(max)),
    svg("text", { x: PAD_L - 4, y: y(0) + 4, class: "tick end" }, "0"),
    svg("polyline", { points: points.map((p, i) => `${x(i).toFixed(1)},${y(p.active).toFixed(1)}`).join(" "), class: "line" }),
  );
  points.forEach((p, i) => {
    if (p.active > 0) chart.append(svg("circle", { cx: x(i).toFixed(1), cy: y(p.active).toFixed(1), r: 2.5, class: "point" }));
  });
  monthTicks(points, x).forEach(({ name, at }) => chart.append(svg("text", { x: at, y: H - 6, class: "tick" }, name)));
  container.append(chart);
}

async function load() {
  const days = $("#days").value;
  const o = await api("GET", "/overview", null, { days });
  $("#active").textContent = String(o.active);
  $("#today").textContent = String(o.today);
  $("#new").textContent = String(o.new);
  $("#new-window").textContent = String(o.days);
  barRows($("#versions"), o.versions);
  barRows($("#channels"), o.active === 0 ? [] : o.channels);
  trend($("#trend"), o.trend);
}

async function main() {
  await hatamoto.ready;
  $("#days").addEventListener("change", () => run(load));
  await run(load);
}

main();
