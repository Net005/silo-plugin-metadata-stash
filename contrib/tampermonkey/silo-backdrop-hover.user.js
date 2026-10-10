// ==UserScript==
// @name         Silo Stash Backdrop Hover
// @namespace    https://github.com/Net005/silo-plugin-metadata-stash
// @version      1.2.1
// @downloadURL  https://raw.githubusercontent.com/Net005/silo-plugin-metadata-stash/main/contrib/tampermonkey/silo-backdrop-hover.user.js
// @updateURL    https://raw.githubusercontent.com/Net005/silo-plugin-metadata-stash/main/contrib/tampermonkey/silo-backdrop-hover.user.js
// @description  Stash backdrop previews, native Watchlist and O-count toolbar actions, and library-scoped subtitle creation.
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
  async function personOCount(person, request) {
    const id = performerID(person);
    if (!id) return null;
    const data = await request('query($id:ID!){findPerformer(id:$id){o_counter}}', { id });
    const count = data?.findPerformer?.o_counter;
    return Number.isSafeInteger(count) && count > 0 ? count : null;
  }
  if (typeof module !== 'undefined' && module.exports) { module.exports = { performerID, personOCount, createOCounter, parseCues, exactScene, createSiloFileReader, stashOrigin, sceneID, subtitleEligible, confirmSubtitleOverwrite, fullReleaseDate, metadataFilterHref }; return; }
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
  function reconcile() {
    reconcilePersonO();
    reconcileReleaseDate();
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
