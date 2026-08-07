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
    if (navigator.vibrate) navigator.vibrate(8);
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
    if (navigator.vibrate) navigator.vibrate(8);
  });

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

  // Mark the newest row so a logged set is visible without reading it.
  function flashNewest() {
    const items = document.querySelectorAll('#panel .sets li');
    if (items.length) items[items.length - 1].classList.add('new');
  }

  document.body.addEventListener('htmx:afterSwap', (event) => {
    if (event.target.id !== 'panel') return;
    syncRepeat();
    flashNewest();
    if (navigator.vibrate) navigator.vibrate(18);
  });

  if (form) {
    // Keep focus out of the exercise field after submitting so the keyboard
    // does not reopen between sets.
    form.addEventListener('submit', () => {
      if (document.activeElement && document.activeElement.blur) document.activeElement.blur();
    });
  }

  syncRepeat();

  if ('serviceWorker' in navigator) {
    window.addEventListener('load', () => {
      navigator.serviceWorker.register(base + '/sw.js', { scope: base + '/' }).catch(() => {});
    });
  }
})();
