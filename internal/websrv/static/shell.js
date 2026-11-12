// Chat-first home. Panels (history, plans, review, progress) open over the chat
// in a frame and close back to it, so the conversation and its stream are never
// reloaded. Cards in the chat come from the server: a logged set, the plan of
// the day, proposals waiting for an answer. Nothing here needs the model.
(function () {
  'use strict';

  const base = document.body.dataset.base || '';
  const $ = (id) => document.getElementById(id);
  const log = $('coach-log');
  const cards = $('chat-cards');
  const sheet = $('panel-sheet');
  const frame = $('panel-frame');
  const title = $('panel-title');
  if (!log || !cards || !sheet || !frame) return;

  const csrfMeta = document.querySelector('meta[name="csrf-token"]');
  const csrf = csrfMeta ? csrfMeta.content : '';
  const home = base + '/';
  const scroll = () => { log.scrollTop = log.scrollHeight; };

  // --- refresh from the server --------------------------------------------
  // After a change made in a panel, the workout panel, the server's cards and
  // the review count are taken from a fresh home page; the conversation stays.
  // verdictText says on a chat card what the person's answer did.
  function verdictText(accepted, answer) {
    if (answer === 'allow_always') return 'Aceptada, y aceptadas siempre en esta sesión.';
    if (answer === 'reject_always') return 'Rechazada, y rechazadas siempre en esta sesión.';
    return accepted ? 'Aceptada.' : 'Rechazada.';
  }

  function apply(html) {
    const doc = new DOMParser().parseFromString(html, 'text/html');
    const fresh = doc.getElementById('panel');
    if (!fresh) { window.location.reload(); return; } // signed out, or an error page
    const panel = $('panel');
    if (panel) {
      const node = document.importNode(fresh, true);
      panel.replaceWith(node);
      if (window.htmx) window.htmx.process(node);
      // app.js keeps the timer, the next exercise and the quick log in step.
      node.dispatchEvent(new CustomEvent('htmx:afterSwap', { bubbles: true }));
    }
    const freshCards = doc.getElementById('chat-cards');
    if (freshCards) {
      cards.replaceChildren(...Array.from(freshCards.childNodes).map((n) => document.importNode(n, true)));
    }
    syncReview(doc);
    const workout = $('workout');
    const freshWorkout = doc.getElementById('workout');
    if (workout && freshWorkout) workout.classList.toggle('idle', freshWorkout.classList.contains('idle'));
  }

  // The review count, in the drawer and as the badge in the top bar.
  function setReview(count) {
    const link = $('review-link');
    if (link) {
      link.textContent = '✅ Revisar' + (count ? ' (' + count + ')' : '');
      link.classList.toggle('attention', count > 0);
    }
    // Planificar's Revisión chip.
    document.querySelectorAll('[data-review-count]').forEach((badge) => {
      badge.hidden = !count;
      badge.textContent = String(count);
    });
  }
  function syncReview(doc) {
    const fresh = doc.querySelector('[data-review-count]');
    if (fresh) setReview(fresh.hidden ? 0 : parseInt(fresh.textContent.replace(/\D/g, ''), 10) || 0);
  }

  // Only the server's cards and the review count, after a set: the workout
  // panel itself was just swapped by htmx.
  function refreshCards() {
    return fetch(home, { credentials: 'same-origin' })
      .then((res) => res.text())
      .then((html) => {
        const doc = new DOMParser().parseFromString(html, 'text/html');
        const freshCards = doc.getElementById('chat-cards');
        if (freshCards) {
          cards.replaceChildren(...Array.from(freshCards.childNodes).map((n) => document.importNode(n, true)));
        }
      })
      .catch(() => {});
  }

  function refresh() {
    return fetch(home, { credentials: 'same-origin' })
      .then((res) => res.text())
      .then(apply)
      .catch(() => {});
  }

  // --- drawer ---------------------------------------------------------------
  // The menu: panels and the account. It closes on the scrim, Escape, or when
  // a panel opens from it.
  const drawer = $('drawer');
  const scrim = $('drawer-scrim');
  const menu = $('menu-open');
  function setDrawer(open) {
    if (!drawer) return;
    drawer.hidden = !open;
    if (scrim) scrim.hidden = !open;
    if (menu) menu.setAttribute('aria-expanded', String(open));
    if (open) document.dispatchEvent(new CustomEvent('drawer:open'));
  }
  // The coach closes the drawer after a conversation is picked.
  document.addEventListener('drawer:close', () => setDrawer(false));
  if (menu) menu.addEventListener('click', () => setDrawer(drawer.hidden));
  if (scrim) scrim.addEventListener('click', () => setDrawer(false));

  // --- workout sheet ----------------------------------------------------------
  const more = $('wk-more');
  document.addEventListener('click', (event) => {
    if (more && event.target.closest('[data-close-sheet]')) more.open = false;
  });

  // --- panels ---------------------------------------------------------------
  let changed = false;

  function openPanel(link) {
    title.textContent = link.dataset.title || link.textContent.replace(/\s*\(\d+\)$/, '');
    changed = false;
    frame.src = link.href;
    sheet.hidden = false;
    document.body.classList.add('panel-open');
  }

  function closePanel() {
    if (sheet.hidden) return;
    sheet.hidden = true;
    document.body.classList.remove('panel-open');
    frame.src = 'about:blank';
    if (changed) refresh();
    changed = false;
  }

  const isHome = (href) => {
    try { return new URL(href, window.location.href).pathname === home; } catch (e) { return false; }
  };

  document.addEventListener('click', (event) => {
    const link = event.target.closest('a[data-panel]');
    if (!link || event.metaKey || event.ctrlKey || event.shiftKey) return;
    event.preventDefault();
    setDrawer(false);
    openPanel(link);
  });

  $('panel-back').addEventListener('click', closePanel);
  document.addEventListener('keydown', (event) => {
    if (event.key !== 'Escape') return;
    if (more && more.open) more.open = false;
    else if (drawer && !drawer.hidden) setDrawer(false);
    else closePanel();
  });

  // Inside the panel, the page's own link home closes it, and any form sent
  // there marks the chat for a refresh. A form that lands on the home page
  // (choosing a plan, signing out) closes the panel too.
  frame.addEventListener('load', () => {
    let doc;
    try { doc = frame.contentDocument; } catch (e) { return; }
    if (!doc || sheet.hidden || frame.src === 'about:blank') return;
    doc.documentElement.classList.add('in-panel');
    // autofocus does not fire inside the frame: creating a routine lands on
    // its name, and its editor on the exercise field.
    const first = doc.querySelector('[autofocus]');
    if (first) first.focus();
    if (isHome(doc.location.href)) {
      changed = true;
      closePanel();
      return;
    }
    doc.addEventListener('click', (event) => {
      const a = event.target.closest('a[href]');
      if (a && isHome(a.href)) {
        event.preventDefault();
        closePanel();
      }
    });
    doc.addEventListener('submit', () => { changed = true; }, true);
    doc.addEventListener('htmx:afterRequest', () => { changed = true; });
  });

  // --- cards ----------------------------------------------------------------
  function card(className, heading, detail) {
    const article = document.createElement('article');
    article.className = 'chat-card ' + className;
    const strong = document.createElement('strong');
    strong.textContent = heading;
    article.appendChild(strong);
    if (detail) {
      const span = document.createElement('span');
      span.className = 'muted';
      span.textContent = ' ' + detail;
      article.appendChild(span);
    }
    return article;
  }

  // A set logged from the workout card, with the values the server stored. The
  // server starts the coach's reaction as soon as it stores the set, so that
  // reaction can reach the chat before this card: the card goes before it.
  let setSentAt = 0;
  document.body.addEventListener('htmx:beforeRequest', (event) => {
    if (event.target && event.target.id === 'entry') setSentAt = Date.now();
  });
  document.addEventListener('workout:set-logged', (event) => {
    const d = event.detail || {};
    if (!d.exercise) return;
    const el = card('set-card', '✓ ' + d.exercise,
      d.weight + ' ' + (d.unit || 'kg') + ' × ' + d.reps + (d.rpe ? ' @' + d.rpe : ''));
    const reaction = Array.from(log.querySelectorAll('.coach-turn[data-origin="reaction"]'))
      .find((t) => Number(t.dataset.at) >= setSentAt);
    if (setSentAt && reaction) log.insertBefore(el, reaction);
    else log.appendChild(el);
    scroll();
    refreshCards();
  });

  // The coach proposed a change: bring its card in without leaving the chat.
  // Its card goes at the end of the conversation, where the coach mentioned it.
  document.addEventListener('coach:proposal', () => {
    fetch(home, { credentials: 'same-origin' })
      .then((res) => res.text())
      .then((html) => {
        const doc = new DOMParser().parseFromString(html, 'text/html');
        doc.querySelectorAll('.proposal-card[data-proposal]').forEach((fresh) => {
          if (document.querySelector('[data-proposal="' + fresh.dataset.proposal + '"]')) return;
          log.appendChild(document.importNode(fresh, true));
        });
        syncReview(doc);
        scroll();
      })
      .catch(() => {});
  });

  // Accept and reject post to the review endpoints with the session's token,
  // then the card says what happened. A refused answer shows the reason.
  // Choosing a plan, from the chat or from Entrenar's start, refreshes in place
  // and lands on the training screen.
  document.addEventListener('submit', (event) => {
    const form = event.target;
    if (!form.closest('#coach-log, #train')) return;
    const proposal = form.closest('.proposal-card');
    const pick = form.classList.contains('plan-pick');
    if (!proposal && !pick) return;
    event.preventDefault();
    const headers = { 'Content-Type': 'application/x-www-form-urlencoded' };
    if (csrf) headers['X-CSRF-Token'] = csrf;
    if (proposal) headers['HX-Request'] = 'true';
    // The answer is the pressed button's value, so the body is read before the
    // buttons are disabled (a disabled submitter is left out of the form data).
    const body = new URLSearchParams(new FormData(form, event.submitter)).toString();
    form.querySelectorAll('button').forEach((b) => { b.disabled = true; });
    fetch(form.action, {
      method: 'POST', credentials: 'same-origin', headers,
      body,
    })
      .then((res) => res.text())
      .then((html) => {
        if (pick) { // the plan redirects to a fresh training screen
          apply(html);
          document.dispatchEvent(new CustomEvent('mode:go', { detail: 'train' }));
          return;
        }
        const doc = new DOMParser().parseFromString(html, 'text/html');
        const alert = doc.querySelector('.alert');
        const verdict = document.createElement('p');
        verdict.className = alert ? 'coach-note coach-error' : 'coach-note';
        verdict.textContent = alert ? alert.textContent
          : verdictText(form.action.endsWith('/accept'), event.submitter && event.submitter.value);
        proposal.querySelector('.card-actions').replaceWith(verdict);
        proposal.removeAttribute('data-proposal');
        setReview(doc.querySelectorAll('article.proposal').length);
      })
      .catch(() => {
        form.querySelectorAll('button').forEach((b) => { b.disabled = false; });
      });
  });

  // The chat was out of sight (the home, or Entrenar under the overlay).
  document.addEventListener('coach:shown', scroll);

  scroll();
})();
