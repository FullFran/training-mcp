// "Aplicado: <label> · Deshacer": the changes accepted in the last 12 hours
// that can still be undone, newest first and at most five, as the server
// lists them (GET /review/undoable). Undoing posts to the proposal's undo_url.
(function () {
  'use strict';

  const base = document.body.dataset.base || '';
  const box = document.getElementById('undo-cards');
  if (!box) return;
  const hidden = new Set(); // undone here; the list may lag a moment behind

  function headers() {
    const h = {};
    const csrf = document.querySelector('meta[name="csrf-token"]');
    if (csrf) h['X-CSRF-Token'] = csrf.content;
    return h;
  }

  function card(p) {
    const row = document.createElement('div');
    row.className = 'undo-card';
    row.dataset.undo = String(p.id);
    const text = document.createElement('span');
    text.className = 'undo-text';
    const label = document.createElement('strong');
    label.textContent = 'Aplicado: ' + p.label;
    text.appendChild(label);
    if (p.summary) {
      const summary = document.createElement('span');
      summary.className = 'muted';
      summary.textContent = p.summary;
      text.appendChild(summary);
    }
    const undo = document.createElement('button');
    undo.type = 'button';
    undo.className = 'undo-btn';
    undo.textContent = 'Deshacer';
    undo.addEventListener('click', () => {
      undo.disabled = true;
      fetch(p.undo_url, { method: 'POST', headers: headers(), credentials: 'same-origin' })
        .then((res) => {
          if (!res.ok) throw new Error(String(res.status));
          hidden.add(p.id);
          row.classList.add('undone');
          label.textContent = 'Deshecho: ' + p.label;
          undo.remove();
          // The workout and the cards may have changed.
          document.dispatchEvent(new CustomEvent('coach:proposal'));
          setTimeout(() => { row.remove(); }, 4000);
        })
        .catch(() => {
          undo.disabled = false;
          undo.textContent = 'No se pudo deshacer';
          setTimeout(() => { undo.textContent = 'Deshacer'; }, 3000);
        });
    });
    row.append(text, undo);
    return row;
  }

  let pending = null;
  function load() {
    if (pending) return pending;
    pending = fetch(base + '/review/undoable', { credentials: 'same-origin' })
      .then((res) => (res.ok ? res.json() : { proposals: [] }))
      .then((data) => {
        const list = (data.proposals || []).filter((p) => !hidden.has(p.id));
        const keep = Array.from(box.querySelectorAll('.undo-card.undone'));
        box.replaceChildren(...keep, ...list.map(card));
      })
      .catch(() => {})
      .finally(() => { pending = null; });
    return pending;
  }

  load();
  // After anything that may accept a change: an answer (an approval mode applies
  // during it), a new proposal, or a card's form in the chat.
  document.addEventListener('coach:answer', load);
  document.addEventListener('coach:shown', load);
  document.addEventListener('submit', (event) => {
    if (event.target.closest('#chat-cards')) setTimeout(load, 800);
  }, true);
})();
