// Coach chat, the home screen. It talks to /coach/*: connect ChatGPT with a
// device code, then chat. The stream stays open while the page is, so the
// coach's reaction to a logged set arrives on its own. Every text from the
// model is inserted as text, never as HTML.
//
// Voice is the browser's own: speech recognition dictates into the composer
// and speech synthesis reads replies aloud. No provider, no tokens.
(function () {
  'use strict';

  const base = document.body.dataset.base || '';
  const $ = (id) => document.getElementById(id);
  if (!$('coach')) return;
  const stateLabel = $('coach-state');
  const connect = $('coach-connect');
  const codeBox = $('coach-code');
  const log = $('coach-log');
  const form = $('coach-form');
  const text = $('coach-text');
  const send = $('coach-send');
  const stop = $('coach-stop');
  const disconnect = $('coach-disconnect');
  const mic = $('coach-mic');
  const speak = $('coach-speak');

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
  let said = ''; // the answer being written, kept for reading aloud

  const scroll = () => { log.scrollTop = log.scrollHeight; };

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
    scroll();
  }

  // A write the coach asked for waits for the person's approval: the shell
  // brings its card, with accept and reject, into the chat.
  function pendingNote() {
    const p = document.createElement('p');
    p.className = 'coach-note coach-pending';
    p.append('Cambio propuesto, pendiente de tu aprobación. ');
    const a = document.createElement('a');
    a.href = base + '/review';
    a.dataset.panel = '';
    a.textContent = 'Revisar';
    p.appendChild(a);
    log.appendChild(p);
    scroll();
    document.dispatchEvent(new CustomEvent('coach:proposal'));
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
    scroll();
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
      said = '';
      setRunning(true);
    });
    stream.addEventListener('delta', (ev) => {
      if (!current) current = bubble('coach');
      const piece = data(ev).text || '';
      current.textContent += piece;
      said += piece;
      scroll();
    });
    stream.addEventListener('tool', (ev) => {
      const d = data(ev);
      if (d.phase === 'end' && d.pending === true) pendingNote();
      else if (d.phase === 'end' && d.ok === false) note('Herramienta con error: ' + (d.name || ''), 'error');
      else if (d.phase === 'start') note('Consultando ' + (d.name || '').replace(/_/g, ' ') + '…', 'step');
    });
    stream.addEventListener('done', (ev) => {
      if (data(ev).aborted) note('Detenido.');
      else readAloud(said);
      said = '';
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

  // --- voice ----------------------------------------------------------------
  // Read-aloud is a per-device choice; it only speaks finished replies.
  const SPEAK_KEY = 'coach-speak';
  const canSpeak = 'speechSynthesis' in window && 'SpeechSynthesisUtterance' in window;
  let speaking = canSpeak && localStorage.getItem(SPEAK_KEY) === '1';

  function paintSpeak() {
    speak.hidden = !canSpeak;
    speak.setAttribute('aria-pressed', String(speaking));
    speak.textContent = speaking ? '🔊 Leyendo respuestas' : '🔈 Leer respuestas';
  }

  function readAloud(textToRead) {
    if (!speaking || !textToRead.trim()) return;
    const u = new SpeechSynthesisUtterance(textToRead);
    u.lang = document.documentElement.lang || 'es';
    window.speechSynthesis.speak(u);
  }

  speak.addEventListener('click', () => {
    speaking = !speaking;
    localStorage.setItem(SPEAK_KEY, speaking ? '1' : '0');
    if (!speaking) window.speechSynthesis.cancel();
    paintSpeak();
  });
  paintSpeak();

  // Dictation appends to whatever is already typed; the person still sends.
  const Recognition = window.SpeechRecognition || window.webkitSpeechRecognition;
  let listening = null;
  if (Recognition) {
    mic.hidden = false;
    mic.addEventListener('click', () => {
      if (listening) { listening.stop(); return; }
      const rec = new Recognition();
      rec.lang = document.documentElement.lang === 'es' ? 'es-ES' : (document.documentElement.lang || 'es-ES');
      rec.interimResults = true;
      const before = text.value ? text.value.replace(/\s*$/, ' ') : '';
      rec.onresult = (ev) => {
        let heard = '';
        for (let i = 0; i < ev.results.length; i++) heard += ev.results[i][0].transcript;
        text.value = before + heard;
      };
      rec.onerror = (ev) => {
        if (ev.error === 'not-allowed' || ev.error === 'service-not-allowed') note('Sin permiso para usar el micrófono.', 'error');
      };
      rec.onend = () => {
        listening = null;
        mic.classList.remove('on');
        mic.setAttribute('aria-pressed', 'false');
        text.focus();
      };
      listening = rec;
      mic.classList.add('on');
      mic.setAttribute('aria-pressed', 'true');
      try { rec.start(); } catch (e) { rec.onend(); }
    });
  }

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
      // Only the conversation goes; the server's cards stay.
      log.querySelectorAll('.coach-msg, .coach-note').forEach((el) => el.remove());
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

  refresh();
})();
