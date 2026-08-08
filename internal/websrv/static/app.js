// Entry-speed helpers. Everything here is progressive enhancement: the form
// posts and works with JavaScript disabled, this just removes taps.
(function () {
  'use strict';

  const base = document.body.dataset.base || '';
  const form = document.getElementById('entry');
  const exercise = document.getElementById('exercise');
  const weight = document.getElementById('weight');
  const reps = document.getElementById('reps');
  const repeat = document.getElementById('repeat');
  const previous = document.getElementById('previous');

  let info = {};
  const infoTag = document.getElementById('exercise-info');
  if (infoTag) {
    try { info = JSON.parse(infoTag.textContent) || {}; } catch (e) { info = {}; }
  }

  const buzz = (ms) => { if (navigator.vibrate) navigator.vibrate(ms); };
  const num = (v) => Math.round(parseFloat(v) * 100) / 100;

  function setRPE(value) {
    const el = document.getElementById('rpe-' + value);
    if (el) el.checked = true;
  }

  function fill(data) {
    if (!data || !data.exercise) return;
    exercise.value = data.exercise;
    if (data.weight) weight.value = data.weight;
    if (data.reps) reps.value = data.reps;
    if (data.rpe) setRPE(data.rpe);
    showPrevious();
  }

  // Shows what was done last time for the exercise being entered, plus the
  // standing record. This is the number you are trying to beat.
  function showPrevious() {
    if (!previous) return;
    const entry = info[(exercise.value || '').trim().toLowerCase()];
    if (!entry) { previous.hidden = true; return; }
    const bits = [];
    if (entry.last) {
      bits.push(`Anterior: <strong>${num(entry.last.weight_kg)} kg × ${entry.last.reps} @${num(entry.last.rpe)}</strong> (${entry.last.date})`);
    }
    if (entry.best_e1rm) bits.push(`Récord 1RM est. <strong>${entry.best_e1rm} kg</strong>`);
    if (entry.group) bits.push(entry.group);
    let html = bits.join(' · ');
    // The setup note goes on its own line: it is an instruction, not a stat.
    if (entry.note) html += `<span class="note-line">🛠 ${entry.note}</span>`;
    if (!html) { previous.hidden = true; return; }
    previous.innerHTML = html;
    previous.hidden = false;
    const noteInput = document.getElementById('note');
    if (noteInput) noteInput.value = entry.note || '';
  }

  // Saving a setup note is deliberately its own action: it belongs to the
  // exercise for good, not to the set being logged.
  const saveNote = document.getElementById('save-note');
  if (saveNote) {
    saveNote.addEventListener('click', () => {
      const name = (exercise.value || '').trim();
      if (!name || !window.htmx) return;
      window.htmx.ajax('POST', base + '/note', {
        target: '#panel', swap: 'outerHTML',
        values: { exercise: name, note: document.getElementById('note').value }
      });
      buzz(8);
    });
  }

  // Within a superset you move straight to the next exercise of the round, so
  // the next one is pre-loaded and the rest timer is held back.
  function nextInSuperset(justLogged) {
    const items = Array.from(document.querySelectorAll('.planned li'));
    const current = items.find((li) => li.dataset.exercise === justLogged);
    if (!current || !current.dataset.superset) return null;
    const round = items.filter((li) => li.dataset.superset === current.dataset.superset);
    const from = round.indexOf(current);
    for (let i = 1; i <= round.length; i++) {
      const candidate = round[(from + i) % round.length];
      if (candidate !== current && !candidate.classList.contains('ok') && !candidate.classList.contains('skipped')) {
        return candidate;
      }
    }
    return null;
  }

  // Steppers. Weight moves in 2.5 kg jumps by default because that is the
  // smallest plate pair on most racks; reps move by 1.
  document.addEventListener('click', (event) => {
    const pad = event.target.closest('.pad');
    if (!pad) return;
    const stepper = pad.closest('.stepper');
    const input = document.getElementById(stepper.dataset.target);
    const step = parseFloat(stepper.dataset.step);
    const min = parseFloat(stepper.dataset.min);
    const delta = parseFloat(pad.dataset.delta) * step;
    const next = Math.round(((parseFloat(input.value) || 0) + delta) * 100) / 100;
    input.value = next < min ? min : next;
    buzz(8);
  });

  // Quick-pick chips restore the whole last set for that exercise, which is the
  // common case: same movement, same load, next set.
  document.addEventListener('click', (event) => {
    const chip = event.target.closest('.chip');
    if (!chip) return;
    fill({
      exercise: chip.dataset.exercise,
      weight: chip.dataset.weight,
      reps: chip.dataset.reps,
      rpe: chip.dataset.rpe
    });
    buzz(8);
  });

  if (exercise) exercise.addEventListener('input', showPrevious);

  // The "⋯" opens per-exercise adjustments. Kept behind a tap so the checklist
  // stays readable at a glance mid-set.
  document.addEventListener('click', (event) => {
    const more = event.target.closest('.more');
    if (!more) return;
    event.stopPropagation();
    const box = more.parentElement.querySelector('.item-actions');
    document.querySelectorAll('.item-actions').forEach((el) => { if (el !== box) el.hidden = true; });
    box.hidden = !box.hidden;
    buzz(8);
  });

  // Substitution asks for the replacement, then posts it like any other action.
  document.addEventListener('click', (event) => {
    const btn = event.target.closest('.swap');
    if (!btn) return;
    event.stopPropagation();
    const replacement = window.prompt('¿Por qué ejercicio lo sustituyes?', '');
    if (!replacement || !replacement.trim()) return;
    if (!window.htmx) return;
    window.htmx.ajax('POST', base + '/plan/item', {
      target: '#panel',
      swap: 'outerHTML',
      values: { action: 'swap', exercise: btn.dataset.exercise, replacement: replacement.trim() }
    });
  });

  // Tapping a planned exercise loads its prescription: the exercise, the middle
  // of the target rep range and the target RPE, plus last time's weight, which
  // the plan deliberately does not prescribe.
  document.addEventListener('click', (event) => {
    const item = event.target.closest('.planned li');
    if (!item) return;
    if (event.target.closest('.more, .item-actions')) return;
    const name = item.dataset.exercise;
    const known = info[name] || {};
    fill({
      exercise: name,
      weight: known.last ? known.last.weight_kg : null,
      reps: item.dataset.reps !== '0' ? item.dataset.reps : null,
      rpe: item.dataset.rpe !== '0' ? item.dataset.rpe : null
    });
    buzz(8);
    window.scrollTo({ top: document.getElementById('entry').offsetTop - 60, behavior: 'smooth' });
  });

  // --- inline edit -------------------------------------------------------
  // Turns a logged set into an editable row so a mistyped weight does not need
  // deleting and re-entering.
  document.addEventListener('click', (event) => {
    const btn = event.target.closest('.edit');
    if (!btn) return;
    const li = btn.closest('li');
    if (li.querySelector('form')) return;
    const f = document.createElement('form');
    f.className = 'edit-form wide-edit';
    f.setAttribute('hx-post', `${base}/sets/${li.dataset.setId}/update`);
    f.setAttribute('hx-target', '#panel');
    f.setAttribute('hx-swap', 'outerHTML');
    f.innerHTML =
      `<input name="weight_kg" type="number" step="0.5" min="0.5" inputmode="decimal" value="${li.dataset.weight}" aria-label="Peso">` +
      `<input name="reps" type="number" step="1" min="1" inputmode="numeric" value="${li.dataset.reps}" aria-label="Reps">` +
      `<input name="rpe" type="number" step="0.5" min="1" max="10" inputmode="decimal" value="${li.dataset.rpe}" aria-label="RPE">` +
      `<input name="technique" type="text" list="techniques" placeholder="técnica" value="${li.dataset.technique || ''}" aria-label="Técnica">` +
      `<button type="submit" class="ok">Guardar</button>`;
    li.appendChild(f);
    if (window.htmx) window.htmx.process(f);
    f.querySelector('input').focus();
  });

  // --- rest timer --------------------------------------------------------
  const restBar = document.getElementById('rest');
  const restTime = document.getElementById('rest-time');
  const REST_KEY = 'rest-seconds';
  let restDefault = parseInt(localStorage.getItem(REST_KEY) || '120', 10);
  let remaining = 0;
  let ticker = null;

  function paint() {
    if (!restTime) return;
    const m = Math.floor(Math.abs(remaining) / 60);
    const s = Math.abs(remaining) % 60;
    restTime.textContent = (remaining < 0 ? '-' : '') + m + ':' + String(s).padStart(2, '0');
    restBar.classList.toggle('done', remaining <= 0);
  }

  function stopRest() {
    clearInterval(ticker);
    ticker = null;
    restBar.hidden = true;
  }

  function startRest() {
    if (!restBar) return;
    remaining = restDefault;
    restBar.hidden = false;
    paint();
    clearInterval(ticker);
    ticker = setInterval(() => {
      remaining -= 1;
      paint();
      if (remaining === 0) buzz([120, 80, 120]);
      if (remaining <= -30) stopRest();
    }, 1000);
  }

  if (restBar) {
    restBar.addEventListener('click', (event) => {
      const adj = event.target.closest('.rest-adj');
      if (adj) {
        const delta = parseInt(adj.dataset.delta, 10);
        // Adjusting mid-rest also becomes the new default for next time.
        restDefault = Math.max(30, restDefault + delta);
        localStorage.setItem(REST_KEY, String(restDefault));
        remaining = Math.max(0, remaining + delta);
        paint();
        buzz(8);
        return;
      }
      stopRest();
    });
  }

  // "Repeat last" mirrors the final set of the current session.
  function syncRepeat() {
    const panel = document.getElementById('panel');
    if (!panel || !repeat) return;
    const has = Boolean(panel.dataset.lastExercise);
    repeat.hidden = !has;
    repeat.onclick = has
      ? () => fill({
          exercise: panel.dataset.lastExercise,
          weight: panel.dataset.lastWeight,
          reps: panel.dataset.lastReps,
          rpe: panel.dataset.lastRpe
        })
      : null;
  }

  document.body.addEventListener('htmx:afterSwap', (event) => {
    if (event.target.id !== 'panel') return;
    syncRepeat();
    const failed = document.querySelector('#panel .alert');
    if (failed) { buzz(18); return; }
    const next = nextInSuperset((exercise.value || '').trim().toLowerCase());
    if (next) {
      // Same round: no rest, load the next movement instead.
      const known = info[next.dataset.exercise] || {};
      fill({
        exercise: next.dataset.exercise,
        weight: known.last ? known.last.weight_kg : null,
        reps: next.dataset.reps !== '0' ? next.dataset.reps : null,
        rpe: next.dataset.rpe !== '0' ? next.dataset.rpe : null
      });
    } else {
      startRest();
    }
    buzz(18);
  });

  if (form) {
    // Keep focus out of the exercise field after submitting so the keyboard
    // does not reopen between sets.
    form.addEventListener('submit', () => {
      if (document.activeElement && document.activeElement.blur) document.activeElement.blur();
    });
  }

  syncRepeat();
  showPrevious();

  if ('serviceWorker' in navigator) {
    window.addEventListener('load', () => {
      navigator.serviceWorker.register(base + '/sw.js', { scope: base + '/' }).catch(() => {});
    });
  }
})();
