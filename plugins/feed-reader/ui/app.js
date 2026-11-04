// Feeds admin page. It runs in a sandboxed iframe and reaches the plugin only
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

function ago(iso) {
  if (!iso) return "never";
  const min = Math.max(0, Math.round((Date.now() - new Date(iso).getTime()) / 60000));
  if (min < 1) return "just now";
  if (min < 60) return `${min} min ago`;
  if (min < 1440) return `${Math.round(min / 60)} h ago`;
  return `${Math.round(min / 1440)} d ago`;
}

// Statuses are fixed phrases from the plugin; anything but these is a failure.
const calm = ["ok", "not modified", "pending", "waiting for the next run"];

function statusCell(status) {
  if (status === "ok") return el("td", { className: "state-ok", textContent: "OK" });
  if (!calm.includes(status)) return el("td", { className: "state-error", textContent: `Error: ${status}` });
  return el("td", { textContent: status.charAt(0).toUpperCase() + status.slice(1) });
}

function showFeeds(feeds) {
  $("#empty").hidden = feeds.length > 0;
  $("#feeds").hidden = feeds.length === 0;
  const body = $("#feeds tbody");
  body.replaceChildren();
  for (const f of feeds) {
    body.append(el("tr", {},
      el("td", { textContent: f.name, title: f.url }),
      el("td", { textContent: f.group ? String(f.group) : "-" }),
      el("td", { textContent: ago(f.last_fetch) }),
      statusCell(f.status),
      el("td", { className: "num", textContent: String(f.items) })));
  }
}

function showLatest(items) {
  $("#latest-section").hidden = items.length === 0;
  const list = $("#latest");
  list.replaceChildren();
  for (const it of items) {
    list.append(el("li", {},
      el("span", { className: "date", textContent: it.published_at ? it.published_at.slice(0, 10) : "no date" }),
      el("span", {}, el("span", { className: "source", textContent: it.source }),
        el("span", { className: "title", textContent: it.title, title: it.url })),
      el("p", { className: "excerpt muted", textContent: it.excerpt })));
  }
}

const grantHint = "Grant this plugin's permissions on the Permissions tab first.";

async function load() {
  const data = await api("GET", "/overview");
  if (data.needs_grant) say(grantHint);
  showFeeds(data.feeds);
  showLatest(data.latest);
}

async function main() {
  await hatamoto.ready;
  const button = $("#fetch");
  button.onclick = async () => {
    button.disabled = true;
    try {
      say("");
      const r = await api("POST", "/fetch-now");
      if (r.needs_grant) {
        say(grantHint);
        return;
      }
      const waiting = r.results.filter((x) => x.status === "waiting for the next run").length;
      say(`Fetched ${r.results.length - waiting} feed(s)${waiting ? `; ${waiting} wait for the next run` : ""}.`);
      await load();
    } catch (e) {
      say(e.message, true);
    } finally {
      button.disabled = false;
    }
  };
  await load();
}

main().catch((e) => say(e.message, true));
