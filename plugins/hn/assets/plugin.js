(function () {
  'use strict';
  var w = window, d = document;
  if (w.__hnPolish || !d || !d.addEventListener) return;
  w.__hnPolish = 1;

  function has(el, c) { return !!el && (' ' + (el.className || '') + ' ').indexOf(' ' + c + ' ') >= 0; }
  function up(el, c) { while (el && el !== d) { if (has(el, c)) return el; el = el.parentNode; } return null; }
  function drop(el) { if (el && el.parentNode) el.parentNode.removeChild(el); }
  function body(f) {
    var out = [], e = f.elements, i;
    for (i = 0; i < e.length; i++) {
      if (e[i].name && e[i].type !== 'submit') out.push(encodeURIComponent(e[i].name) + '=' + encodeURIComponent(e[i].value || ''));
    }
    return out.join('&');
  }
  function val(f, n) {
    var e = f.elements, i;
    for (i = 0; i < e.length; i++) if (e[i].name === n) return e[i].value;
  }
  function how(f) { var h = val(f, 'how'); return h == null ? 'up' : h; }
  function isUn(f) { return how(f) === 'un' || f.getAttribute('action').indexOf('how=un') > 0; }
  function cls(el, c, on) {
    var s = (' ' + el.className + ' ').replace(' ' + c + ' ', ' ').replace(/^ | $/g, '');
    el.className = on ? s + ' ' + c : s;
  }
  function cellOf(f) { return f.__hnCell || up(f, 'hn-vote-cell') || up(f, 'hn-vote') || f.parentNode; }
  function button(b) { b.className = 'hn-unvote'; b.type = 'submit'; b.title = b.textContent = 'unvote'; b.setAttribute('aria-label', 'unvote'); return b; }
  function storyUn(f, row) {
    var us = row.querySelector('a.hn-user'), age = us && us.nextSibling.nextSibling, u = d.createElement('form'), n = d.createElement('input'), p = age && age.parentNode, r;
    if (!p) return null;
    u.className = 'hn-act hn-un';
    u.method = 'post';
    u.setAttribute('action', f.getAttribute('action') + '?how=un');
    n.type = 'hidden';
    n.name = 'next';
    n.value = val(f, 'next');
    u.appendChild(n);
    u.appendChild(button(d.createElement('button')));
    r = age.nextSibling;
    p.insertBefore(d.createTextNode(' | '), r);
    p.insertBefore(u, r);
    return u;
  }
  function commentUn(f, cell) {
    var u = f.cloneNode(true), e = u.elements, b = u.querySelector('button'), i;
    for (i = 0; i < e.length; i++) if (e[i].name === 'how') e[i].value = 'un';
    u.className = 'hn-un';
    if (b) button(b);
    return cell.appendChild(u);
  }

  function done(f, j) {
    var cell = cellOf(f), row = up(cell, 'hn-story'), a = cell.querySelectorAll('form.hn-vote-form'), un = isUn(f), u, i;
    f.__hnBusy = 0;
    if (un === !!j.voted) return;
    for (i = 0; i < a.length; i++) if (!isUn(a[i])) cls(a[i], 'hn-gone', !un);
    cls(cell, 'hn-voted', !un);
    if (un) return cls(f, 'hn-gone', 1);
    u = cell.__hnUn || (cell.__hnUn = row ? storyUn(f, row) : commentUn(f, cell));
    if (u) { u.__hnCell = cell; cls(u, 'hn-gone', 0); }
  }

  function fallback(f) { try { f.__hnBusy = 0; f.submit(); } catch (e) {} }

  function vote(ev) {
    var f = ev && ev.target, a = f && f.getAttribute && f.getAttribute('action');
    if (!f || !(has(f, 'hn-vote-form') || has(f, 'hn-un')) || (a || '').indexOf('/p/hn/vote/') !== 0) return;
    if (isUn(f) && !cellOf(f).querySelector('form.hn-gone')) return;
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
  function dropForm(f) {
    var t = f.previousSibling;
    if (t && t.nodeType === 3 && /^\s*\|\s*$/.test(t.nodeValue)) drop(t);
    drop(f);
  }

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

  // No <textarea> without site.scripts: inputs are swapped here (reply's name is rotated: copy it).
  function swap(i, rows, cols, max) {
    var t = d.createElement('textarea');
    t.setAttribute('id', i.getAttribute('id') || '');
    t.setAttribute('name', i.getAttribute('name') || 'text');
    t.setAttribute('rows', rows);
    t.setAttribute('cols', cols);
    if (max) t.setAttribute('maxlength', max);
    if (i.getAttribute('required') !== null) t.setAttribute('required', '');
    t.value = i.getAttribute('value') || i.value || '';
    i.parentNode.replaceChild(t, i);
  }

  function textarea() {
    try {
      var i = d.querySelector('input[id="hn-text"]'), r = d.querySelector('input[data-hn-reply]'), a = d.querySelector('input[id="hn-about"]');
      if (i) swap(i, '4', '49');
      if (r) swap(r, '6', '60', '5000');
      if (a) swap(a, '5', '60', '2000');
    } catch (e) {}
  }

  if (w.fetch) d.addEventListener('submit', vote, true);
  userbar();
  textarea();
  if (w.fetch) state();
  if (d.readyState === 'loading') { d.addEventListener('DOMContentLoaded', userbar); d.addEventListener('DOMContentLoaded', textarea); }
})();
