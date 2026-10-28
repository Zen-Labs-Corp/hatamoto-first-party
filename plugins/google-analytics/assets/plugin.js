// Loads Google Analytics (gtag.js) for the ID in <meta name="google-analytics-id">.
// Does nothing without a valid ID and never loads the same ID twice. Opening a
// page with #ga-exclude leaves this browser out; #ga-include counts it again.
(function () {
  'use strict';
  var meta = document.querySelector('meta[name="google-analytics-id"]');
  var id = ((meta && meta.getAttribute('content')) || '').replace(/^\s+|\s+$/g, '').toUpperCase();
  if (!/^(G|GT)-[A-Z0-9]{4,16}$/.test(id)) return;

  var w = window;
  var KEY = 'hatamoto-ga-exclude';

  function store() {
    try { return w.localStorage; } catch (e) { return null; }
  }
  function setFlag(on) {
    try {
      var s = store();
      if (!s) return;
      if (on) s.setItem(KEY, '1'); else s.removeItem(KEY);
    } catch (e) { /* storage blocked: carry on */ }
  }
  function flagged() {
    try {
      var s = store();
      return !!s && s.getItem(KEY) === '1';
    } catch (e) { return false; }
  }
  function clearFragment() {
    try {
      w.history.replaceState(null, '', w.location.pathname + w.location.search);
    } catch (e) { /* keep the fragment */ }
  }
  function disable() {
    w['ga-disable-' + id] = true;
  }

  function button(text, label, onClick) {
    var b = document.createElement('button');
    b.type = 'button';
    b.className = 'hm-ga-notice-btn';
    b.textContent = text;
    if (label) b.title = label;
    b.addEventListener('click', onClick);
    return b;
  }

  function notice() {
    var bar = document.createElement('div');
    bar.className = 'hm-ga-notice';
    var msg = document.createElement('span');
    msg.textContent = 'Google Analytics is off in this browser.';
    bar.appendChild(msg);
    bar.appendChild(button('Count it again', '', function () {
      setFlag(false);
      w.location.reload();
    }));
    bar.appendChild(button('\u00d7', 'Close', function () {
      if (bar.parentNode) bar.parentNode.removeChild(bar);
    }));
    document.body.appendChild(bar);
  }

  var hash = w.location && w.location.hash;
  if (hash === '#ga-exclude') {
    setFlag(true);
    disable();
    clearFragment();
    notice();
    return;
  }
  if (hash === '#ga-include') {
    setFlag(false);
    // replace() drops the fragment even when history.replaceState is blocked,
    // so the reload cannot land back on #ga-include.
    w.location.replace(w.location.pathname + w.location.search);
    return;
  }
  if (flagged()) {
    disable();
    return;
  }

  if (document.querySelector('script[src*="googletagmanager.com/gtag/js?id=' + id + '"]')) return;

  w.dataLayer = w.dataLayer || [];
  if (!w.gtag) {
    w.gtag = function () { w.dataLayer.push(arguments); };
  }
  w.gtag('js', new Date());
  w.gtag('config', id);

  var s = document.createElement('script');
  s.async = true;
  s.src = 'https://www.googletagmanager.com/gtag/js?id=' + encodeURIComponent(id);
  var first = document.getElementsByTagName('script')[0];
  if (first && first.parentNode) first.parentNode.insertBefore(s, first);
  else document.head.appendChild(s);
})();
