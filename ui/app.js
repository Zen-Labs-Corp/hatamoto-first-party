// Search Console admin page. It runs in a sandboxed iframe and reaches the
// plugin only through the hatamoto bridge; all text is set with textContent.
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

const pct = (v) => `${(v * 100).toFixed(1)}%`;

function fillStatus(st) {
  const dl = $("#status");
  dl.replaceChildren();
  const add = (k, v) => dl.append(el("dt", { textContent: k }), el("dd", { textContent: v }));
  add("Connection", st.connected ? "connected" : "not connected");
  if (st.connected) {
    add("Property", st.site_url || "none picked yet");
    if (st.imported_through) add("Data imported through", st.imported_through);
    if (st.last_import) add("Last import", `${new Date(st.last_import.ran_at).toLocaleString()}: ${st.last_import.row_count} rows${st.last_import.error ? `, error: ${st.last_import.error}` : ""}`);
    if (st.last_sitemap) add("Last sitemap", `${st.last_sitemap.sitemap_url}${st.last_sitemap.error ? ` (failed: ${st.last_sitemap.error})` : ""}`);
  }
  const setup = $("#setup");
  setup.hidden = st.configured;
  if (!st.configured) {
    setup.textContent = `Fill in the settings above: ${st.missing.join(", ")}.` +
      (st.redirect_uri ? ` Authorised redirect address for your Google OAuth client: ${st.redirect_uri}` : "");
  }
  $("#connect").hidden = st.connected || !st.configured;
  $("#connect-help").hidden = true;
  $("#disconnect").hidden = !st.connected;
  $("#connected").hidden = !st.connected;
}

function rows(tableSel, list) {
  const body = $(`${tableSel} tbody`);
  body.replaceChildren();
  for (const r of list) {
    body.append(el("tr", {},
      el("td", { textContent: r.key }),
      el("td", { className: "num", textContent: String(r.clicks) }),
      el("td", { className: "num", textContent: String(r.impressions) }),
      el("td", { className: "num", textContent: pct(r.ctr) }),
      el("td", { className: "num", textContent: r.pos.toFixed(1) })));
  }
}

async function loadPerformance() {
  const perf = await api("GET", "/performance", null, { days: 28 });
  $("#range").textContent = `last ${perf.days} days`;
  rows("#queries", perf.queries);
  rows("#pages", perf.pages);
  $("#no-data").hidden = perf.queries.length > 0;
}

async function loadSites(current) {
  const select = $("#sites");
  select.replaceChildren();
  try {
    const { sites } = await api("GET", "/sites");
    for (const s of sites) {
      select.append(el("option", { value: s.siteUrl, textContent: `${s.siteUrl} (${s.permissionLevel})`, selected: s.siteUrl === current }));
    }
  } catch (e) {
    say(e.message, true);
  }
}

async function refresh() {
  const st = await api("GET", "/status");
  fillStatus(st);
  if (st.connected) {
    await loadSites(st.site_url);
    await loadPerformance();
  }
}

async function main() {
  await hatamoto.ready;
  $("#connect").onclick = () => run(async () => {
    // Ask for the address; the owner opens it in a tab (a sandboxed page cannot open windows).
    const r = await api("GET", "/oauth/start");
    $("#connect-help").hidden = false;
    $("#connect-help").append(el("br"), el("code", { textContent: r.authorization_url }));
  });
  $("#disconnect").onclick = () => run(async () => {
    if (!confirm("Forget the Google connection and its tokens?")) return;
    await api("POST", "/disconnect");
    await refresh();
  });
  $("#pick").onclick = () => run(async () => {
    await api("POST", "/site", { site_url: $("#sites").value });
    say("Property saved.");
    await refresh();
  });
  $("#sitemap").onsubmit = (e) => {
    e.preventDefault();
    run(async () => {
      const url = new FormData(e.target).get("sitemap_url");
      const r = await api("POST", "/sitemap", url ? { sitemap_url: url } : {});
      say(`Submitted ${r.sitemap_url}.`);
      await refresh();
    });
  };
  $("#inspect").onsubmit = (e) => {
    e.preventDefault();
    run(async () => {
      const r = await api("POST", "/inspect", { url: new FormData(e.target).get("url") });
      const dl = $("#inspection");
      dl.replaceChildren();
      for (const [k, v] of Object.entries(r)) if (v) dl.append(el("dt", { textContent: k.replaceAll("_", " ") }), el("dd", { textContent: v }));
    });
  };
  $("#import").onclick = () => run(async () => {
    const r = await api("POST", "/import");
    say(`Imported ${r.days.length} day(s), ${r.rows} rows${r.pending ? `; ${r.pending} more day(s) wait for the next run` : ""}.`);
    await refresh();
  });
  await refresh();
}

run(main);
