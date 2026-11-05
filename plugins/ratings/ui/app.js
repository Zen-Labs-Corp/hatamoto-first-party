// Star ratings admin page. It runs in a sandboxed iframe and reaches the
// plugin only through the hatamoto bridge. The page paths come from public
// form posts, so every value is set with textContent, never as markup.
const $ = (sel) => document.querySelector(sel);

function cell(text, className) {
  const td = document.createElement("td");
  td.textContent = String(text);
  if (className) td.className = className;
  return td;
}

function row(page) {
  const tr = document.createElement("tr");
  tr.append(cell(page.path || page.entity_id));
  tr.append(cell(page.average, "num"));
  tr.append(cell(page.count, "num"));
  for (const n of page.dist || []) tr.append(cell(n, "num"));
  return tr;
}

function say(msg) {
  const n = $("#notice");
  n.textContent = msg;
  n.hidden = !msg;
}

async function load() {
  await hatamoto.ready;
  let pages = [];
  try {
    const res = await hatamoto.route("GET", "/overview", null, {});
    if (res.status >= 400) throw new Error(`HTTP ${res.status}`);
    pages = JSON.parse(res.body || "{}").pages || [];
  } catch (e) {
    say(`Could not load the ratings: ${e.message}`);
  }
  const rows = $("#rows");
  rows.replaceChildren(...pages.map(row));
  $("#table").hidden = pages.length === 0;
  $("#empty").hidden = pages.length !== 0;
}

load();
