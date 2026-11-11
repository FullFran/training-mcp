// The three screens of the home: the choice, Entrenar and Planificar. They live
// in one page and switch in place, so the chat, its stream and the rest timer
// are never reloaded. The URL keeps the screen (?modo=), so a reload or the
// back button lands where the person was. In Entrenar the coach opens as an
// overlay over the workout and closes back to the same point.
(function () {
  'use strict';

  const body = document.body;
  const base = body.dataset.base || '';
  const $ = (id) => document.getElementById(id);
  const QUERY = { train: 'entrenar', plan: 'planificar' };
  const MODES = { entrenar: 'train', planificar: 'plan' };

  const fromURL = () => MODES[new URLSearchParams(window.location.search).get('modo')] || 'home';
  const urlFor = (mode) => base + '/' + (QUERY[mode] ? '?modo=' + QUERY[mode] : '');

  const sheet = $('coach-sheet');
  const scrim = $('coach-scrim');
  const more = $('wk-more');
  const finish = () => $('finish');

  function show(mode, coach) {
    body.dataset.mode = mode;
    const overlay = Boolean(coach) && mode === 'train' && Boolean(sheet);
    body.classList.toggle('coach-open', overlay);
    if (scrim) scrim.hidden = !overlay;
    if (mode !== 'train') setFinish(false);
    if (mode === 'plan' || overlay) document.dispatchEvent(new CustomEvent('coach:shown'));
  }

  // go changes screen and leaves a history entry, so back returns here.
  function go(mode) {
    if (mode === body.dataset.mode && !body.classList.contains('coach-open')) return;
    history.pushState({ mode }, '', urlFor(mode));
    show(mode, false);
    window.scrollTo(0, 0);
  }

  // The URL is the truth: a cached page served offline may carry another mode.
  history.replaceState({ mode: fromURL() }, '', window.location.href);
  show(fromURL(), false);

  window.addEventListener('popstate', (event) => {
    const state = event.state || {};
    show(state.mode || fromURL(), state.coach);
  });

  document.addEventListener('click', (event) => {
    const link = event.target.closest('[data-mode-go]');
    if (!link || event.metaKey || event.ctrlKey || event.shiftKey) return;
    event.preventDefault();
    go(link.dataset.modeGo);
  });
  // Another script (choosing a plan from the chat) can send the person here.
  document.addEventListener('mode:go', (event) => go(event.detail));

  // --- coach overlay -------------------------------------------------------
  // Opening pushes an entry, so the phone's back gesture closes it.
  function openCoach(dictate) {
    if (!sheet) return;
    if (!body.classList.contains('coach-open')) {
      history.pushState({ mode: 'train', coach: true }, '', urlFor('train'));
      show('train', true);
    }
    const mic = $('coach-mic');
    const text = $('coach-text');
    if (dictate && mic && !mic.hidden) mic.click();
    else if (text && !$('coach-form').hidden) text.focus();
  }

  function closeCoach() {
    if (!body.classList.contains('coach-open')) return;
    if (history.state && history.state.coach) history.back();
    else show('train', false);
  }

  const launch = $('launch-open');
  if (launch) launch.addEventListener('click', () => openCoach(false));
  const launchMic = $('launch-mic');
  if (launchMic) launchMic.addEventListener('click', () => openCoach(true));
  const close = $('coach-close');
  if (close) close.addEventListener('click', closeCoach);
  if (scrim) scrim.addEventListener('click', closeCoach);

  // While the coach is open the rest timer stays in sight in its header.
  const restBar = $('rest');
  const restTime = $('rest-time');
  const coachRest = $('coach-rest');
  if (restBar && restTime && coachRest && window.MutationObserver) {
    const mirror = () => {
      coachRest.hidden = restBar.hidden;
      coachRest.textContent = '⏱ ' + restTime.textContent;
      coachRest.classList.toggle('done', restBar.classList.contains('done'));
    };
    new MutationObserver(mirror).observe(restBar, { attributes: true, childList: true, subtree: true, characterData: true });
    mirror();
  }

  // --- training screen -----------------------------------------------------
  // Tapping an exercise loads it (app.js) and opens the entry form over the list.
  document.addEventListener('click', (event) => {
    if (body.dataset.mode !== 'train' || !more) return;
    const item = event.target.closest('#train .planned li');
    if (!item || event.target.closest('.more, .item-actions')) return;
    more.open = true;
  });

  // An empty session starts by logging its first exercise.
  document.addEventListener('click', (event) => {
    if (!event.target.closest('#free-session') || !more) return;
    more.open = true;
    const exercise = $('exercise');
    if (exercise) {
      exercise.value = '';
      exercise.dispatchEvent(new Event('input', { bubbles: true }));
      exercise.focus();
    }
  });

  // --- finishing -----------------------------------------------------------
  // The summary is part of the workout panel, so it is always the server's
  // latest; it stays open across a swap (rating a muscle re-renders the panel).
  let finishing = false;
  function setFinish(open) {
    finishing = open;
    const el = finish();
    if (el) el.hidden = !open;
    body.classList.toggle('finish-open', open);
  }
  const finishOpen = $('finish-open');
  if (finishOpen) finishOpen.addEventListener('click', () => setFinish(true));
  document.addEventListener('click', (event) => {
    if (event.target.closest('[data-close-finish]')) setFinish(false);
    if (event.target.closest('#finish-done')) {
      setFinish(false);
      document.dispatchEvent(new CustomEvent('workout:finish'));
      go('home');
    }
  });
  body.addEventListener('htmx:afterSwap', (event) => {
    if (event.target.id === 'panel' && finishing) setFinish(true);
  });

  document.addEventListener('keydown', (event) => {
    if (event.key !== 'Escape') return;
    // shell.js closes the entry sheet, the drawer or a panel first.
    if ((more && more.open) || !$('panel-sheet').hidden) return;
    if (finishing) setFinish(false);
    else closeCoach();
  });
})();
