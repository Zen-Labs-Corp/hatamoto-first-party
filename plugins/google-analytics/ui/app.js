// Google Analytics admin page. It runs in a sandboxed iframe and reaches the
// plugin only through the hatamoto bridge; all text is set with textContent.
// The addresses below are shown as text: opening one inside this frame would
// navigate it and cut the bridge.
const $ = (sel) => document.querySelector(sel);
const ID_RE = /^(G|GT)-[A-Z0-9]{4,16}$/;

function showStatus(text, warn) {
  const n = $("#status");
  n.textContent = text;
  n.classList.toggle("warn", Boolean(warn));
}

async function loadStatus() {
  let granted = [];
  try {
    const p = await hatamoto.host("permissions", {});
    const result = p && p.ok !== false && (p.result || p);
    granted = (result && result.granted) || [];
  } catch (e) {
    granted = [];
  }
  let r;
  try {
    r = await hatamoto.host("settings.get", {});
  } catch (e) {
    r = { ok: false };
  }
  if (!r || !r.ok) {
    showStatus("The settings permission is not granted: grant settings under Plugins > Permissions.", true);
    return;
  }
  const raw = (r.result && r.result.measurement_id) || "";
  const id = String(raw).trim().toUpperCase();
  if (!ID_RE.test(id)) {
    showStatus("Tag is OFF: no valid measurement ID set.", false);
    return;
  }
  if (granted.indexOf("site.scripts") < 0) {
    showStatus("The site.scripts permission is not granted, so the tag cannot load: grant site.scripts under Plugins > Permissions.", true);
    return;
  }
  showStatus("Tag is ON for " + id + " on every public page.", false);
}

function copyOf(inputSel, buttonSel) {
  $(buttonSel).addEventListener("click", async () => {
    const input = $(inputSel);
    const note = $("#copy-note");
    input.focus();
    input.select();
    let done = false;
    try {
      await navigator.clipboard.writeText(input.value);
      done = true;
    } catch (e) {
      try { done = document.execCommand("copy"); } catch (e2) { done = false; }
    }
    note.textContent = done ? "Copied." : "Press Ctrl/Cmd+C to copy the selected address.";
    note.hidden = false;
  });
}

for (const sel of ["#exclude", "#include"]) {
  $(sel).addEventListener("focus", (e) => e.target.select());
  $(sel).addEventListener("click", (e) => e.target.select());
}
copyOf("#exclude", "#copy-exclude");
copyOf("#include", "#copy-include");
loadStatus();
