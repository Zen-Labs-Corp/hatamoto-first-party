// Reviews admin page. It runs in a sandboxed iframe and reaches the plugin only
// through the hatamoto bridge; all text is set with textContent.
const $ = (sel) => document.querySelector(sel);

function el(tag, props = {}, ...kids) {
  const node = Object.assign(document.createElement(tag), props);
  for (const kid of kids) node.append(kid);
  return node;
}

function say(msg, isError) {
  const n = $("#notice");
  n.textContent = msg;
  n.classList.toggle("error", Boolean(isError));
  n.hidden = !msg;
}

async function api(method, path) {
  const res = await hatamoto.route(method, path, null);
  let data = {};
  try { data = JSON.parse(res.body || "{}"); } catch { /* not JSON */ }
  if (res.status >= 400) throw new Error(data.error || `HTTP ${res.status}`);
  return data;
}

async function run(fn) {
  try { say(""); await fn(); } catch (e) { say(e.message, true); }
}

function ago(iso) {
  const t = Date.parse(iso);
  if (Number.isNaN(t)) return "";
  const mins = Math.max(0, Math.round((Date.now() - t) / 60000));
  if (mins < 60) return `${mins} min ago`;
  const hours = Math.round(mins / 60);
  if (hours < 48) return `${hours} h ago`;
  return `${Math.round(hours / 24)} days ago`;
}

const stars = (n) => "★".repeat(n) + "☆".repeat(5 - n);
const noun = (n) => (n === 1 ? "review" : "reviews");

function sourceRow(s) {
  const detail = el("span", {});
  const state = el("span", { className: "state" });
  switch (s.state) {
    case "connected":
      state.textContent = "Connected";
      state.classList.add("ok");
      detail.textContent = `${s.rating} ★ from ${s.count} ${noun(s.count)}  refreshed ${ago(s.refreshed_at)}`;
      break;
    case "pending":
      state.textContent = "Waiting";
      detail.textContent = "The first refresh has not run yet. Use Refresh now.";
      break;
    case "error":
      state.textContent = "Error";
      state.classList.add("error");
      detail.textContent = `Error at last refresh: ${s.last_error || "unknown"}, ${ago(s.refreshed_at)}`;
      break;
    default:
      detail.textContent = `Not set up: add the ${s.missing || "API key and ID"} in Settings`;
  }
  return el("div", { className: "source" }, el("span", { className: "name", textContent: s.label }), state, detail);
}

function reviewRow(r) {
  return el("article", { className: "review" },
    el("div", { className: "head" },
      el("span", { className: "stars", textContent: stars(r.rating) }),
      el("span", { className: "author", textContent: r.author }),
      el("span", { className: "muted", textContent: `${r.source_label}  ${r.date}` })),
    r.text ? el("p", { className: "text", textContent: `“${r.text}”` }) : "");
}

function render(o) {
  $("#sources").replaceChildren(...(o.sources || []).map(sourceRow));
  $("#notes").replaceChildren(
    el("div", { textContent: `Google returns at most ${o.google_limit} reviews per place (Places API limit).` }),
    el("div", { textContent: `Showing reviews rated ${o.min_rating} and above (min_rating), up to ${o.max_items} (max_items).` }));
  const reviews = o.reviews || [];
  $("#list").replaceChildren(...(reviews.length
    ? reviews.map(reviewRow)
    : [el("p", { className: "empty muted", textContent: "No reviews cached yet. Set up a source in Settings, then Refresh now." })]));
  if (o.error) say(`Reviews are ${o.error}.`, true);
}

async function load() {
  render(await api("GET", "/overview"));
}

async function main() {
  await hatamoto.ready;
  $("#refresh").addEventListener("click", () => run(async () => {
    const btn = $("#refresh");
    btn.disabled = true;
    try { render(await api("POST", "/refresh-now")); } finally { btn.disabled = false; }
  }));
  await run(load);
}

main();
