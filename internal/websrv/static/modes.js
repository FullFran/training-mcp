// The app's screens: four tabs (Entrenar, Planificar, Progreso, Tú) and the
// active workout, a full-screen layer over them. They live in one page and
// switch in place, so the chat, its stream and the rest timer are never
// reloaded. The URL keeps the screen (?modo=), so a reload or the back button
// lands where the person was. In the workout the coach opens as an overlay
// and closes back to the same point; collapsed, the workout is a pill.
(function () {
  'use strict';

  const body = document.body;
  const base = body.dataset.base || '';
  const $ = (id) => document.getElementById(id);
  const QUERY = { train: 'entrenar', plan: 'planificar', progress: 'progreso', you: 'tu' };
  const MODES = { entrenar: 'train', planificar: 'plan', progreso: 'progress', tu: 'you' };

  const fromURL = () => MODES[new URLSearchParams(window.location.search).get('modo')] || 'home';
  const urlFor = (mode) => base + '/' + (QUERY[mode] ? '?modo=' + QUERY[mode] : '');

  const sheet = $('coach-sheet');
  const scrim = $('coach-scrim');
  const more = $('wk-more');
  const finish = () => $('finish');
  let finishing = false;

  function show(mode, coach) {
    body.dataset.mode = mode;
    const overlay = Boolean(coach) && mode === 'train' && Boolean(sheet);
    body.classList.toggle('coach-open', overlay);
    if (scrim) scrim.hidden = !overlay;
    if (mode !== 'train') setFinish(false);
    // The workout belongs to Entrenar's tab.
    const tab = mode === 'train' ? 'home' : mode;
    document.querySelectorAll('#tabs [data-tab]').forEach((a) => {
      if (a.dataset.tab === tab) a.setAttribute('aria-current', 'page');
      else a.removeAttribute('aria-current');
    });
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
    if (link.closest('#finish')) setFinish(false);
    go(link.dataset.modeGo);
    // data-new-chat starts a new conversation first (the finish summary's
    // "Comentar con el coach"); coach.js empties the chat.
    if (link.hasAttribute('data-new-chat')) document.dispatchEvent(new CustomEvent('coach:new'));
    // A link that asks the coach something lands in the chat with it typed.
    if (link.hasAttribute('data-ask')) ask(link.dataset.ask);
  });

  function ask(words) {
    const text = $('coach-text');
    if (!text || $('coach-form').hidden) return;
    if (words) {
      text.value = words;
      text.dispatchEvent(new Event('input', { bubbles: true }));
    }
    text.focus();
  }
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
  // Aplicar closes the sheet so the filled row is in sight.
  document.addEventListener('coach:close', closeCoach);

  // While the coach is open the rest timer stays in sight in its header.
  const restBar = $('rest');
  const restTime = $('rest-time');
  const coachRest = $('coach-rest');
  if (restBar && restTime && coachRest && window.MutationObserver) {
    const time = coachRest.querySelector('[data-rest-mirror]');
    const state = coachRest.querySelector('[data-rest-state]');
    const mirror = () => {
      const done = restBar.classList.contains('done');
      coachRest.hidden = restBar.hidden;
      if (time) time.textContent = restTime.textContent;
      if (state) state.textContent = done ? 'a por la siguiente' : 'sigue contando';
      coachRest.classList.toggle('done', done);
    };
    new MutationObserver(mirror).observe(restBar, { attributes: true, childList: true, subtree: true, characterData: true });
    mirror();
  }

  // --- training screen -----------------------------------------------------
  // After a set, the card of what comes next opens (the same one, the next of a
  // superset, or the next exercise once this one is done).
  document.addEventListener('workout:focus', (event) => {
    const name = event.detail;
    if (!name) return;
    const card = Array.from(document.querySelectorAll('#train .planned > li'))
      .find((li) => li.dataset.exercise === name);
    const details = card && card.querySelector('details');
    if (!details) return;
    details.open = true;
    if (body.dataset.mode === 'train') card.scrollIntoView({ block: 'nearest', behavior: 'smooth' });
  });

  // The chips across the top open their exercise's card.
  document.addEventListener('click', (event) => {
    const chip = event.target.closest('.ex-chip');
    if (chip) document.dispatchEvent(new CustomEvent('workout:focus', { detail: chip.dataset.chip }));
    const next = event.target.closest('.next-ex');
    if (next) {
      const card = next.closest('li.ex-card');
      const after = card && card.nextElementSibling;
      if (after) document.dispatchEvent(new CustomEvent('workout:focus', { detail: after.dataset.exercise }));
    }
  });
  document.addEventListener('toggle', (event) => {
    const details = event.target;
    if (!(details instanceof HTMLDetailsElement) || !details.open) return;
    const card = details.closest('li.ex-card');
    if (!card) return;
    document.querySelectorAll('.ex-chip').forEach((c) => {
      const on = c.dataset.chip === card.dataset.exercise;
      c.classList.toggle('current', on);
      if (on && c.scrollIntoView) c.scrollIntoView({ block: 'nearest', inline: 'center', behavior: 'smooth' });
    });
  }, true);

  // An empty session starts by logging its first exercise.
  document.addEventListener('click', (event) => {
    if (!event.target.closest('[data-free-session]') || !more) return;
    go('train');
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
    if (event.target.id !== 'panel') return;
    if (finishing) setFinish(true);
    // Logging after Terminar picks the workout up again.
    else localStorage.removeItem(FINISHED_KEY);
    syncLive();
  });

  // --- the live workout, everywhere ---------------------------------------
  // The panel is the server's latest word on today's workout; the bar, the
  // Continuar card and the pill on the other tabs are painted from it.
  const FINISHED_KEY = 'workout-finished';
  const panel = () => $('panel');

  // The server says how long ago the session started, by its own clock, so
  // every device shows the same duration whatever its clock says. The old
  // per-device start is dropped.
  localStorage.removeItem('workout-start');
  function readStart(d) {
    const elapsed = parseInt(d.workoutElapsed || '', 10);
    return isNaN(elapsed) ? 0 : Date.now() - elapsed * 1000;
  }

  let startedAt = 0;
  function syncLive() {
    const p = panel();
    if (!p) return;
    const d = p.dataset;
    const active = d.active === '1';
    const finished = active && localStorage.getItem(FINISHED_KEY) === d.sessionId;
    startedAt = active ? readStart(d) : 0;
    const done = parseInt(d.setsDone || '0', 10);
    const count = d.setsPlanned ? done + '/' + d.setsPlanned : String(done);
    const words = {
      title: d.planName || 'Entreno libre',
      count,
      sets: count + (d.setsPlanned || done !== 1 ? ' series' : ' serie'),
      exercises: d.exTotal ? ' · ' + d.exDone + '/' + d.exTotal + ' ejercicios' : ''
    };
    document.querySelectorAll('[data-live]').forEach((el) => {
      if (words[el.dataset.live] !== undefined) el.textContent = words[el.dataset.live];
    });
    const pill = $('workout-pill');
    if (pill) pill.hidden = !active || finished;
    const cont = $('continue');
    if (cont) {
      cont.hidden = !active;
      cont.classList.toggle('finished', finished);
    }
    const idle = document.querySelector('.home-idle');
    if (idle) idle.hidden = active;
    tick();
  }

  function tick() {
    if (!startedAt) return;
    const s = Math.max(0, Math.floor((Date.now() - startedAt) / 1000));
    const h = Math.floor(s / 3600);
    const m = Math.floor((s % 3600) / 60);
    const text = (h ? h + ':' + String(m).padStart(2, '0') : m) + ':' + String(s % 60).padStart(2, '0');
    document.querySelectorAll('[data-clock]').forEach((el) => { el.textContent = text; });
  }
  setInterval(tick, 1000);

  document.addEventListener('workout:finish', () => {
    const p = panel();
    if (p && p.dataset.sessionId) localStorage.setItem(FINISHED_KEY, p.dataset.sessionId);
    syncLive();
  });
  syncLive();

  document.addEventListener('keydown', (event) => {
    if (event.key !== 'Escape') return;
    // shell.js closes the entry sheet, the drawer or a panel first.
    if ((more && more.open) || !$('panel-sheet').hidden) return;
    if (finishing) setFinish(false);
    else closeCoach();
  });
})();
