// Social feeds admin page. It runs in a sandboxed iframe and reaches the
// plugin only through the hatamoto bridge; all text is set with textContent.
// The page's policy blocks remote images, so cached items show as text tiles.
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

function ago(iso) {
  const secs = Math.max(0, (Date.now() - new Date(iso).getTime()) / 1000);
  if (secs < 90) return "just now";
  if (secs < 5400) return `${Math.round(secs / 60)} min ago`;
  if (secs < 129600) return `${Math.round(secs / 3600)} h ago`;
  return `${Math.round(secs / 86400)} days ago`;
}

const day = (iso) => (iso ? new Date(iso).toLocaleDateString() : "");

function row(dl, label, ...parts) {
  const dd = el("dd");
  for (const p of parts) if (p) dd.append(p);
  dl.append(el("dt", { textContent: label }), dd);
}

const text = (s) => el("span", { textContent: s });
const failure = (s) => el("div", { className: "error-text", textContent: `Error at last refresh: ${s}` });

function fillSources(st) {
  const dl = $("#sources");
  dl.replaceChildren();
  const ig = st.instagram;
  if (ig.connected) {
    row(dl, "Instagram",
      text(`Connected${ig.username ? ` as @${ig.username}` : ""}`),
      ig.expired ? el("div", { className: "error-text", textContent: "Token expired: connect again." })
        : ig.token_expires_at ? text(`, token renews before ${day(ig.token_expires_at)}`) : null,
      ig.refreshed_at ? el("div", { className: "muted", textContent: `refreshed ${ago(ig.refreshed_at)}` }) : null,
      ig.error ? failure(ig.error) : null);
  } else if (ig.configured) {
    row(dl, "Instagram", text("Not connected"));
  } else {
    row(dl, "Instagram", text("Not set up"),
      el("div", { className: "muted", textContent: `Add ${st.missing.instagram.join(", ")} in Settings.` }));
  }
  const yt = st.youtube;
  if (yt.configured) {
    row(dl, "YouTube", text(`Channel ${yt.channel_id}`),
      yt.refreshed_at ? el("div", { className: "muted", textContent: `refreshed ${ago(yt.refreshed_at)}` }) : null,
      yt.error ? failure(yt.error) : null);
  } else {
    row(dl, "YouTube", text("Not set up: add the API key and channel ID in Settings"));
  }
  $("#connect").hidden = (ig.connected && !ig.expired) || !ig.configured;
  $("#connect-help").hidden = true;
  $("#disconnect").hidden = !ig.connected;
  $("#disconnect").textContent = "Disconnect";
  $("#redirect").textContent = st.redirect_uri.startsWith("/") ? "set the site address in Settings first" : st.redirect_uri;
}

function tiles(target, items) {
  const box = $(target);
  box.replaceChildren();
  for (const it of items) {
    box.append(el("div", { className: "tile" },
      el("div", { className: "title", textContent: it.title || "(no title)" }),
      el("div", { className: "meta", textContent: [day(it.published_at), it.type].filter(Boolean).join(" · ") }),
      it.url ? el("code", { textContent: it.url }) : ""));
  }
}

function fillItems(st) {
  $("#instagram-title").textContent = `Instagram (${st.instagram.count})`;
  $("#youtube-title").textContent = `YouTube (${st.youtube.count})`;
  tiles("#instagram", st.items.instagram);
  tiles("#youtube", st.items.youtube);
  $("#empty").hidden = st.instagram.count + st.youtube.count > 0;
  if (st.error) say(st.error, true);
}

async function load() {
  const st = await api("GET", "/status");
  fillSources(st);
  fillItems(st);
}

async function main() {
  await hatamoto.ready;
  $("#connect").onclick = () => run(async () => {
    // Ask for the address; the owner opens it in a tab (a sandboxed page cannot open windows).
    const r = await api("GET", "/connect/instagram");
    const help = $("#connect-help");
    help.hidden = false;
    help.append(el("br"), el("code", { textContent: r.authorization_url }));
  });
  $("#disconnect").onclick = () => run(async () => {
    const btn = $("#disconnect");
    if (btn.textContent === "Disconnect") {
      btn.textContent = "Click again to forget the Instagram connection";
      return;
    }
    await api("POST", "/disconnect");
    await load();
  });
  $("#refresh").onclick = () => run(async () => {
    const btn = $("#refresh");
    btn.disabled = true;
    try {
      await api("POST", "/refresh-now");
      await load();
    } finally {
      btn.disabled = false;
    }
  });
  await load();
}

run(main);
