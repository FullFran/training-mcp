// Connector tool approvals in the chat. The coach asks before every call of a
// tool of a person's connector; this shows the question and sends the answer.
// It has its own stream so it does not depend on the chat's code. Only a press
// here answers: closing the page or doing nothing denies the call on the server.
(function () {
  'use strict';
  if (!window.EventSource) return;
  const base = document.body.dataset.base || '';
  const csrf = document.querySelector('meta[name="csrf-token"]');
  const queue = [];
  let box = null;

  const css = document.createElement('link');
  css.rel = 'stylesheet';
  css.href = base + '/static/connectors.css';
  document.head.append(css);

  const ANSWERS = [
    ['allow_once', 'Permitir una vez', 'allow'],
    ['allow_always', 'Permitir en esta sesión', ''],
    ['reject_once', 'Rechazar', ''],
    ['reject_always', 'Rechazar en esta sesión', ''],
  ];

  function text(tag, value) {
    const node = document.createElement(tag);
    node.textContent = value;
    return node;
  }

  function showNext() {
    if (box || queue.length === 0) return;
    const q = queue[0];
    box = document.createElement('div');
    box.className = 'cx-approval';
    box.setAttribute('role', 'alertdialog');
    box.setAttribute('aria-label', 'Permiso para una herramienta');
    box.append(text('h3', 'El coach quiere usar ' + q.server));
    box.append(text('p', 'Herramienta: ' + q.tool));
    if (q.args) box.append(text('pre', q.args));
    const actions = document.createElement('div');
    actions.className = 'cx-actions';
    for (const [answer, label, cls] of ANSWERS) {
      const b = text('button', label);
      b.type = 'button';
      if (cls) b.className = cls;
      b.addEventListener('click', () => answer && send(q.id, answer));
      actions.append(b);
    }
    box.append(actions);
    document.body.append(box);
    actions.querySelector('button').focus();
  }

  function close(id) {
    const i = queue.findIndex((q) => q.id === id);
    if (i >= 0) queue.splice(i, 1);
    if (box && i === 0) {
      box.remove();
      box = null;
    }
    showNext();
  }

  async function send(id, answer) {
    const headers = { 'X-Coach': '1', 'Content-Type': 'application/json' };
    if (csrf) headers['X-CSRF-Token'] = csrf.content;
    try {
      await fetch(base + '/coach/approval', { method: 'POST', headers, body: JSON.stringify({ id, answer }), credentials: 'same-origin' });
    } finally {
      close(id);
    }
  }

  const stream = new EventSource(base + '/coach/stream');
  stream.addEventListener('approval', (event) => {
    try {
      const q = JSON.parse(event.data);
      if (typeof q.id !== 'string') return;
      queue.push({ id: q.id, server: String(q.server || ''), tool: String(q.tool || ''), args: typeof q.args === 'string' ? q.args : '' });
      showNext();
    } catch (_) { /* not ours */ }
  });
  stream.addEventListener('approval_closed', (event) => {
    try { close(JSON.parse(event.data).id); } catch (_) { /* ignore */ }
  });
})();
