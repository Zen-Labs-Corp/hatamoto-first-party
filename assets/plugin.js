// Loads the Google Tag Manager container named by <meta name="gtm-container">.
// Does nothing without a valid ID and never loads the same container twice.
(function () {
  'use strict';
  var meta = document.querySelector('meta[name="gtm-container"]');
  var id = ((meta && meta.getAttribute('content')) || '').replace(/^\s+|\s+$/g, '').toUpperCase();
  if (!/^GTM-[A-Z0-9]{4,12}$/.test(id)) return;

  var w = window;
  if (w.google_tag_manager && w.google_tag_manager[id]) return;
  if (document.querySelector('script[src*="googletagmanager.com/gtm.js?id=' + id + '"]')) return;

  (w.dataLayer = w.dataLayer || []).push({ 'gtm.start': new Date().getTime(), event: 'gtm.js' });
  var s = document.createElement('script');
  s.async = true;
  s.src = 'https://www.googletagmanager.com/gtm.js?id=' + encodeURIComponent(id);
  var first = document.getElementsByTagName('script')[0];
  if (first && first.parentNode) first.parentNode.insertBefore(s, first);
  else document.head.appendChild(s);
})();
