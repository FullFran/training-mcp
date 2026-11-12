// Progreso and Tú. Progreso loads its body from /progress the first time its
// tab is shown, and again after a set is logged or a panel changed something,
// so the home page never pays for the history it does not show. Tú's rest
// stepper writes the shared rest target the workout's timer starts from.
(function () {
  'use strict';

  const body = document.body;
  const base = body.dataset.base || '';
  const $ = (id) => document.getElementById(id);
  const csrfMeta = document.querySelector('meta[name="csrf-token"]');
  const csrf = csrfMeta ? csrfMeta.content : '';

  // --- Progreso: load on demand ----------------------------------------------
  const holder = $('progress-body');
  let pane = 'history';

  function load() {
    if (!holder || !window.htmx) return;
    holder.dataset.ready = '';
    window.htmx.trigger(holder, 'progress:load');
  }
  function maybeLoad() {
    if (body.dataset.mode === 'progress' && holder && !('ready' in holder.dataset)) load();
  }
  // modes.js owns the screen; watching its attribute keeps the two apart.
  new MutationObserver(maybeLoad).observe(body, { attributes: true, attributeFilter: ['data-mode'] });
  // htmx is deferred like this script; it is ready once the page is.
  if (document.readyState === 'complete') maybeLoad();
  else window.addEventListener('load', maybeLoad);

  // New sets or an edit in a panel make the history stale.
  function stale() {
    if (!holder) return;
    delete holder.dataset.ready;
    maybeLoad();
  }
  document.addEventListener('workout:set-logged', stale);
  document.addEventListener('progress:stale', stale);

  // --- Progreso: Historial · Ejercicios · Cuerpo ---------------------------
  function select(name) {
    if (!holder) return;
    const tab = holder.querySelector('[data-pg="' + name + '"]');
    if (!tab) return;
    pane = name;
    holder.querySelectorAll('[data-pg]').forEach((b) => {
      const on = b === tab;
      b.setAttribute('aria-selected', on ? 'true' : 'false');
      const panel = $(b.getAttribute('aria-controls'));
      if (panel) panel.hidden = !on;
    });
  }
  document.addEventListener('click', (event) => {
    const tab = event.target.closest('#progress-body [data-pg]');
    if (tab) select(tab.dataset.pg);
  });
  // A reload keeps the pane the person was reading.
  document.body.addEventListener('htmx:afterSwap', (event) => {
    if (event.target === holder) select(pane);
  });

  // --- drawer links that switch screen close the drawer ----------------------
  document.addEventListener('click', (event) => {
    if (event.target.closest('#drawer [data-mode-go]')) document.dispatchEvent(new CustomEvent('drawer:close'));
  });

  // --- Tú: rest target --------------------------------------------------------
  const MIN = 30;
  const MAX = 900;
  const value = document.querySelector('[data-rest-value]');
  let rest = parseInt(body.dataset.restTarget || '', 10) || 120;
  const clock = (s) => Math.floor(s / 60) + ':' + String(s % 60).padStart(2, '0');

  function paintRest(seconds) {
    rest = seconds;
    if (value) value.textContent = clock(seconds);
    body.dataset.restTarget = String(seconds);
  }
  document.addEventListener('rest:target', (event) => paintRest(event.detail));
  document.addEventListener('click', (event) => {
    const step = event.target.closest('[data-rest-step]');
    if (!step) return;
    const next = Math.min(MAX, Math.max(MIN, rest + parseInt(step.dataset.restStep, 10)));
    if (next === rest) return;
    document.dispatchEvent(new CustomEvent('rest:target', { detail: next }));
    fetch(base + '/rest', {
      method: 'POST',
      credentials: 'same-origin',
      headers: Object.assign({ 'Content-Type': 'application/x-www-form-urlencoded' }, csrf ? { 'X-CSRF-Token': csrf } : {}),
      body: 'seconds=' + next
    }).catch(() => {});
    if (navigator.vibrate) navigator.vibrate(8);
  });

  // --- Tú: the account lives in Planificar's menu ----------------------------
  document.addEventListener('click', (event) => {
    const link = event.target.closest('[data-open-drawer]');
    const menu = $('menu-open');
    if (!link || !menu || event.metaKey || event.ctrlKey) return;
    event.preventDefault();
    const plan = document.querySelector('#tabs [data-tab="plan"]');
    if (plan) plan.click();
    menu.click();
  });

  // --- Tú: the coach's model -------------------------------------------------
  // The chat's own model button opens the sheet; Tú mirrors its label.
  const modelOpen = $('model-open');
  const modelName = $('model-name');
  const mirror = document.querySelector('[data-model-name]');
  document.addEventListener('click', (event) => {
    if (event.target.closest('[data-open-model]') && modelOpen) modelOpen.click();
  });
  if (modelName && mirror) {
    const copy = () => { if (modelName.textContent.trim()) mirror.textContent = modelName.textContent.trim(); };
    new MutationObserver(copy).observe(modelName, { childList: true, characterData: true, subtree: true });
  }
})();
