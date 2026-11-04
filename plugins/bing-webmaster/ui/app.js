// Bing Webmaster Tools admin page. It runs in a sandboxed iframe and reaches
// the plugin only through the hatamoto bridge; all text is set with textContent.
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

async function api(method, path, body) {
  const res = await hatamoto.route(method, path, body ?? null);
  let data = {};
  try { data = JSON.parse(res.body || "{}"); } catch { /* not JSON */ }
  if (res.status >= 400) throw new Error(data.error || `HTTP ${res.status}`);
  return data;
}

const MONTHS = ["Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"];
const pad = (n) => String(n).padStart(2, "0");
const num = (n) => Number(n).toLocaleString();

function fmtDate(iso) {
  const d = new Date(iso);
  return `${d.getDate()} ${MONTHS[d.getMonth()]} ${d.getFullYear()}`;
}

function fmtDateTime(iso) {
  const d = new Date(iso);
  return `${fmtDate(iso)} ${pad(d.getHours())}:${pad(d.getMinutes())}`;
}

function fill(tableSel, list, emptySel, cells) {
  const body = $(`${tableSel} tbody`);
  body.replaceChildren();
  for (const item of list) {
    body.append(el("tr", {}, ...cells(item).map(([text, cls]) => el("td", { className: cls || "", textContent: text }))));
  }
  if (emptySel) $(emptySel).hidden = list.length > 0;
}

function render(o) {
  $("#not-set").hidden = o.api_key_set;
  $("#cards").hidden = !o.api_key_set;
  if (!o.api_key_set) return;

  $("#key-state").textContent = "Set";
  const source = o.site_source === "setting" ? "from Settings" : "from your Bing account";
  $("#site").textContent = o.site_url ? `${o.site_url} (${source})` : "not known yet";
  const run = o.last_import;
  $("#last-import").textContent = run ? `${fmtDateTime(run.at)}, ${run.ok ? "ok" : `failed: ${run.error}`}` : "never";
  if (o.site_error) say(o.site_error, true);

  $("#total-clicks").textContent = num(o.totals.clicks);
  $("#total-impressions").textContent = num(o.totals.impressions);
  fill("#days", o.traffic, "#no-traffic", (r) => [[r.day], [num(r.clicks), "num"], [num(r.impressions), "num"]]);
  fill("#queries", o.top_queries, null, (r) => [[r.key], [num(r.clicks), "num"], [num(r.impressions), "num"], [r.position.toFixed(1), "num"]]);
  fill("#pages", o.top_pages, null, (r) => [[r.key], [num(r.clicks), "num"], [num(r.impressions), "num"]]);
  fill("#issues", o.crawl_issues, "#no-issues", (r) => [[r.url], [String(r.http_code), "num"], [r.labels.join(", ")], [num(r.in_links), "num"]]);
  fill("#feeds", o.feeds, "#no-feeds", (r) => [[r.url], [r.status], [num(r.url_count), "num"], [r.last_crawled_at ? fmtDate(r.last_crawled_at) : "never"]]);
  const sub = o.last_submission;
  $("#last-submitted").textContent = sub ? `Last submitted ${fmtDateTime(sub.at)}${sub.error ? ` (failed: ${sub.error})` : ""}` : "Not submitted from here yet";
}

async function refresh() {
  render(await api("GET", "/overview"));
}

// act runs fn with the buttons disabled and shows what went wrong.
async function act(fn) {
  const buttons = document.querySelectorAll("button");
  buttons.forEach((b) => { b.disabled = true; });
  try {
    say("");
    await fn();
  } catch (e) {
    say(e.message, true);
  } finally {
    buttons.forEach((b) => { b.disabled = false; });
  }
}

async function main() {
  await hatamoto.ready;
  $("#test").onclick = () => act(async () => {
    const r = await api("POST", "/test");
    const n = r.sites_verified;
    $("#status-line").textContent = `Key works. ${n} verified site${n === 1 ? "" : "s"}.` +
      (r.site_url_ok ? "" : " The Site URL does not match one verified site; check Settings.");
  });
  $("#import-now").onclick = () => act(async () => {
    const r = await api("POST", "/import-now");
    await refresh();
    if (r.error) say(`Import finished with problems: ${r.error}`, true);
    else if (!r.done) say("More to import, run again or wait for the next scheduled import.");
    else $("#status-line").textContent = `Import done, ${num(r.rows)} rows.`;
  });
  $("#submit").onclick = () => act(async () => {
    await api("POST", "/submit-sitemap");
    await refresh();
    $("#status-line").textContent = "Sitemap submitted.";
  });
  await act(refresh);
}

main();
