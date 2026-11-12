// The exercise picker: a sheet with search, recent exercises and muscle-group
// filters. «+ Añadir ejercicio» picks several and adds them to today's plan;
// «Sustituir» picks one and swaps it in. Both post the same /plan/item actions
// the plain forms post, so the server's rules stay the only rules.
(function () {
  'use strict';

  const body = document.body;
  const base = body.dataset.base || '';
  const $ = (id) => document.getElementById(id);
  const sheet = $('picker');
  if (!sheet) return;
  const scrim = $('picker-scrim');
  const title = $('picker-title');
  const search = $('picker-q');
  const chips = $('picker-chips');
  const list = $('picker-list');
  const done = $('picker-done');

  let data = { groups: [], recent: [], exercises: [] };
  let mode = 'add';
  let from = '';
  let filter = 'recent';
  let chosen = [];

  // Search ignores case and accents: «triceps» finds «tríceps».
  const fold = (s) => s.normalize('NFD').replace(/[̀-ͯ]/g, '').toLowerCase().trim();
  const label = (s) => s.charAt(0).toUpperCase() + s.slice(1);

  function visible() {
    const q = fold(search.value);
    if (q) return data.exercises.filter((e) => fold(e.name).includes(q) || fold(e.group || '').includes(q));
    if (filter === 'recent') {
      const byName = new Map(data.exercises.map((e) => [e.name, e]));
      const recent = data.recent.map((n) => byName.get(n)).filter(Boolean);
      return recent.length ? recent : data.exercises;
    }
    if (filter === 'all') return data.exercises;
    return data.exercises.filter((e) => e.group === filter);
  }

  function chip(value, text) {
    const b = document.createElement('button');
    b.type = 'button';
    b.className = 'chip';
    b.dataset.filter = value;
    b.textContent = text;
    b.setAttribute('aria-pressed', String(filter === value && !search.value.trim()));
    return b;
  }

  function paintChips() {
    chips.replaceChildren(chip('recent', 'Recientes'), chip('all', 'Todos'), ...data.groups.map((g) => chip(g, label(g))));
  }

  function row(name, meta, create) {
    const li = document.createElement('li');
    const b = document.createElement('button');
    b.type = 'button';
    b.className = 'pick' + (create ? ' create' : '');
    b.dataset.name = name;
    const on = chosen.includes(name);
    b.setAttribute('aria-pressed', String(on));
    const main = document.createElement('span');
    main.className = 'row-main';
    const strong = document.createElement('strong');
    strong.textContent = create ? 'Crear «' + name + '»' : name;
    main.appendChild(strong);
    if (meta) {
      const m = document.createElement('span');
      m.className = 'muted num';
      m.textContent = meta;
      main.appendChild(m);
    }
    const box = document.createElement('span');
    box.className = 'tick';
    box.setAttribute('aria-hidden', 'true');
    b.append(main, box);
    li.appendChild(b);
    return li;
  }

  function paintList() {
    const items = visible().filter((e) => e.name !== from);
    const rows = items.map((e) => {
      const meta = [e.group ? label(e.group) : '', e.last ? 'última: ' + e.last : ''].filter(Boolean).join(' · ');
      return row(e.name, meta, false);
    });
    // Nothing matches: the text itself becomes a new exercise.
    const typed = search.value.trim().toLowerCase();
    if (typed && !data.exercises.some((e) => e.name === typed)) rows.push(row(typed, 'Ejercicio nuevo', true));
    if (!rows.length) {
      const li = document.createElement('li');
      li.className = 'muted picker-empty';
      li.textContent = 'Aún no has registrado ejercicios. Escribe uno para crearlo.';
      rows.push(li);
    }
    list.replaceChildren(...rows);
  }

  function paintDone() {
    done.hidden = mode === 'swap';
    done.disabled = chosen.length === 0;
    done.textContent = chosen.length ? 'Añadir ' + chosen.length : 'Añadir';
  }

  function paint() {
    paintChips();
    paintList();
    paintDone();
  }

  function setOpen(open) {
    sheet.hidden = !open;
    if (scrim) scrim.hidden = !open;
    body.classList.toggle('picker-open', open);
  }

  async function open(options) {
    mode = options.mode === 'swap' ? 'swap' : 'add';
    from = mode === 'swap' ? options.exercise || '' : '';
    chosen = [];
    filter = 'recent';
    search.value = '';
    title.textContent = mode === 'swap' ? 'Sustituir ' + from : 'Añadir ejercicio';
    paint();
    setOpen(true);
    try {
      const res = await fetch(base + '/exercises', { credentials: 'same-origin' });
      if (res.ok) data = await res.json();
    } catch (e) { /* offline: the typed name still works */ }
    paint();
  }

  function close() { setOpen(false); }

  // Each pick is the same request the plain form sends; one after another so
  // the panel ends showing all of them.
  async function submit(names) {
    if (!window.htmx || !names.length) return;
    close();
    for (const name of names) {
      const values = mode === 'swap'
        ? { action: 'swap', exercise: from, replacement: name }
        : { action: 'add', exercise: name, target_sets: '3' };
      await window.htmx.ajax('POST', base + '/plan/item', { target: '#panel', swap: 'outerHTML', values });
    }
    if (navigator.vibrate) navigator.vibrate(8);
  }

  window.openExercisePicker = open;

  search.addEventListener('input', () => { paintChips(); paintList(); });
  chips.addEventListener('click', (event) => {
    const b = event.target.closest('[data-filter]');
    if (!b) return;
    filter = b.dataset.filter;
    search.value = '';
    paint();
  });
  list.addEventListener('click', (event) => {
    const b = event.target.closest('.pick');
    if (!b) return;
    const name = b.dataset.name;
    if (mode === 'swap') {
      submit([name]);
      return;
    }
    chosen = chosen.includes(name) ? chosen.filter((n) => n !== name) : chosen.concat(name);
    b.setAttribute('aria-pressed', String(chosen.includes(name)));
    if (b.classList.contains('create') && chosen.includes(name)) {
      // A created name joins the list so it stays ticked while searching on.
      data.exercises = data.exercises.concat({ name });
    }
    paintDone();
  });
  done.addEventListener('click', () => submit(chosen.slice()));
  sheet.addEventListener('click', (event) => { if (event.target.closest('[data-picker-close]')) close(); });
  if (scrim) scrim.addEventListener('click', close);
  document.addEventListener('keydown', (event) => {
    if (event.key === 'Escape' && !sheet.hidden) {
      event.stopPropagation();
      close();
    }
  }, true);

  // «+ Añadir ejercicio» in the workout opens the picker instead of the form;
  // the form stays for a page without JavaScript.
  document.addEventListener('click', (event) => {
    const summary = event.target.closest('#panel details.add-item > summary');
    if (!summary) return;
    event.preventDefault();
    open({ mode: 'add' });
  });
})();
