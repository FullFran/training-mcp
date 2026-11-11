// Connectors panel: connect a popular server from the host's catalog with one
// tap, or add any other remote MCP server, connect it (signing in at its
// provider when it uses OAuth), see its tools, disconnect, remove, and take
// back this session's "always" answers. Text from servers is always set as
// text, never as markup.
(function () {
  'use strict';
  const base = document.body.dataset.base || '';
  const csrf = document.querySelector('meta[name="csrf-token"]');
  const list = document.getElementById('cx-list');
  const grants = document.getElementById('cx-grants');
  const empty = document.getElementById('cx-empty');
  const flash = document.getElementById('cx-flash');
  const catalog = document.getElementById('cx-catalog');

  const MESSAGES = {
    bad_url: 'La URL debe empezar por https:// y no llevar usuario ni contraseña.',
    blocked_address: 'Esa dirección no está permitida (red privada, local o de metadatos).',
    bad_label: 'Ese nombre no sirve: usa letras o números.',
    bad_token: 'El servidor no aceptó ese token.',
    exists: 'Ya tienes un conector con ese nombre.',
    too_many: 'Has llegado al máximo de conectores.',
    not_found: 'Ese conector ya no existe.',
    oauth_unavailable: 'Este servidor pide iniciar sesión y aquí no se puede: usa un token fijo.',
    discovery_failed: 'No se encontró cómo iniciar sesión en ese servidor.',
    registration_failed: 'El servidor no permitió registrar la app.',
    token_failed: 'El inicio de sesión no se completó. Conéctalo otra vez.',
    unauthorized: 'El servidor ya no acepta la sesión. Conéctalo otra vez.',
    unreachable: 'No se pudo contactar con el servidor.',
    state_invalid: 'El inicio de sesión caducó o no era tuyo. Vuelve a intentarlo.',
    denied: 'Cancelaste el inicio de sesión.',
    vault: 'No se pudo guardar la credencial.',
    catalog: 'El catálogo no está disponible ahora mismo.',
  };
  const say = (code) => MESSAGES[code] || 'Algo falló.';

  function show(text) {
    flash.textContent = text;
    flash.hidden = !text;
  }

  async function call(method, path, body) {
    const headers = { 'X-Coach': '1' };
    if (csrf) headers['X-CSRF-Token'] = csrf.content;
    if (body !== undefined) headers['Content-Type'] = 'application/json';
    const res = await fetch(base + path, { method, headers, body: body === undefined ? undefined : JSON.stringify(body), credentials: 'same-origin' });
    let data = {};
    try { data = await res.json(); } catch (_) { /* empty body */ }
    if (!res.ok) throw new Error(data.error || 'unreachable');
    return data;
  }

  function el(tag, cls, text) {
    const node = document.createElement(tag);
    if (cls) node.className = cls;
    if (text !== undefined) node.textContent = text;
    return node;
  }

  function button(text, cls, onClick) {
    const b = el('button', cls, text);
    b.type = 'button';
    b.addEventListener('click', async () => {
      b.disabled = true;
      try { await onClick(); } catch (err) { show(say(err.message)); } finally { b.disabled = false; }
    });
    return b;
  }

  // connect starts the connection of an existing connector: with OAuth the
  // browser goes to the provider's own sign-in page, which sends it back here.
  async function connect(id) {
    const r = await call('POST', '/coach/connectors/' + id + '/connect', {});
    if (r.authorize_url) {
      window.location.assign(r.authorize_url);
      return;
    }
    show('Conectado.');
    await load();
  }

  // renderCatalog shows the host's catalog as cards grouped by category, in
  // the order the catalog gives them.
  function renderCatalog(entries) {
    catalog.replaceChildren();
    let group = null;
    let current = null;
    for (const e of entries) {
      if (e.category !== current) {
        current = e.category;
        catalog.append(el('h3', 'cx-group', e.category_label));
        group = el('ul', 'cx-catalog');
        catalog.append(group);
      }
      const li = el('li', 'cx-card');
      li.append(el('span', 'cx-icon', e.icon));
      const body = el('div', 'cx-card-body');
      const head = el('div');
      head.append(el('strong', '', e.title));
      const c = e.connector;
      if (c) head.append(el('span', 'cx-badge' + (c.state === 'connected' ? ' ok' : ''), c.state === 'connected' ? 'conectado' : 'añadido'));
      body.append(head, el('div', 'muted', e.summary));
      li.append(body);
      if (!c) {
        li.append(button('Conectar', 'primary mini', async () => {
          const v = await call('POST', '/coach/connectors/catalog', { entry: e.id });
          await connect(v.id);
        }));
      } else if (c.state !== 'connected') {
        li.append(button('Conectar', 'primary mini', () => connect(c.id)));
      }
      group.append(li);
    }
  }

  function render(data) {
    list.replaceChildren();
    empty.hidden = data.connectors.length > 0;
    for (const c of data.connectors) {
      const li = el('li', 'cx-item');
      const head = el('div');
      head.append(el('strong', '', c.label));
      const state = c.state === 'connected' ? 'conectado' : 'desconectado';
      head.append(el('span', 'cx-badge' + (c.state === 'connected' ? ' ok' : ''), state));
      if (c.state === 'connected' && c.loaded === false) head.append(el('span', 'cx-badge bad', 'no cargó en el coach'));
      if (c.state === 'connected' && c.loaded === true) head.append(el('span', 'cx-badge ok', 'cargado en el coach'));
      li.append(head, el('div', 'cx-url', c.url), el('div', 'muted', 'Herramientas en el coach: mcp__' + c.name + '__…'));
      const actions = el('div', 'cx-actions');
      const tools = el('ul', 'cx-tools');
      if (c.state === 'connected') {
        actions.append(button('Ver herramientas', 'ghost mini', async () => {
          const r = await call('GET', '/coach/connectors/' + c.id + '/tools');
          tools.replaceChildren(...r.tools.map((t) => el('li', '', t.description ? t.name + ': ' + t.description : t.name)));
          if (r.tools.length === 0) tools.replaceChildren(el('li', 'muted', 'Sin herramientas.'));
        }));
        actions.append(button('Desconectar', 'ghost mini', async () => { await call('POST', '/coach/connectors/' + c.id + '/disconnect', {}); await load(); }));
      } else {
        actions.append(button('Conectar', 'primary mini', () => connect(c.id)));
      }
      actions.append(button('Quitar', 'ghost mini risky', async () => {
        if (!window.confirm('¿Quitar ' + c.label + '? Se borran sus credenciales.')) return;
        await call('POST', '/coach/connectors/' + c.id + '/remove', {});
        await load();
      }));
      if (data.grants.some((g) => g.server === c.name)) {
        actions.append(button('Retirar permisos de la sesión', 'ghost mini', async () => { await call('POST', '/coach/connectors/' + c.id + '/revoke', {}); await load(); }));
      }
      li.append(actions, tools);
      list.append(li);
    }
    grants.replaceChildren(...data.grants.map((g) => el('li', 'cx-item', g.tool + ': ' + (g.answer === 'allow_always' ? 'permitido' : 'rechazado') + ' en esta sesión')));
    if (data.grants.length === 0) grants.replaceChildren(el('li', 'muted', 'Ninguno.'));
  }

  async function load() {
    try {
      const [list, cat] = await Promise.all([call('GET', '/coach/connectors/list'), call('GET', '/coach/connectors/catalog')]);
      render(list);
      renderCatalog(cat.entries);
    } catch (err) { show(say(err.message)); }
  }

  document.getElementById('cx-add').addEventListener('submit', async (event) => {
    event.preventDefault();
    const form = event.currentTarget;
    const data = Object.fromEntries(new FormData(form));
    try {
      await call('POST', '/coach/connectors', { url: data.url || '', label: data.label || '', token: data.token || '' });
      form.reset();
      show('Añadido. Pulsa «Conectar».');
      await load();
    } catch (err) {
      show(say(err.message));
    }
  });

  const params = new URLSearchParams(window.location.search);
  if (params.get('error')) show(say(params.get('error')));
  else if (params.get('connected')) show('Conectado.');
  if (params.has('error') || params.has('connected')) history.replaceState(null, '', window.location.pathname);
  load();
})();
