const test = require('node:test');
const assert = require('node:assert/strict');
const { collectionHref, createCollectionIndex } = require('./silo-backdrop-hover.user.js');

// The server listing is only a capped preview; full lists come per library.
const server = { libraries: [{ library_id: 7, total_count: 3, collections: [{ id: '10', title: 'First' }] }, { library_id: 8, total_count: 1, collections: [] }] };
const perLibrary = {
  7: { library_id: 7, collections: [
    { id: '20', title: 'Second & Co', sort_order: 2, poster_url: 'https://img.example.invalid/20.webp' },
    { id: '10', title: 'First', sort_order: 1 },
    { id: '30', title: 'Elsewhere', sort_order: 3 },
  ] },
  8: { library_id: 8, collections: [{ id: '40', title: 'Other Library', sort_order: 1 }] },
};
const members = { '10': ['ABC-1', 'XYZ-9'], '20': ['ABC-1'], '30': ['XYZ-9'], '40': ['ABC-1'], 'u1': ['ABC-1'] };
function fixture(overrides = {}) {
  const calls = [];
  const fetchJSON = async path => {
    calls.push(path);
    if (overrides[path]) return overrides[path]();
    if (path === '/api/v1/collections/server') return server;
    if (path === '/api/v2/collections') return { items: [{ id: 'u1', title: 'Mine', poster_url: '' }], groups: [] };
    let m = /^\/api\/v1\/library\/(\d+)\/collections$/.exec(path);
    if (m) return perLibrary[m[1]];
    m = /^\/api\/v1\/library\/\d+\/collections\/([^/]+)\/items$/.exec(path) || /^\/api\/v1\/collections\/([^/]+)\/items$/.exec(path);
    if (m) return { items: members[m[1]].map(content_id => ({ content_id })), has_more: false };
    throw new Error('unexpected ' + path);
  };
  return { calls, fetchJSON };
}

test('collection links use the same catalog route Silo builds for library and personal collections', () => {
  assert.equal(collectionHref({ id: '10', title: 'A & B', library_id: 7, kind: 'library' }),
    '/catalog?source=library_collection&collection_id=10&title=A+%26+B&library_id=7');
  assert.equal(collectionHref({ id: 'u1', title: 'Mine', kind: 'user' }),
    '/catalog?source=user_collection&collection_id=u1&title=Mine');
});

test('lists every collection in the viewed library containing the release, in Silo order, beyond the capped preview', async () => {
  const { calls, fetchJSON } = fixture();
  const lookup = createCollectionIndex({ fetchJSON });
  const found = await lookup('ABC-1', '7');
  assert.deepEqual(found.map(c => [c.title, c.href, c.poster_url || '']), [
    ['First', '/catalog?source=library_collection&collection_id=10&title=First&library_id=7', ''],
    ['Second & Co', '/catalog?source=library_collection&collection_id=20&title=Second+%26+Co&library_id=7', 'https://img.example.invalid/20.webp'],
    ['Mine', '/catalog?source=user_collection&collection_id=u1&title=Mine', ''],
  ]);
  assert.ok(!calls.includes('/api/v1/library/8/collections'), 'other libraries are not read when one is selected');
  assert.deepEqual(await lookup('NOT-IN-ANY', '7'), []);
});

test('without a library context every library is searched', async () => {
  const { fetchJSON } = fixture();
  const found = await createCollectionIndex({ fetchJSON })('ABC-1', '');
  assert.deepEqual(found.map(c => c.title), ['First', 'Second & Co', 'Other Library', 'Mine']);
});

test('membership is fetched once per library and reused until it expires', async () => {
  let now = 0;
  const { calls, fetchJSON } = fixture();
  const lookup = createCollectionIndex({ fetchJSON, ttl: 1000, now: () => now });
  await Promise.all([lookup('ABC-1', '7'), lookup('XYZ-9', '7')]);
  const first = calls.length;
  assert.equal(first, 6); // library list + 3 collections + personal list + 1 personal collection
  await lookup('XYZ-9', '7');
  assert.equal(calls.length, first);
  now = 5000;
  await lookup('XYZ-9', '7');
  assert.equal(calls.length, first * 2);
});

test('one unreadable collection is skipped, not cached as empty forever', async () => {
  let failing = true;
  const { calls, fetchJSON } = fixture({ '/api/v1/library/7/collections/20/items': () => { if (failing) throw new Error('HTTP 500'); return { items: [{ content_id: 'ABC-1' }] }; } });
  const lookup = createCollectionIndex({ fetchJSON, ttl: 60000, now: () => 0 });
  assert.deepEqual((await lookup('ABC-1', '7')).map(c => c.title), ['First', 'Mine']);
  failing = false;
  const before = calls.length;
  assert.deepEqual((await lookup('ABC-1', '7')).map(c => c.title), ['First', 'Second & Co', 'Mine']);
  assert.ok(calls.length > before, 'partial index must be rebuilt');
});

test('a failing collection list surfaces an error instead of claiming no collections', async () => {
  const { fetchJSON } = fixture({ '/api/v1/library/7/collections': () => { throw new Error('HTTP 401'); } });
  await assert.rejects(createCollectionIndex({ fetchJSON })('ABC-1', '7'), /HTTP 401/);
});
