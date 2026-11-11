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
    const freshLink = doc.getElementById('review-link');
    const link = $('review-link');
    if (freshLink && link) {
      link.textContent = freshLink.textContent;
      link.className = freshLink.className;
    }
    const workout = $('workout');
    const freshWorkout = doc.getElementById('workout');
    if (workout && freshWorkout) workout.classList.toggle('idle', freshWorkout.classList.contains('idle'));
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

  // --- panels ---------------------------------------------------------------
  let changed = false;

  function openPanel(link) {
    title.textContent = link.textContent.replace(/\s*\(\d+\)$/, '');
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
    openPanel(link);
  });

  $('panel-back').addEventListener('click', closePanel);
  document.addEventListener('keydown', (event) => {
    if (event.key === 'Escape') closePanel();
  });

  // Inside the panel, the page's own link home closes it, and any form sent
  // there marks the chat for a refresh. A form that lands on the home page
  // (choosing a plan, signing out) closes the panel too.
  frame.addEventListener('load', () => {
    let doc;
    try { doc = frame.contentDocument; } catch (e) { return; }
    if (!doc || sheet.hidden || frame.src === 'about:blank') return;
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

  // A set logged from the workout card, with the values the server stored.
  document.addEventListener('workout:set-logged', (event) => {
    const d = event.detail || {};
    if (!d.exercise) return;
    log.appendChild(card('set-card', '✓ ' + d.exercise,
      d.weight + ' kg × ' + d.reps + (d.rpe ? ' @' + d.rpe : '')));
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
        const freshLink = doc.getElementById('review-link');
        const link = $('review-link');
        if (freshLink && link) {
          link.textContent = freshLink.textContent;
          link.className = freshLink.className;
        }
        scroll();
      })
      .catch(() => {});
  });

  // Accept and reject post to the review endpoints with the session's token,
  // then the card says what happened. A refused answer shows the reason.
  log.addEventListener('submit', (event) => {
    const form = event.target;
    const proposal = form.closest('.proposal-card');
    const pick = form.classList.contains('plan-pick');
    if (!proposal && !pick) return;
    event.preventDefault();
    const headers = { 'Content-Type': 'application/x-www-form-urlencoded' };
    if (csrf) headers['X-CSRF-Token'] = csrf;
    if (proposal) headers['HX-Request'] = 'true';
    form.querySelectorAll('button').forEach((b) => { b.disabled = true; });
    fetch(form.action, {
      method: 'POST', credentials: 'same-origin', headers,
      body: new URLSearchParams(new FormData(form)).toString(),
    })
      .then((res) => res.text())
      .then((html) => {
        if (pick) { apply(html); return; } // the plan redirects to a fresh home
        const doc = new DOMParser().parseFromString(html, 'text/html');
        const alert = doc.querySelector('.alert');
        const verdict = document.createElement('p');
        verdict.className = alert ? 'coach-note coach-error' : 'coach-note';
        verdict.textContent = alert ? alert.textContent
          : (form.action.endsWith('/accept') ? 'Aceptada.' : 'Rechazada.');
        proposal.querySelector('.card-actions').replaceWith(verdict);
        proposal.removeAttribute('data-proposal');
        const link = $('review-link');
        const left = doc.querySelectorAll('article.proposal').length;
        if (link) {
          link.textContent = 'Revisar' + (left ? ' (' + left + ')' : '');
          link.classList.toggle('attention', left > 0);
        }
      })
      .catch(() => {
        form.querySelectorAll('button').forEach((b) => { b.disabled = false; });
      });
  });

  scroll();
})();
