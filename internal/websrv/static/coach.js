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
    performance_dropoff: ['Midiendo cuánto bajas de serie a serie', 'Medido cuánto bajas de serie a serie'],
    memory_context: ['Recordando lo que sé de ti', 'Recordado lo que sé de ti'],
    memory_search: ['Buscando en lo que recuerdo de ti', 'Buscado en lo que recuerdo de ti'],
    memory_get: ['Consultando un recuerdo', 'Consultado un recuerdo'],
    memory_save: ['Guardando un recuerdo', 'Recuerdo guardado'],
    memory_forget: ['Olvidando un recuerdo', 'Recuerdo olvidado'],
    memory_judge: ['Relacionando recuerdos', 'Recuerdos relacionados'],
    memory_confirm: ['Confirmando un recuerdo', 'Recuerdo confirmado'],
    memory_consolidate_preview: ['Viendo cómo unir recuerdos', 'Visto cómo unir recuerdos'],
    memory_consolidate: ['Uniendo recuerdos', 'Recuerdos unidos'],
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
  let restored = false; // the remembered conversation was reopened

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
    const think = document.createElement('details');
    think.className = 'coach-think';
    think.hidden = true;
    const thinkTitle = document.createElement('summary');
    const thought = document.createElement('p');
    think.append(thinkTitle, thought);
    const body = document.createElement('div');
    body.className = 'coach-body';
    const live = document.createElement('p');
    live.className = 'coach-live';
    live.setAttribute('aria-hidden', 'true');
    live.textContent = 'Pensando…';
    div.append(think, steps, body, live);
    log.appendChild(div);
    showEmpty();
    scroll();
    return { el: div, steps, title, list, body, live, think, thinkTitle, thought, thinking: '', thinkStart: 0, thinkEnd: 0, text: '', rows: new Map(), origin: div.dataset.origin };
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

  // Reasoning: open while it streams, closed to «Pensó N s» once the answer starts.
  function closeThinking(t) {
    if (!t.thinkStart || t.thinkEnd) return;
    t.thinkEnd = Date.now();
    const secs = Math.max(1, Math.round((t.thinkEnd - t.thinkStart) / 1000));
    t.thinkTitle.textContent = 'Pensó ' + secs + ' s';
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
    closeThinking(turn);
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
    if (connected) {
      openStream();
      if (conversation && !restored) {
        restored = true;
        openConversation(conversation);
      }
    }
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
      const d = data(ev);
      // A reaction can start the conversation of a new chat.
      if (d.conversation && !conversation) setConversation(d.conversation);
      turn = startTurn(d.origin);
      setRunning(true);
    });
    stream.addEventListener('thinking', (ev) => {
      if (!turn) turn = startTurn('user');
      if (!turn.thinkStart) {
        turn.thinkStart = Date.now();
        turn.think.hidden = false;
        turn.thinkTitle.textContent = 'Pensando…';
      }
      turn.thinking += data(ev).text || '';
      turn.thought.textContent = turn.thinking;
      scroll();
    });
    stream.addEventListener('delta', (ev) => {
      if (!turn) turn = startTurn('user');
      closeThinking(turn);
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
      } else {
        tools(t);
        // coach-apply.js offers the loads of a live answer as Aplicar chips.
        document.dispatchEvent(new CustomEvent('coach:answer', { detail: { el: t.el, text: t.text } }));
      }
      // The engine is up now: the model's name can show without starting one.
      if (!models) loadModels(false);
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
    const body = images.length ? { text: value, images } : { text: value };
    if (conversation) body.conversation = conversation;
    return post('prompt', body).then(({ ok, data }) => {
      if (ok) {
        if (data.conversation) setConversation(data.conversation);
        return;
      }
      if (data.error === 'not_found') {
        // The conversation went (deleted, or past 90 days): start a new one.
        newChat();
        note('Esa conversación ya no existe. Empieza una nueva.', 'error');
        return;
      }
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
      clearChat();
      models = null;
      modelName.textContent = '';
      chip.hidden = true;
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

  // --- conversations ----------------------------------------------------------
  // Like ChatGPT: the drawer lists the person's conversations, newest first; a
  // new chat starts empty and becomes a conversation with its first message.
  // The open one is remembered per device and reopened on load.
  const CONV_KEY = 'coach-conversation';
  const convList = $('conversations');
  const deleteAll = $('conv-delete-all');
  let conversation = parseInt(localStorage.getItem(CONV_KEY) || '0', 10) || 0;

  function setConversation(id) {
    conversation = id || 0;
    localStorage.setItem(CONV_KEY, String(conversation));
    convList.querySelectorAll('.conv-row').forEach((row) => row.classList.toggle('on', Number(row.dataset.id) === conversation));
  }

  function clearChat() {
    log.querySelectorAll('.coach-msg, .coach-note, .run-card, .set-card').forEach((el) => el.remove());
    turn = null;
    lastPrompt = null;
    showEmpty();
  }

  function newChat() {
    if (running) return note('Espera a que termine la respuesta.');
    clearChat();
    setConversation(0);
    post('open', { conversation: 0 });
    text.focus();
  }

  // A stored answer reads as a live one did: steps row, then the text.
  function storedTurn(m) {
    const t = startTurn(m.origin === 'reaction' ? 'reaction' : 'user');
    t.live.remove();
    (m.steps || []).forEach((step) => {
      const [name, outcome] = String(step).split(':');
      const w = words(name);
      const row = document.createElement('li');
      row.dataset.doing = w[0];
      if (outcome === 'error') { row.className = 'error'; row.textContent = w[0] + ': ha fallado'; }
      else if (outcome === 'pending') { row.className = 'waiting'; row.textContent = 'Cambio propuesto, pendiente de tu aprobación'; }
      else row.textContent = w[1];
      t.list.appendChild(row);
    });
    paintSteps(t, false);
    t.text = m.text || '';
    if (t.text) render(t.body, t.text); else t.body.remove();
    tools(t);
  }

  function openConversation(id) {
    if (running) return note('Espera a que termine la respuesta.');
    return fetch(base + '/coach/conversations/' + id, { credentials: 'same-origin' })
      .then((res) => res.json().then((data) => ({ ok: res.ok, data })))
      .then(({ ok, data }) => {
        if (!ok) { if (data.error === 'not_found') newChat(); return; }
        clearChat();
        setConversation(data.id);
        (data.messages || []).forEach((m) => {
          if (m.role === 'user') {
            const me = mine(m.text || '');
            if (m.photos) {
              const small = document.createElement('small');
              small.textContent = '📷 ' + m.photos + (m.photos === 1 ? ' foto (no se guarda)' : ' fotos (no se guardan)');
              me.appendChild(small);
            }
          } else storedTurn(m);
        });
        showEmpty();
        scroll(true);
        post('open', { conversation: data.id });
      })
      .catch(() => {});
  }

  const DAY = 86400000;
  function group(at) {
    const start = new Date(); start.setHours(0, 0, 0, 0);
    const t = new Date(at).getTime();
    if (t >= start.getTime()) return 'Hoy';
    if (t >= start.getTime() - DAY) return 'Ayer';
    if (t >= start.getTime() - 7 * DAY) return 'Últimos 7 días';
    if (t >= start.getTime() - 30 * DAY) return 'Últimos 30 días';
    return 'Anteriores';
  }

  function rowActions(row, c) {
    const open = row.nextElementSibling;
    if (open && open.classList.contains('conv-actions')) { open.remove(); return; }
    const bar = document.createElement('div');
    bar.className = 'conv-actions';
    const rename = document.createElement('button');
    rename.type = 'button'; rename.className = 'ghost'; rename.textContent = 'Renombrar';
    rename.addEventListener('click', () => {
      const title = window.prompt('Nuevo nombre', c.title);
      if (!title || !title.trim()) return;
      post('conversations/' + c.id + '/rename', { title: title.trim() }).then(({ ok, data }) => {
        if (!ok) return note(data.error === 'bad_request' ? 'Ese nombre no vale (1 a 80 caracteres).' : 'No se pudo renombrar.', 'error');
        loadConversations();
      });
    });
    const del = document.createElement('button');
    del.type = 'button'; del.className = 'ghost danger-text'; del.textContent = 'Borrar';
    del.addEventListener('click', () => {
      if (!window.confirm('¿Borrar «' + c.title + '»?')) return;
      post('conversations/' + c.id + '/delete').then(({ ok }) => {
        if (ok && c.id === conversation) newChat();
        loadConversations();
      });
    });
    bar.append(rename, del);
    row.after(bar);
  }

  function loadConversations() {
    if (!connected) { convList.replaceChildren(); deleteAll.hidden = true; return Promise.resolve(); }
    return fetch(base + '/coach/conversations', { credentials: 'same-origin' })
      .then((res) => res.json())
      .then((data) => {
        const list = data.conversations || [];
        deleteAll.hidden = list.length === 0;
        const nodes = [];
        let last = '';
        list.forEach((c) => {
          const g = group(c.updated_at);
          if (g !== last) {
            const h = document.createElement('h3');
            h.textContent = g;
            nodes.push(h);
            last = g;
          }
          const row = document.createElement('div');
          row.className = 'conv-row' + (c.id === conversation ? ' on' : '');
          row.dataset.id = String(c.id);
          const openBtn = document.createElement('button');
          openBtn.type = 'button';
          openBtn.className = 'conv-open';
          openBtn.textContent = c.title;
          openBtn.addEventListener('click', () => {
            document.dispatchEvent(new CustomEvent('drawer:close'));
            openConversation(c.id);
          });
          const more = document.createElement('button');
          more.type = 'button';
          more.className = 'conv-more';
          more.textContent = '⋯';
          more.setAttribute('aria-label', 'Opciones de «' + c.title + '»');
          more.addEventListener('click', () => rowActions(row, c));
          row.append(openBtn, more);
          nodes.push(row);
        });
        convList.replaceChildren(...nodes);
      })
      .catch(() => {});
  }

  document.addEventListener('drawer:open', loadConversations);
  $('chat-new').addEventListener('click', newChat);
  $('drawer-new').addEventListener('click', () => {
    document.dispatchEvent(new CustomEvent('drawer:close'));
    newChat();
  });
  deleteAll.addEventListener('click', () => {
    if (!window.confirm('¿Borrar todas tus conversaciones con el coach? Tus entrenos no se tocan.')) return;
    post('conversations/delete-all').then(() => {
      newChat();
      loadConversations();
    });
  });

  // --- model and thinking ---------------------------------------------------
  // The list is the person's own ChatGPT models, read from their engine; it is
  // asked for when the picker opens or after a turn, never on page load, so an
  // idle page starts no engine.
  const LEVELS = { off: 'No', minimal: 'Mínimo', low: 'Bajo', medium: 'Medio', high: 'Alto', xhigh: 'Muy alto', max: 'Máximo' };
  const sheetEl = $('model-sheet');
  const sheetScrim = $('model-scrim');
  const modelName = $('model-name');
  const chip = $('thinking-chip');
  const modelStatus = $('model-status');
  let models = null;

  function current() {
    return models && models.models.find((m) => m.id === models.model);
  }

  function paintChoice() {
    const m = current();
    if (!m) return;
    modelName.textContent = '· ' + m.name;
    chip.hidden = m.thinking.length < 2;
    chip.querySelector('span').textContent = 'Pensar: ' + (LEVELS[models.thinking] || 'auto');
    const list = $('model-list');
    list.replaceChildren(...models.models.map((x) => {
      const b = document.createElement('button');
      b.type = 'button';
      b.setAttribute('role', 'radio');
      b.setAttribute('aria-checked', String(x.id === models.model));
      b.textContent = x.name;
      b.addEventListener('click', () => choose(x.id, x.thinking.includes(models.thinking) ? models.thinking : x.thinking[Math.floor(x.thinking.length / 2)]));
      return b;
    }));
    const levels = $('thinking-levels');
    levels.replaceChildren(...m.thinking.map((l) => {
      const b = document.createElement('button');
      b.type = 'button';
      b.setAttribute('role', 'radio');
      b.setAttribute('aria-checked', String(l === models.thinking));
      b.textContent = LEVELS[l] || l;
      b.addEventListener('click', () => choose(m.id, l));
      return b;
    }));
  }

  function loadModels(showErrors) {
    if (showErrors) modelStatus.textContent = 'Cargando tus modelos…';
    return fetch(base + '/coach/models', { credentials: 'same-origin' })
      .then((res) => res.json().then((data) => ({ ok: res.ok, data })))
      .then(({ ok, data }) => {
        if (!ok) {
          if (showErrors) modelStatus.textContent = MESSAGES[data.error] || MESSAGES.engine_unavailable;
          return;
        }
        models = data;
        modelStatus.textContent = '';
        paintChoice();
      })
      .catch(() => { if (showErrors) modelStatus.textContent = 'Sin conexión.'; });
  }

  function choose(model, thinking) {
    const before = models && { model: models.model, thinking: models.thinking };
    models.model = model;
    models.thinking = thinking;
    paintChoice();
    post('settings', { model, thinking }).then(({ ok, data }) => {
      if (ok) return;
      Object.assign(models, before);
      paintChoice();
      modelStatus.textContent = MESSAGES[data.error] || 'No se pudo guardar.';
    });
  }

  function setSheet(open) {
    sheetEl.hidden = !open;
    sheetScrim.hidden = !open;
    if (open) loadModels(!models);
  }
  $('model-open').addEventListener('click', () => { if (connected) setSheet(sheetEl.hidden); });
  chip.addEventListener('click', () => setSheet(true));
  $('model-close').addEventListener('click', () => setSheet(false));
  sheetScrim.addEventListener('click', () => setSheet(false));
  document.addEventListener('keydown', (ev) => { if (ev.key === 'Escape' && !sheetEl.hidden) setSheet(false); });

  refresh();
})();
