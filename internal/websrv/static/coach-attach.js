// Photos for the coach. Mounts a photo button into the composer's
// #composer-extras slot. Every photo is redrawn on a canvas before it leaves the
// device: that drops its EXIF data (location included) and scales it to at most
// MAX_EDGE px as a JPEG. The photos travel with the next message only; nothing
// keeps them. coach.js asks for them with window.coachAttach.take().
(function () {
  'use strict';

  const slot = document.getElementById('composer-extras');
  if (!slot) return;

  // The server's limits are in internal/engine/images.go; these stay below them.
  const MAX_PHOTOS = 4;
  const MAX_EDGE = 1600;
  const MAX_BYTES = 2 * 1024 * 1024;
  const QUALITIES = [0.85, 0.7, 0.5];

  let photos = []; // { type, data (base64), url (data: URL for the thumbnail) }
  let preparing = 0;


  const input = document.createElement('input');
  input.type = 'file';
  input.accept = 'image/*'; // on a phone: camera or gallery
  input.multiple = true;
  input.hidden = true;

  const button = document.createElement('button');
  button.type = 'button';
  button.id = 'coach-photo';
  button.className = 'ghost';
  button.style.cssText = 'min-height: var(--tap); min-width: var(--tap); font-size: 20px; padding: 0 10px;';
  button.textContent = '📷';
  button.setAttribute('aria-label', 'Añadir fotos para el coach');

  // The button sits in the slot; the thumbnails and notes take a full row
  // above the message box, shown only when there is something in it.
  const row = document.createElement('div');
  row.style.cssText = 'flex-basis: 100%; display: flex; flex-wrap: wrap; gap: 6px; align-items: center;';
  row.hidden = true;
  const tray = document.createElement('div');
  tray.id = 'coach-photos';
  tray.style.cssText = 'display: flex; gap: 6px; flex-wrap: wrap;';
  const status = document.createElement('small');
  status.className = 'muted';
  status.setAttribute('aria-live', 'polite');
  row.append(tray, status);

  slot.append(button, input);
  slot.parentNode.insertBefore(row, slot);

  function showRow() { row.hidden = photos.length === 0 && preparing === 0 && !status.textContent; }

  function say(message) { status.textContent = message || ''; showRow(); }

  function loadImage(file) {
    if (window.createImageBitmap) {
      // from-image applies the EXIF orientation to the pixels before EXIF is dropped.
      return createImageBitmap(file, { imageOrientation: 'from-image' }).catch(() => fallback(file));
    }
    return fallback(file);
  }

  function fallback(file) {
    return new Promise((resolve, reject) => {
      const url = URL.createObjectURL(file);
      const img = new Image();
      img.onload = () => { URL.revokeObjectURL(url); resolve(img); };
      img.onerror = () => { URL.revokeObjectURL(url); reject(new Error('unreadable')); };
      img.src = url;
    });
  }

  function toBlob(canvas, quality) {
    return new Promise((resolve) => canvas.toBlob(resolve, 'image/jpeg', quality));
  }

  function dataURL(blob) {
    return new Promise((resolve, reject) => {
      const reader = new FileReader();
      reader.onload = () => resolve(reader.result);
      reader.onerror = reject;
      reader.readAsDataURL(blob);
    });
  }

  // prepare redraws one photo as a clean, bounded JPEG.
  async function prepare(file) {
    const img = await loadImage(file);
    const w = img.width || img.naturalWidth;
    const h = img.height || img.naturalHeight;
    if (!w || !h) throw new Error('unreadable');
    const scale = Math.min(1, MAX_EDGE / Math.max(w, h));
    const canvas = document.createElement('canvas');
    canvas.width = Math.max(1, Math.round(w * scale));
    canvas.height = Math.max(1, Math.round(h * scale));
    canvas.getContext('2d').drawImage(img, 0, 0, canvas.width, canvas.height);
    if (img.close) img.close();
    for (const q of QUALITIES) {
      const blob = await toBlob(canvas, q);
      if (blob && blob.size <= MAX_BYTES) {
        const url = await dataURL(blob);
        return { type: 'image/jpeg', data: url.slice(url.indexOf(',') + 1), url };
      }
    }
    throw new Error('too large');
  }

  function render() {
    tray.textContent = '';
    photos.forEach((p, i) => {
      const box = document.createElement('span');
      box.style.cssText = 'position: relative; display: inline-block;';
      const img = document.createElement('img');
      img.src = p.url;
      img.alt = 'Foto ' + (i + 1);
      img.style.cssText = 'width: 56px; height: 56px; object-fit: cover; border-radius: 8px; display: block;';
      const remove = document.createElement('button');
      remove.type = 'button';
      remove.className = 'ghost';
      remove.textContent = '×';
      remove.setAttribute('aria-label', 'Quitar foto ' + (i + 1));
      remove.style.cssText = 'position: absolute; top: -6px; right: -6px; min-height: 0; padding: 0 6px; line-height: 1.4; border-radius: 999px; background: var(--bg);';
      remove.addEventListener('click', () => { photos.splice(i, 1); render(); });
      box.append(img, remove);
      tray.appendChild(box);
    });
    button.disabled = preparing > 0 || photos.length >= MAX_PHOTOS;
    showRow();
  }

  button.addEventListener('click', () => input.click());

  input.addEventListener('change', () => {
    const files = Array.from(input.files || []);
    input.value = '';
    const room = MAX_PHOTOS - photos.length - preparing;
    if (files.length > room) say('Máximo ' + MAX_PHOTOS + ' fotos por mensaje.');
    else say('');
    files.slice(0, Math.max(0, room)).forEach((file) => {
      preparing++;
      render();
      prepare(file)
        .then((p) => { photos.push(p); })
        .catch(() => say('No se pudo usar una de las fotos.'))
        .finally(() => { preparing--; render(); });
    });
  });

  window.coachAttach = {
    // busy is true while a photo is still being prepared.
    busy: () => preparing > 0,
    count: () => photos.length,
    // take hands the photos to the message being sent and empties the tray.
    take() {
      const out = photos.map((p) => ({ type: p.type, data: p.data }));
      photos = [];
      say('');
      render();
      return out;
    },
    // restore puts photos back after a refused send.
    restore(list) {
      photos = list.map((p) => ({ type: p.type, data: p.data, url: 'data:' + p.type + ';base64,' + p.data })).concat(photos).slice(0, MAX_PHOTOS);
      render();
    },
    // show adds thumbnails of sent photos to a chat bubble.
    show(container, list) {
      const row = document.createElement('div');
      row.style.cssText = 'display: flex; gap: 6px; flex-wrap: wrap; margin-top: 6px;';
      list.forEach((p, i) => {
        const img = document.createElement('img');
        img.src = 'data:' + p.type + ';base64,' + p.data;
        img.alt = 'Foto enviada ' + (i + 1);
        img.style.cssText = 'max-width: 120px; max-height: 120px; border-radius: 8px; display: block;';
        row.appendChild(img);
      });
      container.appendChild(row);
    },
  };

  render();
})();
