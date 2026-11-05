// Keeps the ad click ID of a visitor who consented to marketing and posts it,
// with the submission id, to this plugin when a form is stored. Until
// consent it reads no cookie, reads no address and sets nothing.
(function () {
  'use strict';

  var KEYS = ['gclid', 'gbraid', 'wbraid', 'ttclid', 'li_fat_id'];
  var ID_RE = /^[A-Za-z0-9._~-]{1,256}$/;
  var UUID_RE = /^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$/;
  var SLUG_RE = /^[A-Za-z0-9_-]{1,64}$/;
  var COOKIE = 'hm_adc';
  var MAX_AGE = 90 * 24 * 60 * 60;
  var CLICK_URL = '/p/ads-conversions/click';
  var started = false;

  // No consent banner means hm.consent is undefined: everything is allowed.
  function allowed() {
    var c = window.hm && window.hm.consent;
    if (!c) return true;
    return typeof c.allows === 'function' && c.allows('marketing') === true;
  }

  function decode(s) {
    try { return decodeURIComponent(s.replace(/\+/g, ' ')); } catch (e) { return ''; }
  }

  function readCookie(name) {
    var parts = String(document.cookie || '').split(';');
    for (var i = 0; i < parts.length; i++) {
      var p = parts[i].replace(/^\s+/, '');
      if (p.indexOf(name + '=') === 0) return decode(p.slice(name.length + 1));
    }
    return '';
  }

  // add copies the valid IDs of src over dst; unknown keys and bad values are dropped.
  function add(dst, src) {
    for (var i = 0; i < KEYS.length; i++) {
      var v = src && src[KEYS[i]];
      if (typeof v === 'string' && ID_RE.test(v)) dst[KEYS[i]] = v;
    }
  }

  function stored() {
    var ids = {};
    try { add(ids, JSON.parse(readCookie(COOKIE))); } catch (e) { /* no or broken cookie */ }
    return ids;
  }

  function fromAddress() {
    var out = {};
    var q = String(window.location.search || '').replace(/^\?/, '');
    var pairs = q ? q.split('&') : [];
    for (var i = 0; i < pairs.length; i++) {
      var at = pairs[i].indexOf('=');
      if (at > 0) out[decode(pairs[i].slice(0, at))] = decode(pairs[i].slice(at + 1));
    }
    return out;
  }

  function same(a, b) {
    for (var i = 0; i < KEYS.length; i++) if (a[KEYS[i]] !== b[KEYS[i]]) return false;
    return true;
  }

  function writeCookie(ids) {
    var secure = window.location.protocol === 'https:' ? '; Secure' : '';
    document.cookie = COOKIE + '=' + encodeURIComponent(JSON.stringify(ids)) + '; path=/; max-age=' + MAX_AGE + '; SameSite=Lax' + secure;
  }

  // collect merges what is in the cookie, LinkedIn's own cookie and the
  // address (newest wins) back into hm_adc.
  function collect() {
    var ids = stored();
    var before = {};
    add(before, ids);
    var li = {};
    li.li_fat_id = readCookie('li_fat_id');
    add(ids, li);
    add(ids, fromAddress());
    if (Object.keys(ids).length && !same(ids, before)) writeCookie(ids);
  }

  function onConversion(e) {
    if (!allowed() || typeof window.fetch !== 'function') return;
    var d = (e && e.detail) || {};
    var id = typeof d.id === 'string' ? d.id : '';
    if (!UUID_RE.test(id)) return;
    var ids = stored();
    if (!Object.keys(ids).length) return;
    var body = { submission_id: id, ids: ids };
    if (typeof d.form === 'string' && SLUG_RE.test(d.form)) body.form = d.form;
    var p = window.fetch(CLICK_URL, {
      method: 'POST',
      keepalive: true,
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(body),
    });
    if (p && typeof p.catch === 'function') p.catch(function () {});
  }

  function start() {
    if (started) return;
    started = true;
    collect();
    document.addEventListener('hm:conversion', onConversion);
  }

  if (allowed()) {
    start();
  } else {
    document.addEventListener('hm:consent', function () {
      if (allowed()) start();
    });
  }
})();
