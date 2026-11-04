// Shows the announcement bars and popups published by the render.head filter in
// <meta name="hatamoto-popups">. The head is cached, so everything is checked
// again here: dates (UTC), paths, frequency. Text is set with textContent and
// listeners with addEventListener only; nothing in here can throw out of the
// script.
(function () {
  'use strict';

  var PREFIX = 'hm-popups:';
  var DAY = 86400000;

  // matchPath: include (none = every page) and not exclude. Globs: * stays in
  // one segment, ** crosses segments, a trailing /** also matches the folder.
  function globToRegExp(glob) {
    var out = '';
    for (var i = 0; i < glob.length; i++) {
      var rest = glob.slice(i);
      if (rest === '/**') {
        out += '(?:/.*)?';
        break;
      } else if (rest.slice(0, 2) === '**') {
        out += '.*';
        i++;
      } else if (glob.charAt(i) === '*') {
        out += '[^/]*';
      } else {
        out += glob.charAt(i).replace(/[.+?^${}()|[\]\\\/]/g, '\\$&');
      }
    }
    return new RegExp('^' + out + '$');
  }
  function anyGlob(globs, path) {
    for (var i = 0; i < (globs || []).length; i++) {
      var g = String(globs[i]).replace(/^\s+|\s+$/g, '');
      if (g && globToRegExp(g).test(path)) return true;
    }
    return false;
  }
  function matchPath(path, include, exclude) {
    if (include && include.length && !anyGlob(include, path)) return false;
    return !anyGlob(exclude, path);
  }

  // parseUTC: ms since epoch; null for empty (open); NaN for an invalid value.
  // A date alone is 00:00:00Z for a start and 23:59:59Z for an end.
  function parseUTC(str, isEnd) {
    str = String(str == null ? '' : str).replace(/^\s+|\s+$/g, '');
    if (!str) return null;
    var m = /^(\d{4})-(\d{2})-(\d{2})(?:[T ](\d{2}):(\d{2}))?$/.exec(str);
    if (!m) return NaN;
    var y = +m[1], mo = +m[2], d = +m[3];
    var h = m[4] === undefined ? (isEnd ? 23 : 0) : +m[4];
    var mi = m[5] === undefined ? (isEnd ? 59 : 0) : +m[5];
    var s = m[4] === undefined && isEnd ? 59 : 0;
    if (mo < 1 || mo > 12 || d < 1 || h > 23 || mi > 59) return NaN;
    var t = Date.UTC(y, mo - 1, d, h, mi, s);
    if (new Date(t).getUTCDate() !== d) return NaN;
    return t;
  }

  function inWindow(c, now) {
    var start = parseUTC(c.start, false);
    var end = parseUTC(c.end, true);
    if (start !== start || end !== end) return false;
    if (start !== null && now < start) return false;
    if (end !== null && now > end) return false;
    return true;
  }

  // safeLink: http, https or a site path ("/x", not "//host").
  function safeLink(url) {
    url = String(url == null ? '' : url).replace(/^\s+|\s+$/g, '');
    if (!url) return false;
    if (url.charAt(0) === '/') return url.charAt(1) !== '/' && url.charAt(1) !== '\\';
    return /^https?:\/\//i.test(url);
  }

  // Storage. A blocked or throwing store reads as "never dismissed".
  function readStore(area, key) {
    try { return area ? area.getItem(key) : null; } catch (e) { return null; }
  }
  function writeStore(area, key, value) {
    try { if (area) area.setItem(key, value); } catch (e) { /* blocked: show again next view */ }
  }

  // stores: { session, local }, either may be null.
  function shouldShow(c, stores, now) {
    var key = PREFIX + c.id;
    var f = c.frequency;
    if (f === 'once') return readStore(stores.local, key) === null;
    if (f === 'day' || f === 'week') {
      var t = parseInt(readStore(stores.local, key), 10);
      if (t !== t) return true;
      return now - t >= (f === 'day' ? DAY : 7 * DAY);
    }
    return readStore(stores.session, key) === null;
  }
  function recordDismissal(c, stores, now) {
    var area = c.frequency === 'session' || !c.frequency ? stores.session : stores.local;
    writeStore(area, PREFIX + c.id, String(now));
  }

  function getStores(win) {
    var s = { session: null, local: null };
    try { s.session = win.sessionStorage; } catch (e) { /* blocked */ }
    try { s.local = win.localStorage; } catch (e) { /* blocked */ }
    return s;
  }

  function readCampaigns(doc) {
    var meta = doc.querySelector('meta[name="hatamoto-popups"]');
    var raw = meta && meta.getAttribute('content');
    if (!raw) return [];
    try {
      var list = JSON.parse(raw);
      return Array.isArray(list) ? list : [];
    } catch (e) { return []; }
  }

  // pick: matching campaigns in settings order, at most one bar per position
  // and one popup.
  function pick(list, path, stores, now) {
    var bars = {}, popup = null, out = [];
    for (var i = 0; i < list.length; i++) {
      var c = list[i];
      if (!c || typeof c.id !== 'string' || typeof c.message !== 'string' || !c.message) continue;
      if (c.kind !== 'bar' && c.kind !== 'popup') continue;
      if (!matchPath(path, c.include_paths, c.exclude_paths)) continue;
      if (!inWindow(c, now) || !shouldShow(c, stores, now)) continue;
      if (c.kind === 'bar') {
        var pos = c.position === 'bottom' ? 'bottom' : 'top';
        if (bars[pos]) continue;
        bars[pos] = true;
      } else {
        if (popup) continue;
        popup = true;
      }
      out.push(c);
    }
    return out;
  }

  function closeButton(doc) {
    var b = doc.createElement('button');
    b.setAttribute('type', 'button');
    b.setAttribute('aria-label', 'Close');
    b.className = 'hm-popups-close';
    b.textContent = '\u00d7';
    return b;
  }
  function content(doc, c) {
    var wrap = doc.createElement('div');
    wrap.className = 'hm-popups-body';
    var msg = doc.createElement('span');
    msg.className = 'hm-popups-message';
    msg.textContent = c.message;
    wrap.appendChild(msg);
    if (c.link_label && safeLink(c.link_url)) {
      var a = doc.createElement('a');
      a.className = 'hm-popups-link';
      a.setAttribute('href', c.link_url);
      a.textContent = c.link_label;
      wrap.appendChild(a);
    }
    return wrap;
  }

  function showBar(doc, c, onDismiss) {
    var bar = doc.createElement('div');
    bar.setAttribute('role', 'region');
    bar.setAttribute('aria-label', 'Announcement');
    bar.className = 'hm-popups-bar hm-popups-' + (c.position === 'bottom' ? 'bottom' : 'top');
    bar.appendChild(content(doc, c));
    var close = closeButton(doc);
    close.addEventListener('click', function () {
      if (bar.parentNode) bar.parentNode.removeChild(bar);
      onDismiss(c);
    });
    bar.appendChild(close);
    doc.body.appendChild(bar);
    return bar;
  }

  function showPopup(doc, c, onDismiss) {
    if (typeof doc.createElement('dialog').showModal !== 'function') return null;
    var previous = doc.activeElement;
    var dlg = doc.createElement('dialog');
    dlg.className = 'hm-popups-dialog';
    dlg.setAttribute('aria-label', 'Announcement');
    var close = closeButton(doc);
    close.addEventListener('click', function () { dlg.close(); });
    dlg.appendChild(close);
    dlg.appendChild(content(doc, c));
    // close fires for the button, for Esc and for dlg.close() alike.
    dlg.addEventListener('close', function () {
      if (dlg.parentNode) dlg.parentNode.removeChild(dlg);
      onDismiss(c);
      try { if (previous && typeof previous.focus === 'function') previous.focus(); } catch (e) { /* gone */ }
    });
    doc.body.appendChild(dlg);
    dlg.showModal();
    return dlg;
  }

  // arm runs show once when the popup's trigger fires.
  function arm(win, doc, c, show) {
    var done = false;
    function fire() {
      if (done) return;
      done = true;
      show();
    }
    if (c.trigger === 'delay') {
      var sec = Math.max(0, Math.min(600, parseInt(c.delay_seconds, 10) || 0));
      win.setTimeout(fire, sec * 1000);
    } else if (c.trigger === 'scroll') {
      var pct = Math.max(1, Math.min(100, parseInt(c.scroll_percent, 10) || 1));
      var check = function () {
        var h = doc.documentElement.scrollHeight || 1;
        var seen = (win.pageYOffset || 0) + (win.innerHeight || 0);
        if (seen / h * 100 >= pct) fire();
      };
      win.addEventListener('scroll', check, { passive: true });
      check();
    } else if (c.trigger === 'exit') {
      var hover = typeof win.matchMedia === 'function' && win.matchMedia('(hover: hover)').matches;
      if (!hover) return;
      doc.addEventListener('mouseout', function (e) {
        if (e.clientY <= 0 && !e.relatedTarget) fire();
      });
    } else {
      fire();
    }
  }

  function run(win, doc) {
    try {
      var now = Date.now();
      var stores = getStores(win);
      var chosen = pick(readCampaigns(doc), win.location.pathname, stores, now);
      var dismiss = function (c) { recordDismissal(c, stores, Date.now()); };
      chosen.forEach(function (c) {
        try {
          if (c.kind === 'bar') showBar(doc, c, dismiss);
          else arm(win, doc, c, function () { try { showPopup(doc, c, dismiss); } catch (e) { /* no popup */ } });
        } catch (e) { /* skip this campaign */ }
      });
    } catch (e) { /* never break the page */ }
  }

  var api = {
    globToRegExp: globToRegExp, matchPath: matchPath, parseUTC: parseUTC,
    inWindow: inWindow, safeLink: safeLink, readStore: readStore,
    writeStore: writeStore, shouldShow: shouldShow, recordDismissal: recordDismissal,
    pick: pick, run: run
  };
  if (typeof module !== 'undefined' && module.exports) module.exports = api;
  if (typeof document !== 'undefined' && typeof window !== 'undefined') run(window, document);
})();
