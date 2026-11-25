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
    var a = f && f.getAttribute && f.getAttribute('action');
    if (!f || !has(f, 'hn-vote-form') || (a || '').indexOf('/p/hn/vote/') !== 0) return;
    if (f.__hnBusy) { ev.preventDefault(); return; }
    try {
      ev.preventDefault();
      f.__hnBusy = 1;
      w.fetch(a, {
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

  function within(n, a) { for (; n; n = n.parentNode) if (n === a) return true; return false; }
  // A theme that renders the bar in its header (inside a wrapper) keeps it there.
  function userbar() {
    try {
      var ub = d.querySelector('.hn-ub'), slot = d.querySelector('.hn-userbar');
      if (ub && slot && !within(ub, slot)) slot.appendChild(ub);
    } catch (e) {}
  }

  function idOf(action, kind) {
    var p = '/p/hn/' + kind + '/comment/';
    return (action || '').indexOf(p) === 0 ? action.slice(p.length) : '';
  }
  function how(f) {
    var e = f.elements || [], i;
    for (i = 0; i < e.length; i++) if (e[i].name === 'how') return e[i].value;
    return 'up';
  }
  // The theme prints " | " before the inline forms; drop it with the form.
  function dropForm(f) {
    var t = f.previousSibling;
    if (t && t.nodeType === 3 && /^\s*\|\s*$/.test(t.nodeValue)) drop(t);
    drop(f);
  }

  // Hides the controls the viewer cannot use. {} (signed out) prunes nothing.
  function prune(s) {
    var v = s && s.viewer, cs = (s && s.comments) || {}, cells, forms, i, j, c;
    if (!v) return;
    cells = d.querySelectorAll('[data-hn-comment]');
    for (i = 0; i < cells.length; i++) {
      c = cs[cells[i].getAttribute('data-hn-comment')];
      forms = cells[i].querySelectorAll('form.hn-vote-form');
      for (j = forms.length - 1; j >= 0; j--) {
        if (how(forms[j]) !== 'un' && ((c && (c.voted || c.own || c.dead)) || !v.downvote && how(forms[j]) === 'down')) drop(forms[j]);
      }
      if (c && c.voted && !has(cells[i], 'hn-voted')) cells[i].className += ' hn-voted';
    }
    forms = d.querySelectorAll('form[action^="/p/hn/flag/comment/"]');
    for (i = forms.length - 1; i >= 0; i--) {
      c = cs[idOf(forms[i].getAttribute('action'), 'flag')];
      if (!v.flag || (c && c.own)) dropForm(forms[i]);
    }
    forms = d.querySelectorAll('form[action^="/p/hn/vouch/comment/"]');
    for (i = forms.length - 1; i >= 0; i--) {
      c = cs[idOf(forms[i].getAttribute('action'), 'vouch')];
      if (!v.vouch || (c && !c.dead)) dropForm(forms[i]);
    }
  }

  function state() {
    try {
      var m = d.querySelector('main[data-hm-entity]'), id = m && m.getAttribute('data-hm-entity');
      if (!d.querySelector('[data-hn-comment]') || !/^[0-9a-f]{8}-([0-9a-f]{4}-){3}[0-9a-f]{12}$/i.test(id || '')) return;
      w.fetch('/p/hn/state/' + id, { credentials: 'same-origin', headers: { 'Accept': 'application/json' } })
        .then(function (r) {
          if (!r || !r.ok) throw 0;
          return r.json();
        }).then(prune).then(null, function () {});
    } catch (e) {}
  }

  // The host refuses <textarea> in a section without site.scripts, so the
  // submit and reply texts are one-line inputs; this file loads only with that
  // grant. The reply input's name is rotated by the server: copy it.
  function swap(i, rows, cols, max) {
    var t = d.createElement('textarea');
    t.setAttribute('id', i.getAttribute('id') || '');
    t.setAttribute('name', i.getAttribute('name') || 'text');
    t.setAttribute('rows', rows);
    t.setAttribute('cols', cols);
    if (max) t.setAttribute('maxlength', max);
    if (i.getAttribute('required') !== null) t.setAttribute('required', '');
    t.value = i.value || '';
    i.parentNode.replaceChild(t, i);
  }

  function textarea() {
    try {
      var i = d.querySelector('input[id="hn-text"]'), r = d.querySelector('input[data-hn-reply]');
      if (i) swap(i, '4', '49');
      if (r) swap(r, '6', '60', '5000');
    } catch (e) {}
  }

  if (w.fetch) d.addEventListener('submit', vote, true);
  userbar();
  textarea();
  if (w.fetch) state();
  if (d.readyState === 'loading') { d.addEventListener('DOMContentLoaded', userbar); d.addEventListener('DOMContentLoaded', textarea); }
})();
