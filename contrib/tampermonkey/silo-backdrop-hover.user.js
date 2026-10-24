// ==UserScript==
// @name         Silo Stash Backdrop Hover
// @namespace    https://github.com/Net005/silo-plugin-metadata-stash
// @version      1.7.0
// @downloadURL  https://raw.githubusercontent.com/Net005/silo-plugin-metadata-stash/main/contrib/tampermonkey/silo-backdrop-hover.user.js
// @updateURL    https://raw.githubusercontent.com/Net005/silo-plugin-metadata-stash/main/contrib/tampermonkey/silo-backdrop-hover.user.js
// @description  Stash backdrop previews, native Watchlist and O-count toolbar actions, library-scoped subtitle creation, a Listed in Collections row on item pages, and watched / O-count indicators plus Stash preview hover on catalog and filmography posters.
// @match        https://silo.example.invalid/*
// @connect      *
// @grant        GM_getValue
// @grant        GM_setValue
// @grant        GM_registerMenuCommand
// @grant        GM_xmlhttpRequest
// @run-at       document-idle
// ==/UserScript==
(function () {
  'use strict';
  const PLUGIN_ID = 'stash-silo-companion';
  const DEFAULTS = { enabled: true, delay: 400, cycle: 700, stashURL: '', stashKey: '', siloKey: '', subtitleLibraries: 'JAV' };
  function stashOrigin(value) {
    let url;
    try { url = new URL(value); } catch { throw new Error('Enter your Stash server URL in Backdrop hover settings.'); }
    if (!['https:', 'http:'].includes(url.protocol) || url.username || url.password || url.search || url.hash || !['', '/'].includes(url.pathname)) throw new Error('Enter a Stash server origin, such as https://stash.example.invalid, without credentials or a path.');
    return url.origin;
  }
  function sceneID(value, origin) {
    const text = value.trim();
    if (/^\d+$/.test(text)) return text;
    try {
      const url = new URL(text);
      return url.origin === stashOrigin(origin) ? /^\/scenes\/(\d+)\/?$/.exec(url.pathname)?.[1] || null : null;
    } catch { return null; }
  }
  function parseCues(text, base) {
    const lines = text.split(/\r?\n/), out = [];
    for (let i = 0; i < lines.length; i++) {
      if (!lines[i].includes('-->')) continue;
      const match = (lines[i + 1] || '').trim().match(/^(.*?)#xywh=(\d+),(\d+),(\d+),(\d+)$/);
      if (!match) continue;
      const [, path, x, y, w, h] = match;
      if (+w && +h) out.push({ url: new URL(path, base).href, x: +x, y: +y, w: +w, h: +h });
    }
    return out;
  }
  function exactScene(result, path) {
    if (result.count > 200) throw new Error('Too many file matches; assign this item a Stash scene ID in the script menu.');
    const ids = new Set(result.scenes.filter(s => s.files.some(f => f.path === path)).map(s => s.id));
    if (ids.size > 1) throw new Error('Ambiguous Stash file match; assign this item a scene ID in the script menu.');
    return [...ids][0] || null;
  }
  function createSiloFileReader({ fetch, localStorage, sessionStorage, getKey, getLibrary }) {
    let refreshing = null;
    async function refresh() {
      if (refreshing) return refreshing;
      refreshing = (async () => {
        const token = localStorage.getItem('refresh_token');
        if (!token) return false;
        const r = await fetch('/api/v2/auth/refresh', { method: 'POST', credentials: 'same-origin', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ refresh_token: token }) });
        if (!r.ok) return false;
        const d = await r.json();
        if (!d.access_token) return false;
        localStorage.setItem('access_token', d.access_token);
        if (d.refresh_token) localStorage.setItem('refresh_token', d.refresh_token);
        return true;
      })().catch(() => false);
      try { return await refreshing; } finally { refreshing = null; }
    }
    function headers() {
      const key = getKey() || localStorage.getItem('access_token');
      const profile = localStorage.getItem('profile_id');
      const proof = sessionStorage.getItem('profile_token') || localStorage.getItem('profile_token');
      return { ...(key ? { Authorization: 'Bearer ' + key } : {}), ...(profile ? { 'X-Profile-Id': profile } : {}), ...(proof ? { 'X-Profile-Token': proof } : {}) };
    }
    return async function (id) {
      const files = [], library = getLibrary(); let cursor = '';
      for (let page = 0; page < 100; page++) {
        const url = '/api/v2/admin/items/' + encodeURIComponent(id) + '/files?limit=200' + (cursor ? '&cursor=' + encodeURIComponent(cursor) : '');
        const request = () => fetch(url, { credentials: 'same-origin', headers: headers() });
        let response = await request();
        if (response.status === 401 && !getKey() && await refresh()) response = await request();
        if (!response.ok) {
          if (response.status === 401) throw new Error('Silo login expired or the saved Silo API key is invalid. Sign in again, or enter a valid Silo admin API key in Backdrop hover settings.');
          if (response.status === 403) throw new Error('Silo denied the file lookup. Unlock your administrator profile, or enter a Silo admin API key in Backdrop hover settings.');
          throw new Error('Silo file lookup returned HTTP ' + response.status + '. Try again after Silo is available.');
        }
        const result = await response.json();
        files.push(...(result.items || []).filter(f => !library || String(f.library_id) === library));
        if (!result.page?.has_more) return files;
        const next = result.page.next_cursor;
        if (!next || next === cursor) throw new Error('Silo file pagination stalled. Assign a Stash scene ID in the script menu.');
        cursor = next;
      }
      throw new Error('Too many Silo file pages. Assign a Stash scene ID in the script menu.');
    };
  }
  async function confirmSubtitleOverwrite(sceneId, runPluginOperation) {
    let status = null;
    try {
      const response = await runPluginOperation({
        variables: {
          pluginId: PLUGIN_ID,
          args: { mode: "subtitle_status", scene_id: String(sceneId) },
        },
      });
      status = response.data?.runPluginOperation || null;
    } catch (_) {
      status = null;
    }

    const backendLabel = (backends) =>
      `${backends?.transcription_backend || "unknown"} / ${backends?.translation_backend || "unknown"}`;

    if (!status || !status.sidecar_found) {
      return window.confirm(
        "🕰️ Old subtitles\n" +
          "These predate subtitle service version tracking.\n\n" +
          "Replace them with a new result?"
      );
    }

    if (status.up_to_date === false) {
      return window.confirm(
        "🆕 Newer backend available\n" +
          `Sidecar:   ${backendLabel(status.sidecar_backends)}\n` +
          `Current: ${backendLabel(status.current_backends)}\n\n` +
          "Replace the existing subtitles with a new subtitle service result?"
      );
    }

    if (status.up_to_date === true) {
      if (
        !window.confirm(
          "✅ Already up to date\n" +
            `Backend: ${backendLabel(status.current_backends)}\n\n` +
            "Regenerating is not recommended. Continue anyway?"
        )
      ) {
        return false;
      }
      return window.confirm(
        "⚠️ FORCE OVERWRITE\n" +
          "This discards up-to-date subtitles and regenerates\n" +
          "them with the SAME backend. Not recommended.\n\n" +
          "Continue?"
      );
    }

    // status.up_to_date === null: a sidecar exists but JAVBeacon-Subs did
    // not report its current backend (older release, or the check failed).
    return window.confirm(
      "🎬 Existing subtitles\n" +
        "This scene already has subtitles.\n\n" +
        "Replace them with a new JAVBeacon-Subs result?"
    );
  }

  function subtitleEligible(libraryNames, allowlist, hasSubtitles, status) {
    const names = String(allowlist || "").split(/[\n,;]+/).map(x => x.trim()).filter(Boolean);
    if (!libraryNames.some(name => names.includes(name))) return false;
    return !hasSubtitles || !!status && (!status.sidecar_found || status.up_to_date === false);
  }
  function createOCounter(request, scene) {
    let busy = false;
    function count(value, nullable = false) {
      if (nullable && value == null) return 0;
      if (!Number.isSafeInteger(value) || value < 0) throw new Error('Stash returned an invalid O count.');
      return value;
    }
    return {
      async read() {
        const data = await request('query($id:ID!){findScene(id:$id){o_counter}}', { id: scene });
        if (!data?.findScene) throw new Error('Stash scene is unavailable.');
        return count(data.findScene.o_counter, true);
      },
      async increment() {
        if (busy) return null;
        busy = true;
        try {
          // Read at click time: playback may have changed since the toolbar loaded.
          const latest = await request('query($id:ID!){findScene(id:$id){last_played_at}}', { id: scene });
          if (!latest?.findScene) throw new Error('Stash scene is unavailable.');
          const played = latest.findScene.last_played_at;
          if (played != null && (typeof played !== 'string' || !Number.isFinite(Date.parse(played)))) {
            throw new Error('Stash returned an invalid last-played timestamp.');
          }
          // O history accepts explicit timestamps; null keeps Stash's current-time
          // behavior for a scene that has never been played. Preserve exact precision.
          const data = await request('mutation($id:ID!,$times:[Timestamp!]){sceneAddO(id:$id,times:$times){count}}', { id: scene, times: played == null ? null : [played] });
          return count(data?.sceneAddO?.count);
        } finally { busy = false; }
      }
    };
  }
  function performerID(person) {
    for (const value of [person?.provider_ids?.stash, person?.provider_ids?.plex, person?.plex_guid]) {
      if (typeof value !== 'string') continue;
      const match = /^stash:(\d+)$/.exec(value);
      if (match) return match[1];
    }
    const raw = person?.provider_ids?.stash;
    if (typeof raw === 'string' && /^\d+$/.test(raw)) return raw;
    // Older enriched people carry the exact integration identity in their homepage.
    try {
      const url = new URL(person?.homepage);
      if (['https:', 'http:'].includes(url.protocol)) return /^\/api\/v1\/integrations\/performers\/(\d+)\/stash\/?$/.exec(url.pathname)?.[1] || '';
    } catch (_) {}
    return '';
  }
  function collectionHref(c) {
    const p = new URLSearchParams();
    p.set('source', c.kind === 'user' ? 'user_collection' : 'library_collection');
    p.set('collection_id', String(c.id));
    if (c.title) p.set('title', c.title);
    if (c.kind !== 'user' && c.library_id != null) p.set('library_id', String(c.library_id));
    return '/catalog?' + p.toString();
  }
  // Silo has no "collections containing this item" endpoint, so build a
  // membership index per library (plus personal collections) from the full
  // per-library lists; /collections/server is only a capped preview.
  function createCollectionIndex({ fetchJSON, ttl = 10 * 60 * 1000, now = Date.now, concurrency = 6 }) {
    const caches = new Map();
    async function membership(collections, itemsPath) {
      const members = new Map();
      let partial = false, next = 0;
      async function worker() {
        while (next < collections.length) {
          const c = collections[next++];
          try {
            const result = await fetchJSON(itemsPath(c));
            for (const item of result.items || []) {
              const id = item.content_id || item.id;
              if (!id) continue;
              if (!members.has(id)) members.set(id, []);
              members.get(id).push(c);
            }
          } catch (_) { partial = true; }
        }
      }
      await Promise.all(Array.from({ length: Math.min(concurrency, collections.length) }, worker));
      return { members, partial };
    }
    async function buildLibrary(library) {
      const listing = await fetchJSON('/api/v1/library/' + encodeURIComponent(library) + '/collections');
      const collections = (listing.collections || []).map((c, order) => {
        const entry = { ...c, kind: 'library', library_id: c.library_id ?? listing.library_id ?? library, order };
        entry.href = collectionHref(entry);
        return entry;
      });
      return membership(collections, c => '/api/v1/library/' + encodeURIComponent(c.library_id) + '/collections/' + encodeURIComponent(c.id) + '/items');
    }
    async function buildPersonal() {
      let listing;
      try { listing = await fetchJSON('/api/v2/collections'); } catch (_) { return { members: new Map(), partial: true }; }
      const collections = (listing.items || listing.collections || []).map((c, order) => {
        const entry = { ...c, kind: 'user', order };
        entry.href = collectionHref(entry);
        return entry;
      });
      return membership(collections, c => '/api/v1/collections/' + encodeURIComponent(c.id) + '/items');
    }
    function cached(key, build) {
      let entry = caches.get(key);
      if (!entry || now() - entry.at >= ttl || (entry.value && entry.value.partial)) {
        entry = { at: now(), value: null, promise: null };
        entry.promise = build().then(value => { entry.value = value; return value; });
        entry.promise.catch(() => { if (caches.get(key) === entry) caches.delete(key); });
        caches.set(key, entry);
      }
      return entry.promise;
    }
    return async function lookup(id, library) {
      let libraries = library ? [String(library)] : null;
      if (!libraries) {
        const server = await cached('server', () => fetchJSON('/api/v1/collections/server'));
        libraries = (server.libraries || []).map(l => String(l.library_id));
      }
      const indexes = await Promise.all(libraries.map(lib => cached('library:' + lib, () => buildLibrary(lib))));
      const personal = await cached('personal', buildPersonal);
      const libraryHits = indexes.flatMap(index => (index.members.get(id) || [])
        .slice().sort((a, b) => (a.sort_order ?? 1e9) - (b.sort_order ?? 1e9) || a.order - b.order));
      const personalHits = (personal.members.get(id) || []).slice().sort((a, b) => a.order - b.order);
      return [...libraryHits, ...personalHits].map(({ order, ...c }) => c);
    };
  }
  async function personOCount(person, request) {
    const id = performerID(person);
    if (!id) return null;
    const data = await request('query($id:ID!){findPerformer(id:$id){o_counter}}', { id });
    const count = data?.findPerformer?.o_counter;
    return Number.isSafeInteger(count) && count > 0 ? count : null;
  }
  // Poster-card state for grid views: Silo's own played flag plus the exact
  // Stash scene's O count. Item reads are bounded; O counts are batched.
  function createCardStateLoader({ fetchItem, resolveScene, readOCounts, concurrency = 4, ttl = 2 * 60 * 1000, now = Date.now }) {
    const cache = new Map(), scenes = new Map(), queue = [];
    let active = 0, batch = null;
    function limited(task) {
      return new Promise((resolve, reject) => {
        queue.push(() => task().then(resolve, reject));
        pump();
      });
    }
    function pump() {
      while (active < concurrency && queue.length) {
        active++;
        queue.shift()().finally(() => { active--; pump(); });
      }
    }
    function oCount(scene) {
      if (!batch) {
        const current = batch = { ids: new Set(), result: null };
        current.result = new Promise(r => setTimeout(r, 25)).then(() => { batch = null; return readOCounts([...current.ids]); });
      }
      batch.ids.add(scene);
      return batch.result.then(counts => {
        const value = counts?.[scene];
        if (value == null) return 0;
        return Number.isSafeInteger(value) && value >= 0 ? value : null;
      });
    }
    function sceneOf(id) {
      if (!scenes.has(id)) scenes.set(id, limited(() => resolveScene(id)).then(s => s ? String(s) : null).catch(() => null));
      return scenes.get(id);
    }
    async function load(id) {
      const [item, scene] = await Promise.all([limited(() => fetchItem(id)), sceneOf(id)]);
      const count = scene ? await oCount(scene).catch(() => null) : null;
      return { played: item?.user_state?.played === true, oCount: count };
    }
    return {
      get(id) {
        const hit = cache.get(id);
        if (hit && now() - hit.at < ttl) return hit.promise;
        const entry = { at: now(), promise: load(id).catch(error => { cache.delete(id); throw error; }) };
        cache.set(id, entry);
        return entry.promise;
      },
      invalidate(id) { cache.delete(id); },
    };
  }
  // Poster grids that get hover previews and indicators: every catalog grid
  // (sections, collections, filters, watchlist) and person filmographies.
  function previewGridPage(pathname) {
    return pathname === '/catalog' || /^\/person\/[^/]+\/?$/.test(pathname);
  }
  if (typeof module !== 'undefined' && module.exports) { module.exports = { previewGridPage, performerID, personOCount, createOCounter, parseCues, exactScene, createSiloFileReader, stashOrigin, sceneID, subtitleEligible, confirmSubtitleOverwrite, fullReleaseDate, metadataFilterHref, collectionHref, createCollectionIndex, createCardStateLoader }; return; }
  let settings = { ...DEFAULTS, ...GM_getValue('settings', {}) };
  let current = null;
  const overviewStyle = document.createElement('style');
  // While a preview plays, fade out everything over the backdrop except the
  // action bar: context line, title, badges, overview, director/studio lines
  // and the cover. visibility keeps the layout so the toolbar does not move.
  const previewHidden = [
    '.detail-hero-copy > :not(.detail-action-bar)',
    '.detail-hero-copy > .contents > *',
    '.detail-hero-primary-content > .media-card-image',
  ].map(s => '.item-detail-hero[data-stash-hover-preview] ' + s).join(',');
  overviewStyle.textContent = '.detail-hero-copy > *, .detail-hero-copy > .contents > *, .detail-hero-primary-content > .media-card-image{transition:opacity .2s ease,visibility .2s}' +
    previewHidden + '{opacity:0!important;visibility:hidden!important;pointer-events:none!important}';
  document.head.append(overviewStyle);
  const itemID = () => /^\/item\/([^/]+)$/.exec(location.pathname)?.[1];
  const delay = () => Math.max(0, Math.min(30000, Number(settings.delay) || 0));
  function notice(message) {
    const old = document.getElementById('silo-stash-hover-notice'); old?.remove();
    const el = document.createElement('div'); el.id = 'silo-stash-hover-notice'; el.textContent = message;
    Object.assign(el.style, { position: 'fixed', bottom: '24px', right: '24px', zIndex: '2147483647', background: '#232830', color: '#fff', padding: '16px', borderRadius: '8px', maxWidth: '440px', font: '14px/1.5 sans-serif' });
    document.body.append(el); setTimeout(() => el.remove(), 9000);
  }
  function save(next) { toolbar?.dispose(); toolbar = null; settings = next; GM_setValue('settings', next); dispose(); reconcile(); }
  function configure() {
    const dialog = document.createElement('dialog');
    dialog.innerHTML = `<form method="dialog" style="display:grid;gap:12px;width:min(460px,80vw);font:14px/1.5 sans-serif;color:inherit">
      <strong style="font-size:20px">Silo backdrop hover</strong>
      <label><input name="enabled" type="checkbox"> Enable preview</label>
      <label>Hover delay (milliseconds)<input name="delay" type="number" min="0" max="30000" step="100" style="display:block;width:100%"></label>
      <label>Thumbnail interval (milliseconds)<input name="cycle" type="number" min="100" max="10000" step="100" style="display:block;width:100%"></label>
      <label>Stash server URL<input name="stashURL" type="url" placeholder="https://stash.example.invalid" style="display:block;width:100%"></label>
      <label>Stash API key<input name="stashKey" type="password" autocomplete="off" style="display:block;width:100%"></label>
      <label>Silo admin API key (optional if your browser session works)<input name="siloKey" type="password" autocomplete="off" style="display:block;width:100%"></label>
      <label>Subtitle library names (exact, comma-separated)<input name="subtitleLibraries" type="text" style="display:block;width:100%"></label>
      <small>Keys stay in Tampermonkey storage. Subtitle generation runs only after you click Create Subtitle and approve any replacement prompt. Watchlist uses Silo’s native toggle. 400 ms matches the companion’s default. Hover the background outside the title, poster and buttons.</small>
      <div><button value="save">Save</button> <button value="cancel">Cancel</button></div></form>`;
    Object.assign(dialog.style, { background: '#20242b', color: '#fff', padding: '24px', border: '1px solid #69717e', borderRadius: '12px' });
    const form = dialog.querySelector('form');
    for (const name of ['delay', 'cycle', 'stashURL', 'stashKey', 'siloKey', 'subtitleLibraries']) form.elements[name].value = settings[name];
    form.elements.enabled.checked = settings.enabled;
    dialog.addEventListener('close', () => {
      if (dialog.returnValue === 'save') {
        try { stashOrigin(form.elements.stashURL.value.trim()); } catch (error) { notice(error.message); dialog.remove(); return; }
        save({ enabled: form.elements.enabled.checked, delay: Number(form.elements.delay.value), cycle: Number(form.elements.cycle.value), stashURL: form.elements.stashURL.value.trim(), stashKey: form.elements.stashKey.value.trim(), siloKey: form.elements.siloKey.value.trim(), subtitleLibraries: form.elements.subtitleLibraries.value });
      }
      dialog.remove();
    });
    document.body.append(dialog); dialog.showModal();
  }
  GM_registerMenuCommand('Backdrop hover: settings / delay / API keys', configure);
  GM_registerMenuCommand('Backdrop hover: assign Stash scene for this item', () => {
    const id = itemID(); if (!id) return notice('Open a Silo movie first.');
    const value = prompt('Stash scene ID (or scene URL). Leave empty to use automatic exact file matching.', GM_getValue('scene:' + id, ''));
    if (value === null) return;
    const assigned = sceneID(value, settings.stashURL);
    if (value.trim() && !assigned) return notice('Enter a numeric scene ID or a scene URL on your configured Stash server.');
    GM_setValue('scene:' + id, assigned || ''); dispose(); reconcile();
  });
  function allowed(raw) {
    const origin = stashOrigin(settings.stashURL);
    const url = new URL(raw, origin);
    if (url.origin !== origin) throw new Error('Preview resource is not on your Stash server.');
    return url.href;
  }
  function stashRequest(url, responseType, body) {
    return new Promise((resolve, reject) => {
      GM_xmlhttpRequest({ method: body ? 'POST' : 'GET', url: allowed(url), responseType, timeout: 30000,
        headers: { ...(settings.stashKey ? { ApiKey: settings.stashKey } : {}), ...(body ? { 'Content-Type': 'application/json' } : {}) },
        data: body ? JSON.stringify(body) : undefined,
        onload: r => {
          if (r.status < 200 || r.status >= 300) return reject(new Error(`Stash returned HTTP ${r.status}. Check the script’s Stash API key.`));
          try { resolve(responseType === 'json' ? (typeof r.response === 'object' ? r.response : JSON.parse(r.responseText)) : r.response); }
          catch { reject(new Error('Stash returned an invalid response.')); }
        }, onerror: () => reject(new Error('Cannot reach Stash.')), ontimeout: () => reject(new Error('Stash request timed out.')) });
    });
  }
  async function gql(query, variables) {
    const result = await stashRequest(stashOrigin(settings.stashURL) + '/graphql', 'json', { query, variables });
    if (result.errors?.length) throw new Error('Stash lookup failed. Check the API key and scene ID.');
    return result.data;
  }
  const siloFiles = createSiloFileReader({ fetch: (...args) => fetch(...args), localStorage, sessionStorage, getKey: () => settings.siloKey, getLibrary: () => new URL(location.href).searchParams.get('libraryId') || '' });
  async function resolveSceneID(id) {
    let scene = GM_getValue('scene:' + id, '');
    if (!scene) {
      const files = await siloFiles(id), matches = new Set();
      for (const path of new Set(files.map(f => f.file_path).filter(Boolean))) {
        const data = await gql('query($path:StringCriterionInput!){findScenes(scene_filter:{path:$path},filter:{per_page:200}){count scenes{id files{path}}}}', { path: { value: path, modifier: 'EQUALS' } });
        const match = exactScene(data.findScenes, path); if (match) matches.add(match);
      }
      if (matches.size !== 1) throw new Error('No unique exact Stash file match. Assign this item its Stash scene ID in the Tampermonkey menu.');
      scene = [...matches][0];
    }
    return scene;
  }
  async function sceneFor(id) {
    const scene = await resolveSceneID(id);
    const data = await gql('query($id:ID!){findScene(id:$id){id paths{preview vtt}}}', { id: scene });
    if (!data.findScene) throw new Error('The assigned Stash scene no longer exists.');
    return data.findScene.paths;
  }
  function dispose() { current?.dispose(); current = null; }
  function attach(hero, backdrop, id) {
    const layer = document.createElement('div'), video = document.createElement('video'), frame = document.createElement('div');
    Object.assign(layer.style, { position: 'absolute', inset: '0', zIndex: '1', background: '#101114', pointerEvents: 'none', overflow: 'hidden', display: 'none' });
    Object.assign(video.style, { width: '100%', height: '100%', objectFit: 'contain' });
    Object.assign(frame.style, { position: 'absolute', overflow: 'hidden', backgroundRepeat: 'no-repeat' });
    video.muted = true; video.defaultMuted = true; video.loop = true; video.playsInline = true; video.preload = 'none';
    layer.append(video, frame); backdrop.append(layer);
    let hovering = false, alive = true, generation = 0, timer = null, cycleTimer = null, pathsPromise = null, previewBlob = null, cuePromise = null, cueIndex = 0;
    const resources = new Set(), images = new Map();
    const valid = n => alive && hovering && generation === n && current?.id === id && !document.hidden;
    function previewVisible(visible) { hero.toggleAttribute('data-stash-hover-preview', visible); layer.style.display = visible ? 'block' : 'none'; }
    function stop() { hovering = false; generation++; clearTimeout(timer); clearTimeout(cycleTimer); video.pause(); previewVisible(false); }
    async function image(url) {
      if (!images.has(url)) images.set(url, (async () => {
        const blob = await stashRequest(url, 'blob'), src = URL.createObjectURL(blob); resources.add(src);
        if (!alive) { URL.revokeObjectURL(src); throw new Error('Preview closed.'); }
        const img = new Image(); img.src = src; await img.decode(); return img;
      })());
      return images.get(url);
    }
    async function sprite(paths, n) {
      if (!paths.vtt) throw new Error('This Stash scene has neither a playable preview nor thumbnail VTT. Generate previews in Stash first.');
      if (!cuePromise) cuePromise = stashRequest(paths.vtt, 'text').then(text => parseCues(text, allowed(paths.vtt)));
      const cues = await cuePromise; if (!cues.length) throw new Error('Stash thumbnail VTT contains no usable frames.');
      const step = async () => {
        if (!valid(n)) return;
        const cue = cues[cueIndex++ % cues.length], img = await image(cue.url); if (!valid(n)) return;
        const box = backdrop.getBoundingClientRect(), scale = Math.min(box.width / cue.w, box.height / cue.h);
        Object.assign(frame.style, { left: (box.width - cue.w * scale) / 2 + 'px', top: (box.height - cue.h * scale) / 2 + 'px', width: cue.w * scale + 'px', height: cue.h * scale + 'px', backgroundImage: `url("${img.src}")`, backgroundSize: `${img.naturalWidth * scale}px ${img.naturalHeight * scale}px`, backgroundPosition: `-${cue.x * scale}px -${cue.y * scale}px`, display: 'block' });
        video.style.display = 'none'; previewVisible(true); cycleTimer = setTimeout(() => step().catch(fail), Math.max(100, Number(settings.cycle) || 700));
      };
      await step();
    }
    function fail(error) { if (alive && hovering) { stop(); notice(error.message); } }
    async function start(n) {
      if (!valid(n)) return;
      if (!pathsPromise) pathsPromise = sceneFor(id).catch(error => { pathsPromise = null; throw error; });
      const paths = await pathsPromise; if (!valid(n)) return;
      if (paths.preview) {
        try {
          if (!previewBlob) {
            previewBlob = stashRequest(paths.preview, 'blob').then(blob => { const src = URL.createObjectURL(blob); resources.add(src); return src; }).catch(error => { previewBlob = null; throw error; });
          }
          const src = await previewBlob; if (!valid(n)) { if (!alive) URL.revokeObjectURL(src); return; }
          if (video.src !== src) video.src = src;
          video.currentTime = 0; await video.play();
          if (!valid(n)) { video.pause(); return; }
          frame.style.display = 'none'; video.style.display = 'block'; previewVisible(true); return;
        } catch { video.pause(); }
      }
      await sprite(paths, n);
    }
    function move(event) {
      const excluded = event.target.closest('a,button,input,select,textarea,nav,[role="button"],.media-card-image,.detail-hero-copy');
      if (excluded || !settings.enabled || document.hidden) { if (hovering) stop(); return; }
      if (hovering) return;
      hovering = true; const n = ++generation; timer = setTimeout(() => start(n).catch(fail), delay());
    }
    hero.addEventListener('pointermove', move); hero.addEventListener('pointerleave', stop);
    document.addEventListener('visibilitychange', stop);
    return { id, hero, backdrop, dispose() { alive = false; stop(); hero.removeEventListener('pointermove', move); hero.removeEventListener('pointerleave', stop); document.removeEventListener('visibilitychange', stop); video.removeAttribute('src'); video.load(); layer.remove(); for (const src of resources) URL.revokeObjectURL(src); } };
  }
  let toolbar = null;
  async function siloJSON(path) {
    function headers() {
      const key = settings.siloKey || localStorage.getItem('access_token');
      const profile = localStorage.getItem('profile_id');
      const proof = sessionStorage.getItem('profile_token') || localStorage.getItem('profile_token');
      return { ...(key ? { Authorization: 'Bearer ' + key } : {}), ...(profile ? { 'X-Profile-Id': profile } : {}), ...(proof ? { 'X-Profile-Token': proof } : {}) };
    }
    let r = await fetch(path, { credentials: 'same-origin', headers: headers() });
    if (r.status === 401 && !settings.siloKey && localStorage.getItem('refresh_token')) {
      const renewed = await fetch('/api/v2/auth/refresh', { method: 'POST', credentials: 'same-origin', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ refresh_token: localStorage.getItem('refresh_token') }) });
      if (renewed.ok) {
        const token = await renewed.json();
        if (token.access_token) {
          localStorage.setItem('access_token', token.access_token);
          if (token.refresh_token) localStorage.setItem('refresh_token', token.refresh_token);
          r = await fetch(path, { credentials: 'same-origin', headers: headers() });
        }
      }
    }
    if (!r.ok) throw new Error('Silo returned HTTP ' + r.status + '. Check your session or API key.');
    return r.json();
  }
  async function subtitleOperation(args) {
    const data = await gql('mutation($pluginId:ID!,$args:Map){runPluginOperation(plugin_id:$pluginId,args:$args)}', { pluginId: PLUGIN_ID, args });
    if (!data?.runPluginOperation) throw new Error('Stash subtitle companion returned no result.');
    return { data: { runPluginOperation: data.runPluginOperation } };
  }
  // Silo RequestActionBar/RequestPosterCard use Lucide Bookmark/BookmarkCheck.
  // The detail overflow menu uses Plus/Check, which are unsuitable for this toolbar.
  function watchlistIcon(added) {
    const svg = document.createElementNS('http://www.w3.org/2000/svg', 'svg');
    const attrs = { width: '24', height: '24', viewBox: '0 0 24 24', fill: 'none', stroke: 'currentColor', 'stroke-width': '2', 'stroke-linecap': 'round', 'stroke-linejoin': 'round', class: 'size-[18px]', 'aria-hidden': 'true' };
    for (const [key, value] of Object.entries(attrs)) svg.setAttribute(key, value);
    const paths = ['M17 3a2 2 0 0 1 2 2v15a1 1 0 0 1-1.496.868l-4.512-2.578a2 2 0 0 0-1.984 0l-4.512 2.578A1 1 0 0 1 5 20V5a2 2 0 0 1 2-2z'];
    if (added) paths.push('m9 10 2 2 4-4');
    for (const d of paths) {
      const path = document.createElementNS('http://www.w3.org/2000/svg', 'path');
      path.setAttribute('d', d); svg.append(path);
    }
    return svg;
  }
  function oIcon() {
    const icon = watchlistIcon(false);
    icon.replaceChildren();
    const path = document.createElementNS('http://www.w3.org/2000/svg', 'path');
    path.setAttribute('d', 'M12 22a7 7 0 0 0 7-7c0-4-7-13-7-13S5 11 5 15a7 7 0 0 0 7 7z M9 15a3 3 0 0 0 3 3');
    icon.append(path);
    return icon;
  }
  function attachToolbar(more, id) {
    let alive = true, loading = false, captured = null, internalMenu = false;
    const watch = more.cloneNode(false), subs = more.cloneNode(false), orgasm = more.cloneNode(false);
    for (const button of [watch, subs, orgasm]) {
      for (const attr of [...button.attributes]) if (attr.name.startsWith('aria-') || attr.name === 'id' || attr.name === 'title') button.removeAttribute(attr.name);
      button.type = 'button'; button.disabled = false;
    }
    watch.dataset.stashWatchlist = ''; watch.hidden = true;
    subs.dataset.stashSubtitles = ''; subs.hidden = true;
    subs.title = 'Create Subtitle'; subs.setAttribute('aria-label', 'Create Subtitle');
    // Keep the native glass variant and theme tokens; change only icon sizing
    // to the text sizing used by Silo's link actions in this same toolbar.
    subs.classList.remove('size-11');
    subs.classList.add('h-11', 'px-4', 'text-[0.8125rem]', 'font-semibold', 'tracking-wide');
    subs.textContent = '+ Sub';
    orgasm.dataset.stashOCount = ''; orgasm.hidden = true;
    const openStash = document.createElement('a');
    openStash.className = more.className;
    openStash.dataset.stashOpen = ''; openStash.hidden = true;
    openStash.title = 'Open in Stash'; openStash.setAttribute('aria-label', 'Open in Stash');
    openStash.target = '_blank'; openStash.rel = 'noopener noreferrer';
    const externalIcon = watchlistIcon(false);
    externalIcon.replaceChildren();
    for (const d of ['M7 17 17 7', 'M7 7h10v10']) {
      const path = document.createElementNS('http://www.w3.org/2000/svg', 'path');
      path.setAttribute('d', d); externalIcon.append(path);
    }
    openStash.append(externalIcon);
    let counter = null;
    function renderO(value) {
      const icon = oIcon();
      orgasm.replaceChildren(icon);
      orgasm.classList.toggle('size-11', value === 0);
      for (const name of ['h-11', 'px-4', 'gap-2', 'text-[0.8125rem]', 'font-semibold', 'tabular-nums']) orgasm.classList.toggle(name, value > 0);
      if (value > 0) {
        const label = document.createElement('span'); label.textContent = String(value); orgasm.append(label);
      }
      orgasm.title = value > 0 ? `O count: ${value} · Add 1` : 'Add O (+1)';
      orgasm.setAttribute('aria-label', `Orgasm count: ${value}. Add one`);
      orgasm.hidden = false;
    }
    async function refreshO() {
      try {
        const scene = await resolveSceneID(id);
        if (!alive || itemID() !== id) return;
        const validScene = sceneID(String(scene), settings.stashURL);
        if (!validScene) throw new Error('Invalid Stash scene ID.');
        openStash.href = stashOrigin(settings.stashURL) + '/scenes/' + encodeURIComponent(validScene);
        openStash.hidden = false;
        const next = createOCounter(gql, scene), value = await next.read();
        if (!alive || itemID() !== id) return;
        counter = next; renderO(value);
      } catch (error) { if (alive) orgasm.title = error.message; }
    }
    orgasm.addEventListener('click', async event => {
      event.preventDefault(); event.stopPropagation();
      if (!counter || orgasm.disabled || !alive || itemID() !== id) return;
      orgasm.disabled = true; orgasm.setAttribute('aria-busy', 'true');
      try {
        const value = await counter.increment();
        if (value !== null && alive && itemID() === id) renderO(value);
      } catch (error) {
        counter = null; // A timeout may have committed; require a fresh read before another increment.
        if (alive && itemID() === id) notice('O count could not be confirmed. Refresh before trying again. ' + error.message);
      } finally { orgasm.disabled = false; orgasm.removeAttribute('aria-busy'); }
    });
    more.before(watch, orgasm, subs, openStash);
    function nativeWatch() {
      const menuID = more.getAttribute('aria-controls');
      const menu = menuID ? document.getElementById(menuID) : document.querySelector('.detail-overflow-menu');
      return [...(menu?.querySelectorAll('[role="menuitem"]') || [])].find(el => /^(Add to|Remove from) Watchlist$/.test(el.textContent.trim()));
    }
    function capture() {
      const action = nativeWatch(); if (!action) return null;
      const label = action.textContent.trim();
      const inWatchlist = label.startsWith('Remove');
      if (watch.getAttribute('aria-label') !== label) {
        watch.title = label; watch.setAttribute('aria-label', label); watch.setAttribute('aria-pressed', String(inWatchlist));
        watch.replaceChildren(watchlistIcon(inWatchlist));
      }
      watch.hidden = false; action.dataset.stashWatchlistMoved = ''; captured = action;
      return action;
    }
    async function acquire() {
      if (more.getAttribute('aria-expanded') === 'true') return capture();
      internalMenu = true; document.documentElement.setAttribute('data-stash-toolbar-acquiring', '');
      more.click();
      for (let i = 0; i < 12 && alive && itemID() === id; i++) {
        await new Promise(resolve => requestAnimationFrame(resolve));
        const action = capture(); if (action) return action;
      }
      return null;
    }
    function closeInternalMenu() {
      if (internalMenu && more.getAttribute('aria-expanded') === 'true') more.click();
      internalMenu = false; document.documentElement.removeAttribute('data-stash-toolbar-acquiring');
    }
    watch.addEventListener('click', async event => {
      event.preventDefault(); event.stopPropagation();
      if (watch.disabled) return;
      watch.disabled = true;
      try {
        const action = await acquire();
        if (!alive || itemID() !== id) return;
        if (!action) throw new Error('Silo Watchlist action is unavailable.');
        action.click(); // Silo owns mutations, cache invalidation and feedback.
        closeInternalMenu();
        const currentAction = await acquire();
        if (currentAction) capture();
      } catch (error) { notice(error.message); }
      finally { closeInternalMenu(); watch.disabled = false; if (alive && itemID() === id) watch.focus(); }
    });
    let context = null;
    async function readSubtitleContext() {
      const files = await siloFiles(id);
      const libraries = await siloJSON('/api/v2/libraries');
      const memberIDs = new Set(files.map(f => String(f.library_id)));
      const names = (libraries.items || []).filter(lib => memberIDs.has(String(lib.id))).map(lib => lib.name);
      if (!subtitleEligible(names, settings.subtitleLibraries, false, null)) return null;
      const scene = await resolveSceneID(id);
      const data = await gql('query($id:ID!){findScene(id:$id){id captions{language_code caption_type}}}', { id: scene });
      if (!data.findScene) throw new Error('Stash scene is unavailable.');
      const item = await siloJSON('/api/v2/catalog/items/' + encodeURIComponent(id));
      const status = (await subtitleOperation({ mode: 'subtitle_status', scene_id: scene })).data.runPluginOperation;
      const hasSubtitles = !!data.findScene.captions?.length || !!item.subtitles?.length || !!status.sidecar_found;
      return { scene, names, hasSubtitles, status };
    }
    async function refreshSubtitles() {
      try {
        const next = await readSubtitleContext();
        if (!alive || itemID() !== id) return;
        context = next;
        subs.hidden = !next || !subtitleEligible(next.names, settings.subtitleLibraries, next.hasSubtitles, next.status);
      } catch (error) {
        if (alive) { subs.hidden = true; subs.title = error.message; }
      }
    }
    subs.addEventListener('click', async event => {
      event.preventDefault(); event.stopPropagation();
      if (loading) return;
      loading = true; subs.disabled = true;
      try {
        const next = await readSubtitleContext();
        if (!alive || itemID() !== id) return;
        context = next;
        if (!next || !subtitleEligible(next.names, settings.subtitleLibraries, next.hasSubtitles, next.status)) {
          subs.hidden = true; return notice('Subtitle status changed; no generation is needed.');
        }
        if (next.hasSubtitles && !await confirmSubtitleOverwrite(next.scene, input => subtitleOperation(input.variables.args))) return;
        if (!alive || itemID() !== id) return;
        const response = await subtitleOperation({ mode: 'subtitles', scene_id: next.scene, overwrite: next.hasSubtitles });
        const filename = response.data.runPluginOperation.filename;
        notice(filename ? 'Subtitle request queued for ' + filename : 'Subtitle request queued');
        subs.hidden = true;
      } catch (error) { notice(error.message); }
      finally { loading = false; subs.disabled = false; }
    });
    const menuObserver = new MutationObserver(() => { if (alive && itemID() === id) capture(); });
    menuObserver.observe(document.body, { childList: true, subtree: true });
    const previousFocus = document.activeElement;
    acquire().catch(() => {}).finally(() => { closeInternalMenu(); if (alive && previousFocus?.isConnected) previousFocus.focus(); });
    refreshSubtitles();
    refreshO();
    return { id, more, dispose() { alive = false; closeInternalMenu(); menuObserver.disconnect(); watch.remove(); subs.remove(); orgasm.remove(); openStash.remove(); captured?.removeAttribute('data-stash-watchlist-moved'); } };
  }
  const toolbarStyle = document.createElement('style');
  toolbarStyle.textContent = '[data-stash-toolbar-acquiring] .detail-overflow-menu{visibility:hidden!important}[data-stash-watchlist-moved]{display:none!important}button[data-stash-watchlist][hidden],button[data-stash-subtitles][hidden],button[data-stash-o-count][hidden],a[data-stash-open][hidden]{display:none!important}';
  // Keep Silo's actual radios and keyboard handlers. Expansion overlays the
  // neighbouring space rather than changing the flex row's measured width.
  toolbarStyle.textContent += `
    .item-detail-hero .detail-secondary-action:has(> .star-rating) {
      position:relative;width:44px;height:44px;flex:0 0 44px;
    }
    .item-detail-hero .detail-secondary-action > .star-rating {
      position:absolute;left:0;top:0;height:44px;width:44px;
      justify-content:center;padding:0;z-index:2;
    }
    .item-detail-hero .detail-secondary-action:not(:hover):not(:focus-within) > .star-rating .star-rating-star {
      display:none;
    }
    .item-detail-hero .detail-secondary-action:not(:hover):not(:focus-within) > .star-rating .star-rating-star[aria-checked="true"],
    .item-detail-hero .detail-secondary-action:not(:hover):not(:focus-within) > .star-rating:not(:has([aria-checked="true"])) .star-rating-star:first-child {
      display:inline-flex;align-items:center;gap:3px;
    }
    .item-detail-hero .detail-secondary-action:not(:hover):not(:focus-within) > .star-rating .star-rating-star[aria-checked="true"]::after {
      content:attr(data-rating);font-size:12px;font-weight:600;
    }
    .item-detail-hero .detail-secondary-action:is(:hover,:focus-within) > .star-rating {
      width:max-content;min-width:44px;padding:0 10px;z-index:20;
      background:var(--color-background,#18181b);box-shadow:0 4px 16px #0006;
    }
  `;
  document.head.append(toolbarStyle);
  function reconcileToolbar() {
    const id = itemID(), more = document.querySelector('.item-detail-hero button[aria-label="More actions"]');
    if (!id || !more) { toolbar?.dispose(); toolbar = null; return; }
    if (toolbar?.id === id && toolbar.more === more && more.parentElement.querySelector('[data-stash-subtitles]')) return;
    toolbar?.dispose(); toolbar = attachToolbar(more, id);
  }
  function fullReleaseDate(value) {
    if (typeof value !== 'string') return '';
    const match = /^(\d{4}-\d{2}-\d{2})(?:$|T)/.exec(value.trim());
    if (!match) return '';
    const date = new Date(match[1] + 'T00:00:00Z');
    return Number.isFinite(date.getTime()) && date.toISOString().slice(0, 10) === match[1] ? match[1] : '';
  }
  function metadataFilterHref(library, field, value) {
    if (!/^\d+$/.test(String(library)) || !['genre', 'studio'].includes(field) || typeof value !== 'string' || !value.trim()) return '';
    const params = new URLSearchParams({ tab: 'library', sort: 'release_date', order: 'desc', 'groups[0][match]': 'all', 'groups[0][rules][0][field]': field, 'groups[0][rules][0][op]': 'is', 'groups[0][rules][0][value]': value });
    return '/library/' + encodeURIComponent(library) + '?' + params;
  }
  function metadataLink(library, field, value, className) {
    const link = document.createElement('a');
    link.href = metadataFilterHref(library, field, value);
    link.target = '_blank'; link.rel = 'noopener noreferrer';
    link.className = className + ' hover:text-foreground/90 transition-colors';
    link.textContent = value;
    link.title = 'Browse ' + field + ': ' + value;
    return link;
  }
  function reconcileMetadataLinks(hero, state) {
    const oldStudios = hero.querySelector('[data-stash-studios]');
    if (oldStudios && oldStudios.dataset.stashStudios !== state.id) oldStudios.remove();
    if (!state.item || !state.library) return;
    const genres = (state.item.genres || []).filter(value => typeof value === 'string');
    const studios = [...new Set((state.item.studios || []).filter(value => typeof value === 'string' && value.trim()))];
    let crewLine = null;
    for (const span of hero.querySelectorAll('span.text-foreground\\/60')) {
      if (span.children.length || !genres.includes(span.textContent.trim())) continue;
      crewLine = span.closest('div');
      span.replaceWith(metadataLink(state.library, 'genre', span.textContent.trim(), span.className));
    }
    for (const span of hero.querySelectorAll('.detail-hero-context span')) {
      if (!span.children.length && studios.includes(span.textContent.trim())) span.replaceWith(metadataLink(state.library, 'studio', span.textContent.trim(), span.className));
    }
    if (!studios.length || hero.querySelector('[data-stash-studios]')) return;
    const anchor = crewLine || hero.querySelector('.metadata-badge')?.parentElement;
    if (!anchor) return;
    const line = document.createElement('div');
    line.dataset.stashStudios = state.id;
    line.className = 'text-muted-foreground text-[0.8125rem]';
    const label = document.createElement('span'); label.className = 'text-muted-foreground/60'; label.textContent = 'Studio: '; line.append(label);
    studios.forEach((studio, index) => {
      if (index) { const separator = document.createElement('span'); separator.className = 'text-muted-foreground/40 mx-1.5'; separator.textContent = '·'; line.append(separator); }
      line.append(metadataLink(state.library, 'studio', studio, 'text-foreground/60'));
    });
    anchor.after(line);
  }
  let releaseDateState = null;
  function reconcileReleaseDate() {
    const id = itemID(), hero = document.querySelector('.item-detail-hero');
    if (!id || !hero) { releaseDateState = null; return; }
    if (releaseDateState?.id !== id) {
      const state = releaseDateState = { id, date: '', item: null, library: '' };
      siloJSON('/api/v2/catalog/items/' + encodeURIComponent(id)).then(item => {
        if (releaseDateState !== state || itemID() !== id) return;
        state.item = item;
        state.date = fullReleaseDate(item.release_date);
        reconcileReleaseDate();
        siloFiles(id).then(files => {
          if (releaseDateState !== state || itemID() !== id) return;
          state.library = String(files[0]?.library_id || item.library_id || '');
          reconcileReleaseDate();
        }).catch(() => {});
      }).catch(() => {});
    }
    reconcileMetadataLinks(hero, releaseDateState);
    const date = releaseDateState.date;
    if (!date) return;
    // Target only the year badge, retaining Silo's markup, classes and theme.
    const badge = [...hero.querySelectorAll('.metadata-badge')].find(el => el.textContent.trim() === date.slice(0, 4));
    if (badge) badge.textContent = date;
  }
  const personPageID = () => /^\/person\/([^/]+)\/?$/.exec(location.pathname)?.[1];
  let personOState = null;
  function reconcilePersonO() {
    const id = personPageID();
    if (!id) { personOState = null; document.querySelectorAll('[data-stash-person-o]').forEach(el => el.remove()); return; }
    if (personOState?.id !== id) {
      document.querySelectorAll('[data-stash-person-o]').forEach(el => el.remove());
      const state = personOState = { id, count: null };
      siloJSON('/api/v2/catalog/people/' + encodeURIComponent(id) + '?prefetch=true')
        .then(person => personOCount(person, gql))
        .then(count => {
          if (personOState !== state || personPageID() !== id) return;
          state.count = count;
          reconcilePersonO();
        }).catch(() => {});
    }
    if (personOState.count === null) return;
    const section = document.querySelector('section.page-shell');
    const info = section?.querySelector('h1')?.parentElement?.parentElement;
    const row = info?.querySelector('.mb-4.flex.flex-wrap.items-center');
    if (!row || row.querySelector('[data-stash-person-o]')) return;
    const badge = document.createElement('span');
    badge.className = 'metadata-badge';
    badge.dataset.stashPersonO = id;
    badge.style.display = 'inline-flex'; badge.style.alignItems = 'center'; badge.style.gap = '0.375rem';
    badge.title = 'Orgasm count'; badge.setAttribute('aria-label', `Orgasm count: ${personOState.count}`);
    badge.append(oIcon(), document.createTextNode(String(personOState.count)));
    const age = [...row.querySelectorAll('.metadata-badge')].find(el => /years old|\(age /i.test(el.textContent));
    if (age) age.after(badge); else row.append(badge);
  }
  const collectionLookup = createCollectionIndex({ fetchJSON: siloJSON });
  let collectionState = null;
  function collectionCard(c, template) {
    // More Like This nests Silo's poster-size class one level inside the
    // carousel slide; mirror both levels so covers render at the same width.
    const slide = document.createElement('div');
    slide.className = template?.className || '';
    slide.style.flex = '0 0 auto';
    const sizeSource = template?.querySelector('.group\\/card')?.parentElement;
    const wrap = sizeSource && sizeSource !== template ? document.createElement('div') : slide;
    if (wrap !== slide) { wrap.className = sizeSource.className; slide.append(wrap); }
    if (!template) wrap.className = 'w-[130px] sm:w-[150px] lg:w-[178px]';
    const measured = template?.querySelector('.aspect-\\[2\\/3\\]')?.getBoundingClientRect().width;
    if (measured && !(sizeSource && sizeSource !== template)) wrap.style.width = Math.round(measured) + 'px';
    const card = document.createElement('div'); card.className = 'group/card';
    const media = document.createElement('div'); media.className = 'group/media relative';
    const link = document.createElement('a'); link.href = c.href; link.className = 'group block'; link.setAttribute('aria-label', c.title);
    const frame = document.createElement('div'); frame.className = 'relative aspect-[2/3] overflow-hidden rounded-lg bg-surface';
    const placeholder = () => {
      frame.replaceChildren();
      const label = document.createElement('div');
      label.className = 'text-muted-foreground flex h-full w-full items-center justify-center p-3 text-center text-sm font-medium';
      label.textContent = c.title; frame.append(label);
    };
    if (c.poster_url) {
      const img = document.createElement('img');
      img.src = c.poster_url; img.alt = c.title; img.loading = 'lazy'; img.decoding = 'async';
      img.className = 'h-full w-full object-cover transition-transform group-hover:scale-105';
      img.addEventListener('error', placeholder, { once: true });
      frame.append(img);
    } else placeholder();
    link.append(frame); media.append(link);
    const caption = document.createElement('a'); caption.href = c.href; caption.className = 'mt-1.5 block truncate text-sm font-medium hover:underline'; caption.textContent = c.title;
    card.append(media, caption); wrap.append(card);
    return slide;
  }
  function spaNavigate(event) {
    const a = event.target.closest('a[href]');
    if (!a || event.defaultPrevented || event.button !== 0 || event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) return;
    event.preventDefault();
    history.pushState(null, '', a.getAttribute('href'));
    window.dispatchEvent(new PopStateEvent('popstate', { state: null }));
    window.scrollTo(0, 0);
  }
  function buildCollectionsRow(collections, mlt) {
    let row;
    if (mlt) {
      row = mlt.cloneNode(true);
      row.querySelectorAll('button[aria-label^="Scroll"], .pointer-events-none.absolute').forEach(el => el.remove());
      row.querySelectorAll('[aria-label^="Explore all"]').forEach(el => el.remove());
      const h2 = row.querySelector('h2'); if (h2) h2.textContent = 'Listed in Collections';
    } else {
      row = document.createElement('section');
      row.className = 'section-row media-carousel relative isolate';
      row.innerHTML = '<div class="mb-5 flex items-end justify-between gap-4"><div class="flex items-center gap-2"><h2 class="text-foreground text-xl font-semibold tracking-tight">Listed in Collections</h2></div></div><div class="relative"><div class="embla__viewport -mt-1 overflow-hidden pt-1"><div class="embla__container flex list-none gap-4 lg:gap-5"></div></div></div>';
    }
    row.dataset.stashCollections = '';
    row.removeAttribute('id');
    const viewport = row.querySelector('.embla__viewport');
    const container = row.querySelector('.embla__container');
    const template = mlt?.querySelector('.embla__container')?.firstElementChild;
    if (viewport) { viewport.style.overflowX = 'auto'; viewport.style.scrollbarWidth = 'none'; viewport.style.cursor = 'auto'; viewport.setAttribute('aria-label', 'Collections'); }
    if (container) { container.style.transform = 'none'; container.style.cursor = 'auto'; container.replaceChildren(...collections.map(c => collectionCard(c, template))); }
    if (viewport) attachCarouselControls(row, viewport, mlt);
    row.addEventListener('click', spaNavigate);
    return row;
  }
  // Mirror Silo's carousel controls (More Like This): hover-revealed gradient
  // arrow buttons at both edges plus a left fade once scrolled, and arrow keys.
  const chevronPath = { left: 'm15 18-6-6 6-6', right: 'm9 18 6-6-6-6' };
  function carouselButton(side, mlt) {
    const button = document.createElement('button');
    button.type = 'button';
    button.setAttribute('aria-label', side === 'left' ? 'Scroll left' : 'Scroll right');
    button.className = 'from-background/80 absolute top-0 bottom-0 z-10 flex h-11 w-11 items-center justify-center self-center to-transparent opacity-0 transition-opacity duration-(--duration-fast) group-hover/carousel:opacity-100 focus-visible:opacity-100 ' +
      (side === 'left' ? 'left-0 bg-gradient-to-r' : 'right-0 bg-gradient-to-l');
    const native = mlt?.querySelector('button[aria-label="Scroll right"] svg, button[aria-label="Scroll left"] svg');
    let icon;
    if (native) {
      icon = native.cloneNode(true);
      const path = icon.querySelector('path');
      if (path && icon.querySelectorAll('path').length === 1) path.setAttribute('d', chevronPath[side]);
    } else {
      icon = document.createElementNS('http://www.w3.org/2000/svg', 'svg');
      icon.setAttribute('viewBox', '0 0 24 24'); icon.setAttribute('fill', 'none'); icon.setAttribute('stroke', 'currentColor');
      icon.setAttribute('stroke-width', '2'); icon.setAttribute('stroke-linecap', 'round'); icon.setAttribute('stroke-linejoin', 'round');
      const path = document.createElementNS('http://www.w3.org/2000/svg', 'path'); path.setAttribute('d', chevronPath[side]); icon.append(path);
    }
    icon.setAttribute('class', 'text-foreground h-6 w-6');
    button.append(icon);
    return button;
  }
  function attachCarouselControls(row, viewport, mlt) {
    row.classList.add('group/carousel');
    const holder = viewport.parentElement;
    holder.classList.add('relative');
    const fade = document.createElement('div');
    fade.className = 'from-background/80 pointer-events-none absolute top-0 bottom-0 left-0 z-[5] w-10 bg-gradient-to-r to-transparent';
    const prev = carouselButton('left', mlt), next = carouselButton('right', mlt);
    holder.prepend(fade, prev);
    holder.append(next);
    const style = document.createElement('style');
    style.textContent = '[data-stash-collections] .embla__viewport::-webkit-scrollbar{display:none}';
    row.prepend(style);
    const step = () => Math.max(200, viewport.clientWidth * 0.8);
    const update = () => {
      const canPrev = viewport.scrollLeft > 2;
      const canNext = viewport.scrollLeft + viewport.clientWidth < viewport.scrollWidth - 2;
      fade.style.display = prev.style.display = canPrev ? '' : 'none';
      next.style.display = canNext ? '' : 'none';
    };
    prev.addEventListener('click', event => { event.preventDefault(); event.stopPropagation(); viewport.scrollBy({ left: -step(), behavior: 'smooth' }); });
    next.addEventListener('click', event => { event.preventDefault(); event.stopPropagation(); viewport.scrollBy({ left: step(), behavior: 'smooth' }); });
    viewport.tabIndex = 0;
    viewport.addEventListener('keydown', event => {
      if (event.key === 'ArrowLeft') { event.preventDefault(); viewport.scrollBy({ left: -step(), behavior: 'smooth' }); }
      if (event.key === 'ArrowRight') { event.preventDefault(); viewport.scrollBy({ left: step(), behavior: 'smooth' }); }
    });
    viewport.addEventListener('scroll', update, { passive: true });
    if (typeof ResizeObserver === 'function') new ResizeObserver(update).observe(viewport);
    requestAnimationFrame(update);
  }
  // Collection grids: show Silo's watched eye permanently (Silo only reveals
  // it on hover) and the exact Stash scene's O count beside it. Both use the
  // native card-action chip styling and step aside while Silo's hover
  // controls occupy the same corner.
  const cardStyle = document.createElement('style');
  cardStyle.textContent = '[data-stash-card-badges]{transition:opacity var(--duration-fast,150ms) var(--ease-default,ease)}'
    + ':where(:root[data-fine-pointer=true]) .group\\/card:hover [data-stash-card-badges],.group\\/card:focus-within [data-stash-card-badges]{opacity:0}';
  document.head.append(cardStyle);
  const cardLoader = createCardStateLoader({
    fetchItem: id => siloJSON('/api/v2/catalog/items/' + encodeURIComponent(id)),
    resolveScene: id => settings.stashURL ? resolveSceneID(id) : Promise.resolve(null),
    readOCounts: async ids => {
      if (!ids.length) return {};
      const query = '{' + ids.map((id, i) => `s${i}:findScene(id:${JSON.stringify(id)}){o_counter}`).join(' ') + '}';
      const data = await gql(query, {});
      return Object.fromEntries(ids.map((id, i) => [id, data?.['s' + i] ? data['s' + i].o_counter : undefined]));
    },
  });
  const cardObserver = 'IntersectionObserver' in window ? new IntersectionObserver(entries => {
    for (const entry of entries) if (entry.isIntersecting) { cardObserver.unobserve(entry.target); loadCard(entry.target); }
  }, { rootMargin: '400px 0px' }) : null;
  function cardItemID(card) {
    const href = card.querySelector('a[href*="/item/"]')?.getAttribute('href') || '';
    const m = /\/item\/([^/?#]+)/.exec(href);
    return m ? decodeURIComponent(m[1]) : '';
  }
  function cardChip(extra) {
    const chip = document.createElement('span');
    chip.className = 'inline-flex items-center justify-center rounded-md border border-border/20 bg-background/80 shadow-sm h-6 sm:h-8 ' + extra;
    return chip;
  }
  function renderCard(card, id, state) {
    const media = card.querySelector('.group\\/media');
    if (!media || cardItemID(card) !== id) return;
    media.querySelector('[data-stash-card-badges]')?.remove();
    if (!state.played && !(state.oCount > 0)) return;
    const box = document.createElement('div');
    box.dataset.stashCardBadges = id;
    box.className = 'pointer-events-none absolute z-10 flex items-center bottom-1.5 left-1.5 gap-0.5 sm:bottom-2.5 sm:left-2.5 sm:gap-1.5';
    if (state.played) {
      const chip = cardChip('size-6 sm:size-8 text-emerald-400');
      chip.setAttribute('role', 'img'); chip.setAttribute('aria-label', 'Watched'); chip.title = 'Watched';
      const eye = watchlistIcon(false); eye.replaceChildren();
      eye.setAttribute('class', 'size-3 sm:size-4');
      for (const [tag, attrs] of [['path', { d: 'M2.062 12.348a1 1 0 0 1 0-.696 10.75 10.75 0 0 1 19.876 0 1 1 0 0 1 0 .696 10.75 10.75 0 0 1-19.876 0' }], ['circle', { cx: '12', cy: '12', r: '3' }]]) {
        const node = document.createElementNS('http://www.w3.org/2000/svg', tag);
        for (const [k, v] of Object.entries(attrs)) node.setAttribute(k, v);
        eye.append(node);
      }
      chip.append(eye); box.append(chip);
    }
    if (state.oCount > 0) {
      const chip = cardChip('gap-1 px-1.5 sm:px-2 text-foreground text-[0.6875rem] sm:text-xs font-semibold tabular-nums leading-none');
      chip.setAttribute('role', 'img'); chip.setAttribute('aria-label', `O count ${state.oCount}`); chip.title = `O count: ${state.oCount}`;
      const icon = oIcon(); icon.setAttribute('class', 'size-3 sm:size-4 text-rose-400');
      chip.append(icon, document.createTextNode(String(state.oCount)));
      box.append(chip);
    }
    media.append(box);
  }
  function loadCard(card) {
    const id = cardItemID(card);
    if (!id) return;
    card.dataset.stashCardState = id;
    cardLoader.get(id).then(state => { if (card.isConnected && previewGridPage(location.pathname)) renderCard(card, id, state); }).catch(() => {});
  }
  function reconcileCardBadges() {
    if (!previewGridPage(location.pathname)) { document.querySelectorAll('[data-stash-card-badges]').forEach(el => el.remove()); return; }
    for (const card of document.querySelectorAll('.media-card.group\\/card')) {
      const id = cardItemID(card);
      if (!id || card.dataset.stashCardState === id) continue;
      card.querySelector('[data-stash-card-badges]')?.remove();
      card.dataset.stashCardState = id;
      if (cardObserver) cardObserver.observe(card); else loadCard(card);
    }
  }
  // Silo's own watched/favorite shortcuts change the state; re-read after them.
  document.addEventListener('click', event => {
    const card = event.target.closest?.('.media-card.group\\/card');
    if (!card || !event.target.closest('button') || !previewGridPage(location.pathname)) return;
    const id = cardItemID(card);
    if (!id) return;
    setTimeout(() => { cardLoader.invalidate(id); loadCard(card); }, 1200);
  }, true);
  // Poster hover previews: hovering a grid poster expands it into a large,
  // centred preview window playing the exact Stash scene's preview. The
  // window is display-only (pointer events pass through), so the hover stays
  // on the poster and moving away closes it.
  cardStyle.textContent += [
    '[data-stash-preview-modal]{position:fixed;inset:0;z-index:2147483000;pointer-events:none;transition:opacity .18s ease}',
    '[data-stash-preview-modal].spm-closing{opacity:0}',
    '.spm-backdrop{position:absolute;inset:0;background:rgb(0 0 0/.62);-webkit-backdrop-filter:blur(6px);backdrop-filter:blur(6px);opacity:0;transition:opacity .24s ease}',
    '.spm-open .spm-backdrop{opacity:1}',
    '.spm-panel{position:fixed;display:flex;flex-direction:column;overflow:hidden;border-radius:var(--radius-xl,16px);border:1px solid color-mix(in srgb,var(--border,#3a3a40) 65%,transparent);background:var(--card,#1c1c20);box-shadow:0 48px 96px -32px rgb(0 0 0/.9),inset 0 1px #ffffff0d;transition:left .28s cubic-bezier(.2,.8,.2,1),top .28s cubic-bezier(.2,.8,.2,1),width .28s cubic-bezier(.2,.8,.2,1),height .28s cubic-bezier(.2,.8,.2,1)}',
    '.spm-media{position:relative;flex:1 1 auto;min-height:0;overflow:hidden;background:#0b0b0d}',
    '.spm-poster{position:absolute;inset:0;width:100%;height:100%;object-fit:cover;transition:filter .35s ease,transform .35s ease}',
    '.spm-open .spm-poster{filter:blur(22px) brightness(.45) saturate(1.2);transform:scale(1.12)}',
    '.spm-video{position:absolute;inset:0;width:100%;height:100%;object-fit:contain;opacity:0;transition:opacity .3s ease}',
    '.spm-playing .spm-video{opacity:1}',
    '.spm-status{position:absolute;inset:0;display:grid;place-items:center;padding:24px;text-align:center;color:rgb(255 255 255/.85);font-size:.875rem;transition:opacity .2s ease}',
    '.spm-playing .spm-status{opacity:0}',
    '.spm-spinner{width:30px;height:30px;border:2px solid rgb(255 255 255/.22);border-top-color:#fff;border-radius:50%;animation:spm-spin .8s linear infinite}',
    '@keyframes spm-spin{to{transform:rotate(360deg)}}',
    '.spm-progress{position:absolute;left:0;bottom:0;height:3px;width:0;background:var(--primary,#e5484d);opacity:.9}',
    '.spm-caption{flex:none;display:flex;align-items:center;justify-content:space-between;gap:16px;height:60px;padding:0 20px;border-top:1px solid color-mix(in srgb,var(--border,#3a3a40) 45%,transparent);opacity:0;transition:opacity .2s ease .12s}',
    '.spm-open .spm-caption{opacity:1}',
    '.spm-title{min-width:0;overflow:hidden;text-overflow:ellipsis;white-space:nowrap;font-weight:600;font-size:1.0625rem;color:var(--foreground,#e8e8ec)}',
    '.spm-meta{flex:none;font-size:.6875rem;font-weight:500;letter-spacing:.14em;text-transform:uppercase;color:var(--muted-foreground,#9a9aa3)}',
    '@media (prefers-reduced-motion:reduce){.spm-panel,.spm-poster{transition:none}}',
  ].join('');
  const previewSources = new Map();
  let cardPreview = null;
  function previewSource(id) {
    if (!previewSources.has(id)) {
      const entry = (async () => {
        const paths = await sceneFor(id);
        if (!paths?.preview) throw new Error('This Stash scene has no generated preview video. Generate previews in Stash first.');
        return URL.createObjectURL(await stashRequest(paths.preview, 'blob'));
      })();
      // Failures are reported, never cached, so the next hover retries.
      entry.catch(() => previewSources.delete(id));
      previewSources.set(id, entry);
      while (previewSources.size > 12) {
        const [oldID, oldSrc] = previewSources.entries().next().value;
        if (oldID === cardPreview?.id) break;
        previewSources.delete(oldID);
        oldSrc.then(src => URL.revokeObjectURL(src), () => {});
      }
    }
    return previewSources.get(id);
  }
  function stopCardPreview() {
    const p = cardPreview; cardPreview = null;
    if (!p) return;
    clearTimeout(p.timer);
    const modal = p.modal;
    if (!modal) return;
    modal.classList.add('spm-closing');
    setTimeout(() => {
      const video = modal.querySelector('video');
      if (video) { video.pause(); video.removeAttribute('src'); video.load(); }
      modal.remove();
    }, 200);
  }
  function previewRect() {
    const vw = window.innerWidth, vh = window.innerHeight, caption = 60;
    const width = Math.round(Math.max(320, Math.min(vw * 0.76, (vh * 0.84 - caption) * 16 / 9, 1400)));
    const height = Math.round(width * 9 / 16 + caption);
    return { left: Math.round((vw - width) / 2), top: Math.round((vh - height) / 2), width, height };
  }
  function place(el, r) { Object.assign(el.style, { left: r.left + 'px', top: r.top + 'px', width: r.width + 'px', height: r.height + 'px' }); }
  function cardTitle(card) {
    return card.querySelector('a[aria-label]')?.getAttribute('aria-label') || card.querySelector('.media-card-image img')?.alt || card.textContent.trim().split('\n')[0] || '';
  }
  function openPreviewModal(p, image) {
    const modal = document.createElement('div');
    modal.dataset.stashPreviewModal = p.id;
    modal.setAttribute('aria-hidden', 'true');
    const backdrop = document.createElement('div'); backdrop.className = 'spm-backdrop';
    const panel = document.createElement('div'); panel.className = 'spm-panel';
    const mediaBox = document.createElement('div'); mediaBox.className = 'spm-media';
    const posterSrc = image.querySelector('img')?.currentSrc || image.querySelector('img')?.src;
    if (posterSrc) { const poster = document.createElement('img'); poster.className = 'spm-poster'; poster.src = posterSrc; poster.alt = ''; mediaBox.append(poster); }
    const status = document.createElement('div'); status.className = 'spm-status';
    const spinner = document.createElement('div'); spinner.className = 'spm-spinner'; status.append(spinner);
    const progress = document.createElement('div'); progress.className = 'spm-progress';
    mediaBox.append(status, progress);
    const caption = document.createElement('div'); caption.className = 'spm-caption';
    const title = document.createElement('div'); title.className = 'spm-title'; title.textContent = cardTitle(p.card);
    const meta = document.createElement('div'); meta.className = 'spm-meta'; meta.textContent = 'Stash preview';
    caption.append(title, meta);
    panel.append(mediaBox, caption);
    modal.append(backdrop, panel);
    // Start exactly over the poster, then expand to the centre.
    const from = image.getBoundingClientRect();
    place(panel, { left: from.left, top: from.top, width: from.width, height: from.height });
    document.body.append(modal);
    p.modal = modal;
    panel.getBoundingClientRect();
    requestAnimationFrame(() => { if (cardPreview !== p) return; modal.classList.add('spm-open'); place(panel, previewRect()); });
    return { modal, mediaBox, status, progress };
  }
  function startCardPreview(card, media) {
    const id = cardItemID(card), image = media.querySelector('.media-card-image');
    if (!id || !image) return;
    const p = cardPreview = { id, card, media, modal: null, timer: null };
    p.timer = setTimeout(async () => {
      if (cardPreview !== p || !card.isConnected) return;
      const view = openPreviewModal(p, image);
      try {
        const src = await previewSource(id);
        if (cardPreview !== p) return;
        const video = document.createElement('video');
        video.className = 'spm-video';
        video.muted = true; video.defaultMuted = true; video.loop = true; video.playsInline = true;
        video.addEventListener('timeupdate', () => { if (video.duration) view.progress.style.width = (video.currentTime / video.duration * 100) + '%'; });
        // Attach before playing: Firefox aborts playback of detached media.
        view.mediaBox.insertBefore(video, view.status);
        video.src = src;
        await video.play();
        if (cardPreview !== p) return;
        view.modal.classList.add('spm-playing');
      } catch (error) {
        if (cardPreview !== p) return;
        console.warn('[Silo Stash hover] poster preview failed', id, error);
        const name = error?.name && error.name !== 'Error' ? ' (' + error.name + ')' : '';
        view.status.textContent = (error?.message || 'The Stash preview could not be played.') + name;
      }
    }, delay());
  }
  window.addEventListener('mouseover', event => {
    const media = event.target?.closest?.('.media-card.group\\/card .group\\/media');
    if (!media || cardPreview?.media === media) return;
    stopCardPreview();
    if (!settings.enabled || !settings.stashURL || document.hidden || !previewGridPage(location.pathname)) return;
    startCardPreview(media.closest('.media-card'), media);
  }, true);
  window.addEventListener('mouseout', event => {
    if (cardPreview && !cardPreview.media.contains(event.relatedTarget)) stopCardPreview();
  }, true);
  document.addEventListener('visibilitychange', stopCardPreview);
  window.addEventListener('pagehide', stopCardPreview);
  window.addEventListener('scroll', stopCardPreview, { capture: true, passive: true });
  window.addEventListener('keydown', event => { if (event.key === 'Escape') stopCardPreview(); });
  function moreLikeThisSection() {
    return [...document.querySelectorAll('section.section-row h2')].find(h => h.textContent.trim() === 'More Like This')?.closest('section') || null;
  }
  function reconcileCollections() {
    const id = itemID(), hero = document.querySelector('.item-detail-hero');
    if (!id || !hero) { collectionState = null; document.querySelectorAll('[data-stash-collections]').forEach(el => el.remove()); return; }
    if (collectionState?.id !== id) {
      document.querySelectorAll('[data-stash-collections]').forEach(el => el.remove());
      const state = collectionState = { id, collections: null };
      collectionLookup(decodeURIComponent(id), new URLSearchParams(location.search).get('libraryId') || '').then(list => {
        if (collectionState !== state || itemID() !== id) return;
        state.collections = list;
        reconcileCollections();
      }).catch(() => {});
    }
    const list = collectionState.collections;
    const existing = document.querySelector('[data-stash-collections]');
    if (!list || !list.length) { existing?.remove(); return; }
    const mlt = moreLikeThisSection();
    const anchor = mlt || [...document.querySelectorAll('section.section-row')].filter(s => !s.hasAttribute('data-stash-collections')).pop();
    if (!anchor) return;
    if (existing && existing.dataset.stashCollectionsFor === id && existing.previousElementSibling === anchor) return;
    existing?.remove();
    const row = buildCollectionsRow(list, mlt);
    row.dataset.stashCollectionsFor = id;
    anchor.after(row);
  }
  function reconcile() {
    if (cardPreview && !cardPreview.card.isConnected) stopCardPreview();
    reconcileCardBadges();
    reconcilePersonO();
    reconcileReleaseDate();
    reconcileCollections();
    reconcileToolbar();
    const id = itemID(), hero = document.querySelector('.item-detail-hero');
    const backdrop = hero?.querySelector('.hero-backdrop-artwork');
    if (!id || !settings.enabled || !hero || !backdrop || (hero.dataset.variant && hero.dataset.variant !== 'full')) { dispose(); return; }
    if (current?.id === id && current.hero === hero && current.backdrop === backdrop) return;
    dispose(); current = attach(hero, backdrop, id);
  }
  let pending = false;
  new MutationObserver(() => { if (!pending) { pending = true; requestAnimationFrame(() => { pending = false; reconcile(); }); } }).observe(document.body, { childList: true, subtree: true });
  setInterval(() => { if (current?.id !== itemID()) reconcile(); }, 500);
  window.addEventListener('pagehide', () => { dispose(); toolbar?.dispose(); toolbar = null; });
  reconcile();
})();
