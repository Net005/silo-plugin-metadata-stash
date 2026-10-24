const test = require('node:test');
const assert = require('node:assert/strict');
const { createCardStateLoader, previewGridPage } = require('./silo-backdrop-hover.user.js');

function fixture({ played = {}, scenes = {}, counts = {}, failScene = new Set(), limit = 3 } = {}) {
  const calls = { items: [], scenes: [], counts: [] };
  let active = 0, peak = 0;
  const loader = createCardStateLoader({
    concurrency: limit,
    fetchItem: async id => {
      calls.items.push(id); active++; peak = Math.max(peak, active);
      await new Promise(r => setTimeout(r, 5)); active--;
      return { content_id: id, user_state: { played: !!played[id] } };
    },
    resolveScene: async id => {
      calls.scenes.push(id);
      if (failScene.has(id)) throw new Error('no exact match');
      return scenes[id];
    },
    readOCounts: async ids => { calls.counts.push([...ids].sort()); return Object.fromEntries(ids.map(s => [s, counts[s]])); },
  });
  return { loader, calls, peak: () => peak };
}

test('reads played state and batches Stash O counts for visible cards', async () => {
  const f = fixture({ played: { a: true }, scenes: { a: '1', b: '2', c: '3' }, counts: { 1: 4, 2: 0, 3: null } });
  const [a, b, c] = await Promise.all(['a', 'b', 'c'].map(id => f.loader.get(id)));
  assert.deepEqual(a, { played: true, oCount: 4 });
  assert.deepEqual(b, { played: false, oCount: 0 });
  assert.deepEqual(c, { played: false, oCount: 0 });
  assert.deepEqual(f.calls.counts, [['1', '2', '3']]);
});

test('caches results until invalidated after a watched toggle', async () => {
  const played = { a: false };
  const f = fixture({ played, scenes: { a: '1' }, counts: { 1: 2 } });
  assert.equal((await f.loader.get('a')).played, false);
  await f.loader.get('a');
  assert.equal(f.calls.items.length, 1);
  played.a = true;
  f.loader.invalidate('a');
  assert.equal((await f.loader.get('a')).played, true);
  assert.equal(f.calls.items.length, 2);
  assert.equal(f.calls.scenes.length, 1, 'scene identity is stable and resolved once');
});

test('keeps the watched state when no exact Stash scene exists', async () => {
  const f = fixture({ played: { a: true }, failScene: new Set(['a']) });
  assert.deepEqual(await f.loader.get('a'), { played: true, oCount: null });
});

test('rejects invalid O counts instead of drawing them', async () => {
  const f = fixture({ scenes: { a: '1' }, counts: { 1: -3 } });
  assert.deepEqual(await f.loader.get('a'), { played: false, oCount: null });
});

test('bounds concurrent Silo item reads', async () => {
  const ids = Array.from({ length: 12 }, (_, i) => 'i' + i);
  const f = fixture({ limit: 3 });
  await Promise.all(ids.map(id => f.loader.get(id)));
  assert.ok(f.peak() <= 3, 'peak ' + f.peak());
  assert.equal(f.calls.items.length, 12);
});

test('hover previews apply to collection grids and person filmographies only', () => {
  assert.equal(previewGridPage('/person/123', ''), true);
  assert.equal(previewGridPage('/catalog', '?source=library_collection&collection_id=1'), true);
  assert.equal(previewGridPage('/catalog', '?source=user_collection&collection_id=1'), true);
  assert.equal(previewGridPage('/catalog', '?source=section'), false);
  assert.equal(previewGridPage('/item/local-1', ''), false);
  assert.equal(previewGridPage('/person/123/extra', ''), false);
});
