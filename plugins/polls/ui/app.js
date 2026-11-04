// Polls admin page. It runs in a sandboxed iframe and reaches the plugin only
// through the hatamoto bridge. Text is set with textContent and the bars are
// <progress> elements, so no inline style or HTML string is needed.
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

async function call(method, path, body, query) {
  const res = await hatamoto.route(method, path, body ?? null, query ?? {});
  if (res.status >= 400) {
    let msg = `HTTP ${res.status}`;
    try { msg = JSON.parse(res.body).error || msg; } catch { /* not JSON */ }
    throw new Error(msg);
  }
  return res;
}

async function api(method, path, body, query) {
  const res = await call(method, path, body, query);
  try { return JSON.parse(res.body || "{}"); } catch { return {}; }
}

async function run(fn) {
  try { say(""); await fn(); } catch (e) { say(e.message, true); }
}

const KINDS = { poll: "poll", survey: "survey", quiz: "quiz" };
const SHOWN = { always: "always", after_close: "after close", never: "never" };

function plural(n, one, many) {
  return n === 1 ? `1 ${one}` : `${n} ${many}`;
}

// bar is one row: label, bar, count and share.
function bar(label, count, percent, correct = false) {
  const row = el("div", { className: "bar-row" });
  row.append(
    el("span", { className: "bar-label", textContent: correct ? `${label} (correct)` : label }),
    el("progress", { max: 100, value: percent, className: correct ? "good" : "" }),
    el("span", { className: "bar-count", textContent: `${count}` }),
    el("span", { className: "bar-percent muted", textContent: `${percent}%` }),
  );
  return row;
}

function showCSV(slug, text) {
  $("#csv-title").textContent = `CSV for ${slug}`;
  const box = $("#csv");
  box.value = text;
  $("#csv-box").hidden = false;
  box.focus();
  box.select();
}

async function exportCSV(slug) {
  const res = await call("GET", `/forms/${encodeURIComponent(slug)}/csv`);
  showCSV(slug, res.body);
}

async function recount(slug) {
  const r = await api("POST", `/forms/${encodeURIComponent(slug)}/recount`);
  await load();
  say(`Recounted ${plural(r.responses ?? 0, "response", "responses")} for ${slug}.`);
}

function card(f) {
  const c = el("section", { className: "card" });
  c.append(el("div", { className: "head" },
    el("strong", { textContent: f.name }),
    el("span", { className: "muted", textContent: `${KINDS[f.kind] || f.kind} · results: ${SHOWN[f.show_results] || f.show_results}` }),
    el("span", { className: "muted total", textContent: plural(f.responses, "response", "responses") }),
  ));
  if (f.note) c.append(el("p", { className: "muted", textContent: f.note }));
  for (const q of f.questions) {
    c.append(el("h3", { textContent: q.label }));
    for (const o of q.options) c.append(bar(o.label, o.count, o.percent, o.correct));
  }
  if (f.kind === "quiz" && f.scores.length) {
    c.append(el("h3", { textContent: "Score distribution" }));
    const total = f.scores.reduce((sum, s) => sum + s.count, 0);
    for (const s of f.scores) c.append(bar(`${s.score}/${f.max_score}`, s.count, total ? Math.round((s.count * 100) / total) : 0));
    c.append(el("p", { className: "muted", textContent: `Average ${f.average} / ${f.max_score}` }));
  }
  const recountBtn = el("button", { type: "button", textContent: "Recount" });
  recountBtn.addEventListener("click", async () => {
    recountBtn.disabled = true;
    try { await run(() => recount(f.slug)); } finally { recountBtn.disabled = false; }
  });
  const csvBtn = el("button", { type: "button", textContent: "Export CSV" });
  csvBtn.addEventListener("click", () => run(() => exportCSV(f.slug)));
  c.append(el("div", { className: "actions" }, recountBtn, csvBtn));
  return c;
}

async function load() {
  const o = await api("GET", "/overview");
  const root = $("#forms");
  root.replaceChildren();
  if (!o.forms || o.forms.length === 0) {
    root.append(el("p", { className: "muted", textContent: "No forms set up yet. Add one under Settings > forms." }));
    return;
  }
  for (const f of o.forms) root.append(card(f));
}

async function main() {
  await hatamoto.ready;
  await run(load);
}

main();
