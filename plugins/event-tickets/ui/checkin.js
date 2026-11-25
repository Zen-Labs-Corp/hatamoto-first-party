"use strict";
// Check-in helpers for the admin page: scan parsing and the offline queue.
// No DOM and no timers of its own: the page injects storage, the network call
// and the scheduler.

const QUEUE_KEY = "event-tickets:checkin-queue";
const SCAN_MAX = 512;
const RETRY_MS = 15000;
// Device and server clocks differ a little: a check-in this much before the
// device's scan time still counts as that scan.
const SKEW_MS = 120000;

// parseScan returns the ticket token of a scanned text: the part after the
// last "/ticket/" without query or fragment, or the trimmed text itself.
// It returns "" for text longer than the server accepts.
function parseScan(text) {
  let s = String(text).trim();
  if (s.length > SCAN_MAX) return "";
  const at = s.lastIndexOf("/ticket/");
  if (at >= 0) s = s.slice(at + "/ticket/".length);
  s = s.split("?")[0].split("#")[0];
  return s.trim();
}

// openStore keeps the queue in the storage getStorage returns. The admin page
// runs in a sandboxed frame where storage throws, so then the queue lives in
// memory only (persistent is false).
function openStore(getStorage) {
  try {
    const s = getStorage();
    s.setItem(QUEUE_KEY + ":probe", "1");
    s.removeItem(QUEUE_KEY + ":probe");
    return {
      persistent: true,
      load: () => {
        try {
          const q = JSON.parse(s.getItem(QUEUE_KEY) || "[]");
          return Array.isArray(q) ? q : [];
        } catch (_) {
          return [];
        }
      },
      save: (q) => {
        try {
          s.setItem(QUEUE_KEY, JSON.stringify(q));
        } catch (_) {
          // Full or refused: the in-page copy still works.
        }
      },
    };
  } catch (_) {
    let mem = [];
    return { persistent: false, load: () => mem, save: (q) => { mem = q; } };
  }
}

// makeQueue replays queued scans one at a time, oldest first.
//   send(event, body) -> Promise<{status, body}>   (body is the JSON text)
//   onResult(item, outcome)  outcome is the route's answer ({result: ok|already|refused, ...})
//                            or {result: "error"} for a request the server refused outright
//   onChange()               the queue changed
//   onStall(status)          optional: a run stopped on an HTTP status (not a rejected call)
//   schedule(fn, ms)         runs fn later
// A 200, 400 or 413 is final and the item is dropped. Anything else (403/404
// from the host while the plugin is off or reloading, 429, 5xx, status 0, a
// rejected call) keeps the item and stops the run.
// An item sent before may have reached the server with its reply lost: an
// "already" whose check-in is not older than the scan is then this scan's own.
function makeQueue({ send, store, onResult, onChange, onStall, schedule }) {
  let items = store.load();
  let running = false;
  let retryPending = false;

  const changed = () => {
    store.save(items);
    if (onChange) onChange();
  };

  function keyOf(event, body) {
    return event + "|" + (body.scan !== undefined ? "s:" + body.scan : "a:" + body.attendee_id);
  }

  function add(event, body) {
    const key = keyOf(event, body);
    if (items.some((it) => it.key === key)) return false;
    items = items.concat([{ key, event, body, at: Date.now() }]);
    changed();
    return true;
  }

  function remove(key) {
    const n = items.length;
    items = items.filter((it) => it.key !== key);
    if (items.length !== n) changed();
  }

  function retryLater() {
    if (retryPending) return;
    retryPending = true;
    schedule(() => {
      retryPending = false;
      return sync();
    }, RETRY_MS);
  }

  async function sync() {
    if (running) return;
    running = true;
    try {
      while (items.length > 0) {
        const item = items[0];
        let res;
        item.attempts = (item.attempts || 0) + 1;
        store.save(items);
        try {
          res = await send(item.event, item.body);
        } catch (_) {
          retryLater();
          return;
        }
        const status = res && res.status;
        let outcome = null;
        if (status === 200) {
          try {
            outcome = JSON.parse(res.body);
          } catch (_) {
            outcome = null;
          }
          if (!outcome || typeof outcome.result !== "string") {
            retryLater();
            return;
          }
          if (outcome.result === "already" && item.attempts > 1 && outcome.attendee) {
            const when = Date.parse(outcome.attendee.checked_in_at);
            if (when >= item.at - SKEW_MS) outcome = { ...outcome, result: "ok", confirmed: true };
          }
        } else if (status === 400 || status === 413) {
          outcome = { result: "error" };
        } else {
          if (onStall && status) onStall(status);
          retryLater();
          return;
        }
        items = items.filter((it) => it.key !== item.key);
        changed();
        if (onResult) onResult(item, outcome);
      }
    } finally {
      running = false;
    }
  }

  return {
    add,
    remove,
    sync,
    size: () => items.length,
    items: () => items.slice(),
    persistent: store.persistent,
  };
}

if (typeof module !== "undefined") module.exports = { parseScan, openStore, makeQueue, QUEUE_KEY };
