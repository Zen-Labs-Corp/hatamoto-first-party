(function () {
  'use strict';
  var w = window, d = document;
  if (w.__hnPolish || !d || !d.addEventListener) return;
  w.__hnPolish = 1;

  function has(el, c) { return !!el && (' ' + (el.className || '') + ' ').indexOf(' ' + c + ' ') >= 0; }
  function up(el, c) { while (el && el !== d) { if (has(el, c)) return el; el = el.parentNode; } return null; }
  function drop(el) { if (el && el.parentNode) el.parentNode.removeChild(el); }
  function body(f) {
    var out = [], e = f.elements || [], i;
    for (i = 0; i < e.length; i++) {
      if (e[i].name && e[i].type !== 'submit') out.push(encodeURIComponent(e[i].name) + '=' + encodeURIComponent(e[i].value || ''));
    }
    return out.join('&');
  }
  function label(n) { return n === 1 ? '1 point' : n + ' points'; }

  function done(f, j) {
    var cell = up(f, 'hn-vote-cell') || up(f, 'hn-vote') || f.parentNode, row = up(f, 'hn-story'), s, all, i;
    if (row && typeof j.points === 'number') {
      s = row.querySelector('.hn-score');
      if (s) s.textContent = label(j.points);
    }
    all = cell && cell.querySelectorAll ? cell.querySelectorAll('form.hn-vote-form') : [f];
    for (i = all.length - 1; i >= 0; i--) drop(all[i]);
    if (cell && j.voted && !has(cell, 'hn-voted')) cell.className += ' hn-voted';
  }

  // A JS submit() does not fire the submit event, so the fallback cannot loop.
  function fallback(f) { try { f.__hnBusy = 0; f.submit(); } catch (e) {} }

  function vote(ev) {
    var f = ev && ev.target;
    if (!f || !has(f, 'hn-vote-form') || f.__hnBusy) return;
    try {
      ev.preventDefault();
      f.__hnBusy = 1;
      w.fetch(f.getAttribute('action'), {
        method: 'POST',
        credentials: 'same-origin',
        headers: { 'Accept': 'application/json', 'Content-Type': 'application/x-www-form-urlencoded' },
        body: body(f)
      }).then(function (r) {
        if (!r || !r.ok) throw 0;
        return r.json();
      }).then(function (j) {
        if (!j || typeof j !== 'object' || 'error' in j || typeof j.voted !== 'boolean') throw 0;
        done(f, j);
      }).then(null, function () { fallback(f); });
    } catch (e) { fallback(f); }
  }

  function userbar() {
    try {
      var ub = d.querySelector('.hn-ub'), slot = d.querySelector('.hn-userbar');
      if (ub && slot && ub.parentNode !== slot) slot.appendChild(ub);
    } catch (e) {}
  }

  if (w.fetch) d.addEventListener('submit', vote, true);
  userbar();
  if (d.readyState === 'loading') d.addEventListener('DOMContentLoaded', userbar);
})();
