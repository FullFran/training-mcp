// The coach in Entrenar: when an answer spells out a load ("45 kg × 8"), an
// Aplicar chip writes it into the set row's inputs. It never logs: the ✓ in
// the row stays the only way to record a set. Also the quick prompts of the
// sheet and the draft that survives closing it.
(function (root) {
  'use strict';

  // --- reading an answer (pure, tested under node) --------------------------
  const NUM = '(\\d{1,3}(?:[.,]\\d{1,2})?)';
  // A weight in kg not glued to a bigger number ("3.200 kg" is a total).
  const WEIGHT = new RegExp('(?<![\\d.,])' + NUM + '\\s*kg\\b', 'gi');
  // Reps right after the weight: "× 8", "x8", "a 8 reps", "por 8 repeticiones".
  const REPS = /^\s*(?:[×xX*]\s*(\d{1,2})(?!\d)|(?:a|por|para|de)\s+(\d{1,2})\s*(?:reps?|repeticiones)\b)/;
  const RPE = /(?:@\s*|\bRPE\s*)(\d{1,2}(?:[.,]5)?)(?![\d])/i;
  const RIR = /\bRIR\s*(\d)(?!\d)/i;
  const SET_N = /\bserie\s+(\d{1,2})\b|\b(\d{1,2})\s*(?:ª|\.ª)/i;
  // Clauses about what was done, totals or records are not instructions.
  const PAST = /\b(hiciste|has hecho|hizo|levantaste|registraste|llevas|llevaste|fue|era|anterior|pasada|ayer|r[ée]cord|PR|e?1RM|volumen|total)\b/i;

  const num = (v) => parseFloat(String(v).replace(',', '.'));

  // coachSuggestions returns the loads an answer proposes: {exercise, n,
  // weight, reps, rpe}, with "" or 0 when the answer does not say. At most 3.
  function coachSuggestions(text, exercises) {
    const names = (exercises || []).slice().sort((a, b) => b.length - a.length);
    const out = [];
    const seen = new Set();
    const plain = String(text || '').replace(/\*\*|`/g, '');
    for (const sentence of plain.split(/[.!?;\n]+(?=\s|$)/)) {
      const lower = sentence.toLowerCase();
      const exercise = names.find((n) => lower.includes(n.toLowerCase())) || '';
      const setMatch = sentence.match(SET_N);
      const n = setMatch ? parseInt(setMatch[1] || setMatch[2], 10) : 0;
      const weights = Array.from(sentence.matchAll(WEIGHT));
      weights.forEach((m, i) => {
        const clauseStart = Math.max(0, sentence.lastIndexOf(',', m.index) + 1, sentence.lastIndexOf(':', m.index) + 1);
        if (PAST.test(sentence.slice(clauseStart, m.index))) return;
        const after = sentence.slice(m.index + m[0].length);
        const reps = after.match(REPS);
        const rest = sentence.slice(m.index + m[0].length, i + 1 < weights.length ? weights[i + 1].index : undefined);
        const rpe = rest.match(RPE);
        const rir = rest.match(RIR);
        const s = {
          exercise,
          n,
          weight: num(m[1]),
          reps: reps ? parseInt(reps[1] || reps[2], 10) : 0,
          rpe: rpe ? num(rpe[1]) : rir ? 10 - parseInt(rir[1], 10) : 0,
        };
        if (!(s.weight > 0) || (s.rpe && (s.rpe < 1 || s.rpe > 10))) return;
        const key = [s.exercise, s.n, s.weight, s.reps, s.rpe].join('|');
        if (seen.has(key) || out.length >= 3) return;
        seen.add(key);
        out.push(s);
      });
    }
    return out;
  }
  root.coachSuggestions = coachSuggestions;

  if (typeof document === 'undefined' || !document.getElementById) return;

  // --- the set table --------------------------------------------------------
  const $ = (id) => document.getElementById(id);
  const cards = () => Array.from(document.querySelectorAll('#train li.ex-card[data-exercise]'));
  const fmt = (v) => String(v).replace('.', ',');

  // The card a suggestion is for: the one it names, else the one open, else
  // the first with sets to do.
  function cardFor(s) {
    const all = cards();
    return all.find((c) => s.exercise && c.dataset.exercise === s.exercise)
      || all.find((c) => c.querySelector('details[open]'))
      || all.find((c) => c.querySelector('li.set-row.pending'));
  }

  // The row to fill: the set it names if still to do, else the next one.
  function rowFor(card, s) {
    const rows = Array.from(card.querySelectorAll('li.set-row.pending, li.set-row.added'));
    const named = s.n && rows.find((r) => {
      const t = r.querySelector('.type-toggle');
      return t && Number(t.dataset.n) === s.n;
    });
    return named || rows[0] || null;
  }

  function setNumber(row) {
    const t = row && row.querySelector('.type-toggle');
    return t ? Number(t.dataset.n) : 0;
  }

  function label(s) {
    const card = cardFor(s);
    if (!card) return '';
    const row = rowFor(card, s);
    const open = card.querySelector('details[open]');
    const where = (open ? '' : card.dataset.exercise + ', ') + (row ? 'serie ' + (setNumber(row) || s.n) : 'nueva serie');
    return 'Aplicar a ' + where + ': ' + fmt(s.weight) + (s.reps ? ' × ' + s.reps : ' kg') + (s.rpe ? ' @' + fmt(s.rpe) : '');
  }

  function write(input, value) {
    if (!input || !value) return;
    input.value = String(value);
    input.dispatchEvent(new Event('input', { bubbles: true }));
    input.dispatchEvent(new Event('change', { bubbles: true }));
  }

  // apply fills the row and shows it; the person still taps ✓.
  function apply(s) {
    const card = cardFor(s);
    if (!card) return false;
    let row = rowFor(card, s);
    if (!row) {
      const add = card.querySelector('.add-row');
      if (add) add.click();
      row = rowFor(card, s);
    }
    if (!row) return false;
    write(row.querySelector('input[name="weight_kg"]'), s.weight);
    write(row.querySelector('input[name="reps"]'), s.reps);
    write(row.querySelector('input[name="rpe"]'), s.rpe);
    document.dispatchEvent(new CustomEvent('workout:focus', { detail: card.dataset.exercise }));
    document.dispatchEvent(new CustomEvent('coach:close'));
    row.classList.remove('applied');
    void row.offsetWidth;
    row.classList.add('applied');
    setTimeout(() => row.scrollIntoView({ block: 'center', behavior: 'smooth' }), 250);
    return true;
  }

  // Chips under a live answer, only while a workout is open.
  document.addEventListener('coach:answer', (event) => {
    const { el, text } = event.detail || {};
    const panel = $('panel');
    if (!el || !panel || panel.dataset.active !== '1') return;
    const names = cards().map((c) => c.dataset.exercise);
    const found = coachSuggestions(text, names).filter((s) => label(s));
    if (!found.length) return;
    const bar = document.createElement('div');
    bar.className = 'apply-chips';
    found.forEach((s) => {
      const b = document.createElement('button');
      b.type = 'button';
      b.className = 'apply-chip';
      b.textContent = label(s);
      b.addEventListener('click', () => {
        if (!apply(s)) return;
        b.classList.add('done');
        b.textContent = '✓ ' + b.textContent.replace(/^Aplicar a /, 'Puesto en ');
      });
      bar.appendChild(b);
    });
    const tools = el.querySelector('.coach-tools');
    el.insertBefore(bar, tools || null);
  });

  // --- quick prompts --------------------------------------------------------
  const text = $('coach-text');
  const form = $('coach-form');
  function openExercise() {
    const card = cards().find((c) => c.querySelector('details[open]'));
    return card ? card.dataset.exercise : '';
  }
  const QUICK = {
    swap: (ex) => ex ? '¿Por qué ejercicio sustituyo ' + ex + ' hoy?' : '¿Por qué ejercicio sustituyo el de ahora?',
    pain: (ex) => 'Me duele ' + (ex ? '… haciendo ' + ex : ''),
    add: (ex) => ex ? '¿Añado una serie más de ' + ex + '?' : '¿Añado una serie más?',
  };
  document.addEventListener('click', (event) => {
    const chip = event.target.closest('[data-quick]');
    if (!chip || !text || !form || form.hidden) return;
    const kind = chip.dataset.quick;
    text.value = QUICK[kind](openExercise());
    text.dispatchEvent(new Event('input', { bubbles: true }));
    if (kind === 'pain') {
      // Where it hurts is the person's to say.
      text.focus();
      const at = 'Me duele '.length;
      text.setSelectionRange(at, text.value.indexOf('…') >= 0 ? at + 1 : at);
    } else if (form.requestSubmit) form.requestSubmit();
  });

  // --- the draft survives closing the sheet and a reload --------------------
  const DRAFT_KEY = 'coach-draft';
  if (text) {
    if (!text.value) {
      const saved = localStorage.getItem(DRAFT_KEY);
      if (saved) {
        text.value = saved;
        text.dispatchEvent(new Event('input', { bubbles: true }));
      }
    }
    const save = () => {
      if (text.value.trim()) localStorage.setItem(DRAFT_KEY, text.value);
      else localStorage.removeItem(DRAFT_KEY);
    };
    text.addEventListener('input', save);
    // Sending empties the box; coach.js does it without an input event.
    if (form) form.addEventListener('submit', () => setTimeout(save, 0));
  }
})(typeof window !== 'undefined' ? window : globalThis);
