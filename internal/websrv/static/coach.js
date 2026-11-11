// Coach chat. The sheet talks to /coach/*: connect ChatGPT with a device code,
// then chat. Once the chat has been opened on this page, its stream stays open,
// so the coach's reaction to a logged set arrives even with the sheet closed.
// Every text from the model is inserted as text, never as HTML.
(function () {
  'use strict';

  const base = document.body.dataset.base || '';
  const $ = (id) => document.getElementById(id);
  const sheet = $('coach');
  if (!sheet) return;
  const fab = $('coach-open');
  const dot = $('coach-dot');
  const stateLabel = $('coach-state');
  const connect = $('coach-connect');
  const codeBox = $('coach-code');
  const log = $('coach-log');
  const form = $('coach-form');
  const text = $('coach-text');
  const send = $('coach-send');
  const stop = $('coach-stop');
  const disconnect = $('coach-disconnect');

  const MESSAGES = {
    model_busy: 'El modelo está saturado. Prueba en un momento.',
    reconnect: 'Vuelve a conectar ChatGPT.',
    provider_error: 'OpenAI ha devuelto un error.',
    busy: 'El coach está ocupado con otras personas. Prueba en un momento.',
    rate_limited: 'Demasiados intentos de conexión. Espera unos minutos.',
    turn_in_progress: 'El coach todavía está respondiendo.',
    turn_limit: 'La respuesta era demasiado larga y se ha cortado.',
    not_connected: 'Conecta ChatGPT para hablar con el coach.',
    engine_unavailable: 'El coach no está disponible ahora mismo.',
    login_failed: 'No se pudo conectar ChatGPT.',
    bad_request: 'Mensaje vacío o demasiado largo.',
  };

  let stream = null;
  let poll = null;
  let current = null; // the answer being written
  let running = false;

  function headers() {
    const h = { 'Content-Type': 'application/json', 'X-Coach': '1' };
    const csrf = document.querySelector('meta[name="csrf-token"]');
    if (csrf) h['X-CSRF-Token'] = csrf.content;
    return h;
  }

  function post(path, body) {
    return fetch(base + '/coach/' + path, {
      method: 'POST', headers: headers(), credentials: 'same-origin',
      body: JSON.stringify(body || {}),
    }).then((res) => res.json().catch(() => ({})).then((data) => ({ ok: res.ok, data })));
  }

  function note(message, kind) {
    const p = document.createElement('p');
    p.className = 'coach-note' + (kind ? ' coach-' + kind : '');
    p.textContent = message;
    log.appendChild(p);
    log.scrollTop = log.scrollHeight;
  }

  function bubble(who, label) {
    const div = document.createElement('div');
    div.className = 'coach-msg coach-' + who;
    if (label) {
      const small = document.createElement('small');
      small.textContent = label;
      div.appendChild(small);
    }
    const body = document.createElement('p');
    div.appendChild(body);
    log.appendChild(div);
    log.scrollTop = log.scrollHeight;
    return body;
  }

  function setRunning(on) {
    running = on;
    send.disabled = on;
    stop.hidden = !on;
  }

  function show(status) {
    clearTimeout(poll);
    const connected = status.state === 'connected';
    connect.hidden = connected;
    form.hidden = !connected;
    disconnect.hidden = !(connected || status.state === 'reconnect');
    codeBox.hidden = status.state !== 'pending';
    $('coach-login').hidden = status.state === 'pending';
    stateLabel.textContent = {
      connected: 'ChatGPT conectado',
      pending: 'Esperando el código',
      reconnect: 'Vuelve a conectar ChatGPT',
      disconnected: 'ChatGPT sin conectar',
    }[status.state] || '';
    if (status.state === 'pending') {
      if (status.user_code) $('coach-usercode').textContent = status.user_code;
      if (status.verification_uri) {
        const a = $('coach-uri');
        a.href = status.verification_uri;
        a.textContent = status.verification_uri.replace(/^https:\/\//, '');
      }
      poll = setTimeout(refresh, 3000);
    }
    if (connected) openStream();
  }

  function refresh() {
    return fetch(base + '/coach/status', { credentials: 'same-origin' })
      .then((res) => res.json())
      .then(show)
      .catch(() => { stateLabel.textContent = 'Sin conexión'; });
  }

  function openStream() {
    if (stream) return;
    stream = new EventSource(base + '/coach/stream');
    const data = (ev) => { try { return JSON.parse(ev.data); } catch (e) { return {}; } };
    stream.addEventListener('turn', (ev) => {
      const d = data(ev);
      current = bubble('coach', d.origin === 'reaction' ? 'Sobre tu última serie' : '');
      setRunning(true);
      if (sheet.hidden) dot.hidden = false;
    });
    stream.addEventListener('delta', (ev) => {
      if (!current) current = bubble('coach');
      current.textContent += data(ev).text || '';
      log.scrollTop = log.scrollHeight;
    });
    stream.addEventListener('tool', (ev) => {
      const d = data(ev);
      if (d.phase === 'end' && d.ok === false) note('Herramienta con error: ' + (d.name || ''), 'error');
      else if (d.phase === 'start') note('Consultando ' + (d.name || '').replace(/_/g, ' ') + '…', 'step');
    });
    stream.addEventListener('done', (ev) => {
      if (data(ev).aborted) note('Detenido.');
      current = null;
      setRunning(false);
    });
    stream.addEventListener('error', (ev) => {
      // A server event named "error" carries a code; a broken connection does not.
      if (ev.data) {
        const code = data(ev).code;
        note(MESSAGES[code] || MESSAGES.provider_error, 'error');
        current = null;
        setRunning(false);
        if (code === 'reconnect') refresh();
      }
    });
  }

  function open() {
    sheet.hidden = false;
    fab.setAttribute('aria-expanded', 'true');
    dot.hidden = true;
    refresh();
  }

  function close() {
    sheet.hidden = true;
    fab.setAttribute('aria-expanded', 'false');
    clearTimeout(poll);
  }

  fab.addEventListener('click', () => (sheet.hidden ? open() : close()));
  $('coach-close').addEventListener('click', close);

  $('coach-login').addEventListener('click', () => {
    post('login').then(({ ok, data }) => {
      if (!ok) return note(MESSAGES[data.error] || MESSAGES.login_failed, 'error');
      show({ state: 'pending', user_code: data.user_code, verification_uri: data.verification_uri });
    });
  });

  disconnect.addEventListener('click', () => {
    if (!window.confirm('¿Desconectar tu cuenta de ChatGPT del coach?')) return;
    post('disconnect').then(() => {
      if (stream) { stream.close(); stream = null; }
      log.textContent = '';
      refresh();
    });
  });

  form.addEventListener('submit', (ev) => {
    ev.preventDefault();
    const value = text.value.trim();
    if (!value || running) return;
    openStream();
    bubble('me').textContent = value;
    text.value = '';
    post('prompt', { text: value }).then(({ ok, data }) => {
      if (!ok) {
        note(MESSAGES[data.error] || MESSAGES.provider_error, 'error');
        if (data.error === 'reconnect' || data.error === 'not_connected') refresh();
      }
    });
  });

  text.addEventListener('keydown', (ev) => {
    if (ev.key === 'Enter' && !ev.shiftKey) {
      ev.preventDefault();
      form.requestSubmit();
    }
  });

  stop.addEventListener('click', () => post('abort'));
})();
