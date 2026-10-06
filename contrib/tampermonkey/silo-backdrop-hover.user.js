// ==UserScript==
// @name         Silo Stash Backdrop Hover
// @namespace    https://github.com/Net005/silo-plugin-metadata-stash
// @version      1.0.2
// @downloadURL  https://raw.githubusercontent.com/Net005/silo-plugin-metadata-stash/main/contrib/tampermonkey/silo-backdrop-hover.user.js
// @updateURL    https://raw.githubusercontent.com/Net005/silo-plugin-metadata-stash/main/contrib/tampermonkey/silo-backdrop-hover.user.js
// @description  Muted Stash preview in Silo movie backdrops, with configurable delay and thumbnail fallback.
// @match        https://silo.example.invalid/*
// @connect      stash.example.invalid
// @grant        GM_getValue
// @grant        GM_setValue
// @grant        GM_registerMenuCommand
// @grant        GM_xmlhttpRequest
// @run-at       document-idle
// ==/UserScript==
(function () {
  'use strict';
  const STASH = 'https://stash.example.invalid';
  const DEFAULTS = { enabled: true, delay: 400, cycle: 700, stashKey: '', siloKey: '' };
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
  if (typeof module !== 'undefined' && module.exports) { module.exports = { parseCues, exactScene, createSiloFileReader }; return; }
  let settings = { ...DEFAULTS, ...GM_getValue('settings', {}) };
  let current = null;
  const overviewStyle = document.createElement('style');
  overviewStyle.textContent = '.item-detail-hero[data-stash-hover-preview] .detail-hero-description{visibility:hidden!important;pointer-events:none!important}';
  document.head.append(overviewStyle);
  const itemID = () => /^\/item\/([^/]+)$/.exec(location.pathname)?.[1];
  const delay = () => Math.max(0, Math.min(30000, Number(settings.delay) || 0));
  function notice(message) {
    const old = document.getElementById('silo-stash-hover-notice'); old?.remove();
    const el = document.createElement('div'); el.id = 'silo-stash-hover-notice'; el.textContent = message;
    Object.assign(el.style, { position: 'fixed', bottom: '24px', right: '24px', zIndex: '2147483647', background: '#232830', color: '#fff', padding: '16px', borderRadius: '8px', maxWidth: '440px', font: '14px/1.5 sans-serif' });
    document.body.append(el); setTimeout(() => el.remove(), 9000);
  }
  function save(next) { settings = next; GM_setValue('settings', next); dispose(); reconcile(); }
  function configure() {
    const dialog = document.createElement('dialog');
    dialog.innerHTML = `<form method="dialog" style="display:grid;gap:12px;width:min(460px,80vw);font:14px/1.5 sans-serif;color:inherit">
      <strong style="font-size:20px">Silo backdrop hover</strong>
      <label><input name="enabled" type="checkbox"> Enable preview</label>
      <label>Hover delay (milliseconds)<input name="delay" type="number" min="0" max="30000" step="100" style="display:block;width:100%"></label>
      <label>Thumbnail interval (milliseconds)<input name="cycle" type="number" min="100" max="10000" step="100" style="display:block;width:100%"></label>
      <label>Stash API key<input name="stashKey" type="password" autocomplete="off" style="display:block;width:100%"></label>
      <label>Silo admin API key (optional if your browser session works)<input name="siloKey" type="password" autocomplete="off" style="display:block;width:100%"></label>
      <small>Keys stay in Tampermonkey storage. Silo is read only; Stash is read only. 400 ms matches the companion’s default. Hover the background outside the title, poster and buttons.</small>
      <div><button value="save">Save</button> <button value="cancel">Cancel</button></div></form>`;
    Object.assign(dialog.style, { background: '#20242b', color: '#fff', padding: '24px', border: '1px solid #69717e', borderRadius: '12px' });
    const form = dialog.querySelector('form');
    for (const name of ['delay', 'cycle', 'stashKey', 'siloKey']) form.elements[name].value = settings[name];
    form.elements.enabled.checked = settings.enabled;
    dialog.addEventListener('close', () => {
      if (dialog.returnValue === 'save') save({ enabled: form.elements.enabled.checked, delay: Number(form.elements.delay.value), cycle: Number(form.elements.cycle.value), stashKey: form.elements.stashKey.value.trim(), siloKey: form.elements.siloKey.value.trim() });
      dialog.remove();
    });
    document.body.append(dialog); dialog.showModal();
  }
  GM_registerMenuCommand('Backdrop hover: settings / delay / API keys', configure);
  GM_registerMenuCommand('Backdrop hover: assign Stash scene for this item', () => {
    const id = itemID(); if (!id) return notice('Open a Silo movie first.');
    const value = prompt('Stash scene ID (or scene URL). Leave empty to use automatic exact file matching.', GM_getValue('scene:' + id, ''));
    if (value === null) return;
    const match = value.trim().match(/^(?:https:\/\/stash\.example\.invalid\/scenes\/)?(\d+)(?:[?#].*)?$/);
    if (value.trim() && !match) return notice('Enter a numeric Stash scene ID or its Stash scene URL.');
    GM_setValue('scene:' + id, match?.[1] || ''); dispose(); reconcile();
  });
  function allowed(raw) {
    const url = new URL(raw, STASH);
    if (url.origin !== STASH) throw new Error('Preview resource is not on your Stash server.');
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
    const result = await stashRequest(STASH + '/graphql', 'json', { query, variables });
    if (result.errors?.length) throw new Error('Stash lookup failed. Check the API key and scene ID.');
    return result.data;
  }
  const siloFiles = createSiloFileReader({ fetch: (...args) => fetch(...args), localStorage, sessionStorage, getKey: () => settings.siloKey, getLibrary: () => new URL(location.href).searchParams.get('libraryId') || '' });
  async function sceneFor(id) {
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
  function reconcile() {
    const id = itemID(), hero = document.querySelector('.item-detail-hero');
    const backdrop = hero?.querySelector('.hero-backdrop-artwork');
    if (!id || !settings.enabled || !hero || !backdrop || (hero.dataset.variant && hero.dataset.variant !== 'full')) { dispose(); return; }
    if (current?.id === id && current.hero === hero && current.backdrop === backdrop) return;
    dispose(); current = attach(hero, backdrop, id);
  }
  let pending = false;
  new MutationObserver(() => { if (!pending) { pending = true; requestAnimationFrame(() => { pending = false; reconcile(); }); } }).observe(document.body, { childList: true, subtree: true });
  setInterval(() => { if (current?.id !== itemID()) reconcile(); }, 500);
  window.addEventListener('pagehide', dispose);
  reconcile();
})();
