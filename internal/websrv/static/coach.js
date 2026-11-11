// Coach chat, the home screen. It talks to /coach/*: connect ChatGPT with a
// device code, then chat. The stream stays open while the page is, so the
// coach's reaction to a logged set arrives on its own. Every text from the
// model is inserted as text, never as HTML: replies go through markdown.js,
// which builds a small Markdown subset from text nodes.
//
// Each turn reads like ChatGPT: one steps row on top (what the coach looked at,
// in words, never a tool id), then the answer without a bubble, a live line
// while it works, and a card with the one action that helps when it fails or
// is stopped.
//
// Voice is the browser's own speech recognition dictating into the composer.
// No provider, no tokens.
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
  const listeningNote = $('coach-listening');
  const autosend = $('coach-autosend');
  const empty = $('chat-empty');
  const jump = $('chat-jump');

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
    too_many_images: 'Como mucho 4 fotos por mensaje.',
    bad_image: 'Esa foto no se puede enviar (formato o tamaño).',
    images_too_large: 'Las fotos pesan demasiado para un mensaje.',
  };
  // Asking again can help with these; the others need the person to act.
  const RETRY = new Set(['model_busy', 'provider_error', 'busy', 'engine_unavailable', 'turn_limit']);

  // What each training-mcp tool does, in the person's words: while it runs and
  // once done. A tool not listed reads by its name made readable.
  const WORDS = {
    get_session: ['Consultando tu sesión de hoy', 'Consultada tu sesión de hoy'],
    list_sessions: ['Consultando tus sesiones', 'Consultadas tus sesiones'],
    session_progress: ['Mirando cómo va la sesión', 'Mirado cómo va la sesión'],
    live_workout: ['Mirando el entreno en marcha', 'Mirado el entreno en marcha'],
    exercise_history: ['Consultando tu historial', 'Consultado tu historial'],
    list_plans: ['Consultando tus rutinas', 'Consultadas tus rutinas'],
    get_plan: ['Consultando la rutina', 'Consultada la rutina'],
    weekly_volume: ['Calculando tu volumen semanal', 'Calculado tu volumen semanal'],
    volume_by_muscle: ['Calculando el volumen por músculo', 'Calculado el volumen por músculo'],
    volume_recommendation: ['Calculando cuánto volumen te toca', 'Calculado cuánto volumen te toca'],
    list_volume_landmarks: ['Consultando tus marcas de volumen', 'Consultadas tus marcas de volumen'],
    suggest_load: ['Calculando el peso', 'Calculado el peso'],
    list_exercise_notes: ['Consultando tus notas', 'Consultadas tus notas'],
    list_exercise_groups: ['Consultando tus grupos de ejercicios', 'Consultados tus grupos de ejercicios'],
    log_set: ['Registrando la serie', 'Serie registrada'],
    log_sets: ['Registrando las series', 'Series registradas'],
    add_set: ['Registrando la serie', 'Serie registrada'],
    update_set: ['Corrigiendo una serie', 'Serie corregida'],
    delete_set: ['Borrando una serie', 'Serie borrada'],
    create_plan: ['Creando una rutina', 'Rutina creada'],
    update_plan: ['Cambiando la rutina', 'Rutina cambiada'],
    set_plan_exercise: ['Cambiando la rutina', 'Rutina cambiada'],
    remove_plan_exercise: ['Cambiando la rutina', 'Rutina cambiada'],
    move_plan_exercise: ['Cambiando la rutina', 'Rutina cambiada'],
    delete_plan: ['Borrando una rutina', 'Rutina borrada'],
    start_session: ['Empezando la sesión', 'Sesión empezada'],
    add_session_exercise: ['Cambiando la sesión de hoy', 'Sesión de hoy cambiada'],
    adjust_session_exercise: ['Cambiando la sesión de hoy', 'Sesión de hoy cambiada'],
    remove_session_exercise: ['Cambiando la sesión de hoy', 'Sesión de hoy cambiada'],
    swap_session_exercise: ['Cambiando la sesión de hoy', 'Sesión de hoy cambiada'],
    delete_session: ['Borrando una sesión', 'Sesión borrada'],
    save_session_as_plan: ['Guardando la sesión como rutina', 'Sesión guardada como rutina'],
    set_rest_target: ['Cambiando tu descanso', 'Descanso cambiado'],
    set_exercise_note: ['Guardando una nota', 'Nota guardada'],
    set_exercise_group: ['Cambiando un grupo de ejercicios', 'Grupo de ejercicios cambiado'],
    set_volume_landmarks: ['Cambiando tus marcas de volumen', 'Marcas de volumen cambiadas'],
    record_feedback: ['Guardando tu valoración', 'Valoración guardada'],
  };
  function words(name) {
    const w = WORDS[name];
    if (w) return w;
    const readable = String(name || 'herramienta').replace(/_/g, ' ');
    return ['Usando «' + readable + '»', 'Usada la herramienta «' + readable + '»'];
  }

  let stream = null;
  let poll = null;
  let turn = null; // the turn being written
  let running = false;
  let connected = false;
  let lastPrompt = null; // { text, images } for «Volver a intentarlo»

  // --- scroll ------------------------------------------------------------------
  // The view follows the stream until the person scrolls up; reaching the end
  // again picks it up. «↓» brings them back.
  let follow = true;
  let lastTop = 0;
  const atBottom = () => log.scrollHeight - log.scrollTop - log.clientHeight <= 48;
  function scroll(force) {
    if (force) follow = true;
    if (follow) log.scrollTop = log.scrollHeight;
    if (jump) jump.hidden = follow || atBottom();
  }
  log.addEventListener('scroll', () => {
    if (log.scrollTop < lastTop && !atBottom()) follow = false;
    else if (atBottom()) follow = true;
    lastTop = log.scrollTop;
    if (jump) jump.hidden = follow || atBottom();
  }, { passive: true });
  if (jump) jump.addEventListener('click', () => scroll(true));

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

  function showEmpty() {
    if (!empty) return;
    empty.hidden = !connected || Boolean(log.querySelector('.coach-msg'));
  }

  // The person's message: a bubble on the right.
  function mine(value) {
    const div = document.createElement('div');
    div.className = 'coach-msg coach-me';
    const body = document.createElement('div');
    body.className = 'coach-body';
    body.textContent = value;
    div.appendChild(body);
    log.appendChild(div);
    showEmpty();
    return div;
  }

  // A coach turn: steps row, answer, live line. `at` lets the shell put a set
  // card before the reaction it caused.
  function startTurn(origin) {
    const div = document.createElement('div');
    div.className = 'coach-msg coach-coach coach-turn';
    div.dataset.origin = origin || 'user';
    div.dataset.at = String(Date.now());
    if (origin === 'reaction') {
      const small = document.createElement('small');
      small.textContent = 'Sobre tu última serie';
      div.appendChild(small);
    }
    const steps = document.createElement('details');
    steps.className = 'coach-steps';
    steps.hidden = true;
    const summary = document.createElement('summary');
    const dot = document.createElement('span');
    dot.className = 'dot';
    const title = document.createElement('span');
    summary.append(dot, title);
    const list = document.createElement('ol');
    list.setAttribute('aria-label', 'Lo que ha consultado el coach');
    steps.append(summary, list);
    const body = document.createElement('div');
    body.className = 'coach-body';
    const live = document.createElement('p');
    live.className = 'coach-live';
    live.setAttribute('aria-hidden', 'true');
    live.textContent = 'Pensando…';
    div.append(steps, body, live);
    log.appendChild(div);
    showEmpty();
    scroll();
    return { el: div, steps, title, list, body, live, text: '', rows: new Map(), origin: div.dataset.origin };
  }

  // The steps row says what is happening now, or what was done.
  function paintSteps(t, open) {
    const rows = Array.from(t.list.children);
    if (!rows.length) return;
    t.steps.hidden = false;
    const runningRow = rows.slice().reverse().find((r) => r.classList.contains('running'));
    const failed = rows.find((r) => r.classList.contains('error'));
    t.steps.classList.toggle('live', open && Boolean(runningRow));
    t.steps.classList.toggle('problem', !open && Boolean(failed));
    let label;
    if (open && runningRow) label = runningRow.dataset.doing + '…';
    else if (!open && failed) label = 'Algo ha fallado';
    else label = rows[0].textContent;
    if (rows.length > 1) label += ' · ' + rows.length + ' pasos';
    t.title.textContent = label;
  }

  function render(el, value) {
    if (window.coachMarkdown) el.replaceChildren(window.coachMarkdown(value));
    else el.textContent = value;
  }

  // Copy once the answer is complete.
  function tools(t) {
    if (!t.text.trim()) return;
    const bar = document.createElement('div');
    bar.className = 'coach-tools';
    const copy = document.createElement('button');
    copy.type = 'button';
    copy.textContent = 'Copiar';
    copy.addEventListener('click', () => {
      if (!navigator.clipboard) return;
      navigator.clipboard.writeText(t.text).then(() => {
        copy.textContent = 'Copiado';
        setTimeout(() => { copy.textContent = 'Copiar'; }, 1500);
      }).catch(() => {});
    });
    bar.appendChild(copy);
    t.el.appendChild(bar);
  }

  // A failed or stopped turn: what happened and the one action that helps.
  function runCard(where, message, action, stopped) {
    const card = document.createElement('section');
    card.className = 'run-card' + (stopped ? ' stopped' : '');
    card.setAttribute('role', stopped ? 'status' : 'alert');
    const p = document.createElement('p');
    p.textContent = message;
    card.appendChild(p);
    if (action) {
      const actions = document.createElement('div');
      actions.className = 'actions';
      const b = document.createElement('button');
      b.type = 'button';
      b.className = 'ghost';
      b.textContent = action.label;
      b.addEventListener('click', () => { b.disabled = true; action.run(); });
      actions.appendChild(b);
      card.appendChild(actions);
    }
    (where || log).appendChild(card);
    scroll();
  }

  function retryAction() {
    return lastPrompt ? { label: 'Volver a intentarlo', run: () => submit(lastPrompt.text, lastPrompt.images, false) } : null;
  }

  function failure(code, where) {
    if (code === 'turn_in_progress') { note(MESSAGES[code]); return; }
    runCard(where, MESSAGES[code] || MESSAGES.provider_error, RETRY.has(code) ? retryAction() : null);
    if (code === 'reconnect' || code === 'not_connected') refresh();
  }

  function endTurn() {
    if (!turn) return;
    turn.live.remove();
    paintSteps(turn, false);
    if (!turn.text.trim() && !turn.list.children.length) turn.body.remove();
    const t = turn;
    turn = null;
    return t;
  }

  function setRunning(on) {
    running = on;
    send.hidden = on;
    stop.hidden = !on;
  }

  function show(status) {
    clearTimeout(poll);
    connected = status.state === 'connected';
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
    showEmpty();
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
      endTurn();
      turn = startTurn(data(ev).origin);
      setRunning(true);
    });
    stream.addEventListener('delta', (ev) => {
      if (!turn) turn = startTurn('user');
      turn.text += data(ev).text || '';
      turn.live.textContent = 'Escribiendo…';
      render(turn.body, turn.text);
      scroll();
    });
    stream.addEventListener('tool', (ev) => {
      const d = data(ev);
      if (!turn) turn = startTurn('user');
      const key = d.call ? 'c' + d.call : 'n' + d.name + turn.list.children.length;
      let row = d.call ? turn.rows.get(key) : null;
      const w = words(d.name);
      if (!row) {
        row = document.createElement('li');
        row.dataset.doing = w[0];
        turn.rows.set(key, row);
        turn.list.appendChild(row);
      }
      row.className = '';
      if (d.phase === 'start') { row.className = 'running'; row.textContent = w[0]; }
      else if (d.pending === true) {
        row.className = 'waiting';
        row.textContent = 'Cambio propuesto, pendiente de tu aprobación';
        document.dispatchEvent(new CustomEvent('coach:proposal'));
      } else if (d.ok === false) { row.className = 'error'; row.textContent = w[0] + ': ha fallado'; }
      else row.textContent = w[1];
      paintSteps(turn, true);
      scroll();
    });
    stream.addEventListener('done', (ev) => {
      const t = endTurn();
      setRunning(false);
      if (!t) return;
      if (data(ev).aborted) {
        const again = t.origin === 'user' ? retryAction() : null;
        if (again) again.label = 'Volver a pedirla';
        runCard(t.el, 'Detuviste la respuesta.', again, true);
      } else tools(t);
    });
    stream.addEventListener('error', (ev) => {
      // A server event named "error" carries a code; a broken connection does not.
      if (!ev.data) return;
      const t = endTurn();
      setRunning(false);
      failure(data(ev).code, t && t.el);
    });
  }

  function submit(value, images, echo) {
    openStream();
    if (echo) {
      const me = mine(value);
      if (images.length && window.coachAttach) window.coachAttach.show(me, images);
    }
    lastPrompt = { text: value, images };
    scroll(true);
    return post('prompt', images.length ? { text: value, images } : { text: value }).then(({ ok, data }) => {
      if (ok) return;
      if (echo && images.length && window.coachAttach) window.coachAttach.restore(images);
      if (['bad_request', 'too_many_images', 'bad_image', 'images_too_large'].includes(data.error)) {
        note(MESSAGES[data.error], 'error');
        return;
      }
      failure(data.error);
    });
  }

  // --- dictation ---------------------------------------------------------------
  // One thumb mid-set: tap the mic and talk. Recognition stops by itself after
  // a silence, or on a second tap; the words land in the message box, added to
  // whatever was typed. With auto-send on, they go to the coach right away.
  const Recognition = window.SpeechRecognition || window.webkitSpeechRecognition;
  const AUTOSEND_KEY = 'coach-autosend';
  let listening = null;

  function setListening(rec) {
    listening = rec;
    const on = Boolean(rec);
    mic.classList.toggle('on', on);
    mic.setAttribute('aria-pressed', String(on));
    mic.setAttribute('aria-label', on ? 'Terminar de dictar' : 'Dictar');
    listeningNote.hidden = !on;
  }

  if (Recognition) {
    mic.hidden = false;
    $('coach-autosend-wrap').hidden = false;
    autosend.checked = localStorage.getItem(AUTOSEND_KEY) === '1';
    autosend.addEventListener('change', () => {
      localStorage.setItem(AUTOSEND_KEY, autosend.checked ? '1' : '0');
    });

    mic.addEventListener('click', () => {
      if (listening) { listening.stop(); return; }
      const rec = new Recognition();
      rec.lang = document.documentElement.lang === 'es' ? 'es-ES' : (document.documentElement.lang || 'es-ES');
      rec.interimResults = true;
      rec.continuous = false; // ends on its own after a pause
      const before = text.value ? text.value.replace(/\s*$/, ' ') : '';
      let heard = '';
      rec.onresult = (ev) => {
        heard = '';
        for (let i = 0; i < ev.results.length; i++) heard += ev.results[i][0].transcript;
        text.value = before + heard;
      };
      rec.onerror = (ev) => {
        if (ev.error === 'not-allowed' || ev.error === 'service-not-allowed') note('Sin permiso para usar el micrófono.', 'error');
        else if (ev.error === 'no-speech') note('No te he oído. Toca 🎤 y habla.');
        else if (ev.error === 'network') note('El dictado necesita conexión.', 'error');
      };
      rec.onend = () => {
        if (listening !== rec) return;
        setListening(null);
        if (heard.trim() && autosend.checked && !running) form.requestSubmit();
        else text.focus();
      };
      setListening(rec);
      if (navigator.vibrate) navigator.vibrate(8);
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
      log.querySelectorAll('.coach-msg, .coach-note, .run-card').forEach((el) => el.remove());
      turn = null;
      refresh();
    });
  });

  form.addEventListener('submit', (ev) => {
    ev.preventDefault();
    const value = text.value.trim();
    // Photos come from coach-attach.js when it is on the page.
    const attach = window.coachAttach;
    if (running || (attach && attach.busy())) return;
    const images = attach ? attach.take() : [];
    if (!value && !images.length) return;
    text.value = '';
    grow();
    submit(value, images, true);
  });

  // The message box grows with what is typed, up to its CSS limit.
  function grow() {
    text.style.height = 'auto';
    text.style.height = text.scrollHeight + 'px';
  }
  text.addEventListener('input', grow);

  text.addEventListener('keydown', (ev) => {
    if (ev.key === 'Enter' && !ev.shiftKey) {
      ev.preventDefault();
      form.requestSubmit();
    }
  });

  // A suggestion is sent as if typed.
  if (empty) {
    empty.addEventListener('click', (ev) => {
      const b = ev.target.closest('[data-suggest]');
      if (!b || running) return;
      text.value = b.dataset.suggest;
      form.requestSubmit();
    });
  }

  stop.addEventListener('click', () => post('abort'));

  refresh();
})();
