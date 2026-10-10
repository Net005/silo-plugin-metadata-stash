const test = require('node:test');
const assert = require('node:assert/strict');
const { createSiloFileReader, stashOrigin, sceneID } = require('./silo-backdrop-hover.user.js');
const storage = data => ({ getItem: k => data[k] || null, setItem: (k,v) => data[k]=v });
const result = (status,data) => ({ status, ok: status>=200 && status<300, json: async()=>data });
test('expired browser token refreshes once and retries with fresh authentication', async()=>{
 const data={access_token:'expired',refresh_token:'refresh',profile_id:'profile',profile_token:'proof'},calls=[];
 const reader=createSiloFileReader({localStorage:storage(data),sessionStorage:storage({}),getKey:()=>'',getLibrary:()=>'21',fetch:async(url,opts)=>{
  calls.push([url,opts]);
  if(url.includes('/auth/refresh'))return result(200,{access_token:'fresh',refresh_token:'rotated'});
  if(opts.headers.Authorization==='Bearer expired')return result(401,{});
  assert.equal(opts.headers.Authorization,'Bearer fresh');assert.equal(opts.headers['X-Profile-Token'],'proof');
  return result(200,{items:[{library_id:'3',file_path:'/unrelated'},{library_id:'21',file_path:'/scene'}]});
 }});
 assert.deepEqual(await reader('shared'),[{library_id:'21',file_path:'/scene'}]);assert.equal(calls.length,3);assert.equal(data.refresh_token,'rotated');
});
test('invalid explicit API key does not refresh or overwrite browser login',async()=>{
 let calls=0;const data={access_token:'session',refresh_token:'refresh'};
 const reader=createSiloFileReader({localStorage:storage(data),sessionStorage:storage({}),getKey:()=>'bad-key',getLibrary:()=>'',fetch:async(url,opts)=>{calls++;assert.equal(opts.headers.Authorization,'Bearer bad-key');return result(401,{});}});
 await assert.rejects(reader('item'),/API key is invalid/);assert.equal(calls,1);assert.equal(data.access_token,'session');
});
test('paginated lookup keeps selected library files and rejects stalled cursor',async()=>{
 let calls=0;
 const reader=createSiloFileReader({localStorage:storage({}),sessionStorage:storage({}),getKey:()=>'api',getLibrary:()=>'21',fetch:async(url)=>{
  calls++;return result(200,{items:[{library_id:'21',file_path:'/scene'+calls}],page:{has_more:true,next_cursor:'same'}});
 }});
 await assert.rejects(reader('item'),/pagination stalled/);assert.equal(calls,2);
});

test('server configuration rejects credentials and scene URLs from other hosts', () => {
 assert.equal(stashOrigin('https://stash.example.invalid/'), 'https://stash.example.invalid');
 assert.throws(() => stashOrigin('https://user:password@stash.example.invalid'), /without credentials/);
 assert.throws(() => stashOrigin('https://stash.example.invalid/graphql'), /without credentials/);
 assert.equal(sceneID('42', ''), '42');
 assert.equal(sceneID('https://stash.example.invalid/scenes/42?q=1', 'https://stash.example.invalid'), '42');
 assert.equal(sceneID('https://other.example.invalid/scenes/42', 'https://stash.example.invalid'), null);
});

const { subtitleEligible, confirmSubtitleOverwrite } = require('./silo-backdrop-hover.user.js');
test('subtitle action uses exact configurable library names and hides current/unknown subtitles', () => {
 assert.equal(subtitleEligible(['JAV'], 'JAV', false, null), true);
 assert.equal(subtitleEligible(['JAV Archive'], 'JAV', false, null), false);
 assert.equal(subtitleEligible(['JAV'], '', false, null), false);
 assert.equal(subtitleEligible(['Other'], ' JAV, Other\nThird ', false, null), true);
 assert.equal(subtitleEligible(['JAV'], 'JAV', true, {sidecar_found:true,up_to_date:true}), false);
 assert.equal(subtitleEligible(['JAV'], 'JAV', true, {sidecar_found:true,up_to_date:false}), true);
 assert.equal(subtitleEligible(['JAV'], 'JAV', true, {sidecar_found:false}), true);
 assert.equal(subtitleEligible(['JAV'], 'JAV', true, {sidecar_found:true,up_to_date:null}), false);
 assert.equal(subtitleEligible(['JAV'], 'JAV', true, null), false);
});
test('replacement prompts match Stash, allow cancellation, and require two approvals for a current backend', async () => {
 const previous = global.window;
 try {
  const prompts=[]; let answer=false;
  global.window={confirm: text => {prompts.push(text);return answer;}};
  const operation = status => async input => {
   assert.equal(input.variables.args.mode,'subtitle_status');
   assert.equal(input.variables.args.scene_id,'42');
   return {data:{runPluginOperation:status}};
  };
  assert.equal(await confirmSubtitleOverwrite('42',operation({sidecar_found:false})),false);
  assert.match(prompts.pop(),/Old subtitles/);
  answer=true;
  assert.equal(await confirmSubtitleOverwrite('42',operation({sidecar_found:true,up_to_date:false,sidecar_backends:{transcription_backend:'old'},current_backends:{transcription_backend:'new'}})),true);
  assert.match(prompts.pop(),/old.*\nCurrent: new/s);
  assert.equal(await confirmSubtitleOverwrite('42',operation({sidecar_found:true,up_to_date:true})),true);
  assert.match(prompts.pop(),/FORCE OVERWRITE/);
  assert.match(prompts.pop(),/Already up to date/);
 } finally {global.window=previous;}
});

const { createOCounter } = require('./silo-backdrop-hover.user.js');
test('O counter reads existing or absent counts and adds exactly one through Stash history', async () => {
 for (const initial of [null, 0, 7]) {
  let count = initial, mutations = 0;
  const counter = createOCounter(async (query, variables) => {
   assert.deepEqual(variables, { id: '42' });
   if (query.startsWith('query')) return { findScene: { o_counter: count } };
   assert.equal(query, 'mutation($id:ID!){sceneAddO(id:$id){count}}');
   mutations++; count = (count || 0) + 1; return { sceneAddO: { count } };
  }, '42');
  assert.equal(await counter.read(), initial || 0);
  assert.equal(await counter.increment(), (initial || 0) + 1);
  assert.equal(mutations, 1);
 }
});
test('O counter suppresses concurrent clicks and never retries a failed mutation', async () => {
 let finish, calls = 0;
 const counter = createOCounter(() => { calls++; return new Promise(resolve => { finish = resolve; }); }, '42');
 const pending = counter.increment();
 assert.equal(await counter.increment(), null); assert.equal(calls, 1);
 finish({ sceneAddO: { count: 9 } }); assert.equal(await pending, 9);
 let failures = 0;
 const failing = createOCounter(async () => { failures++; throw new Error('timeout'); }, '42');
 await assert.rejects(failing.increment(), /timeout/); assert.equal(failures, 1);
});
test('O counter rejects missing scenes and invalid mutation responses', async () => {
 await assert.rejects(createOCounter(async () => ({ findScene: null }), '42').read(), /unavailable/);
 await assert.rejects(createOCounter(async () => ({ sceneAddO: null }), '42').increment(), /invalid O count/);
});

const { performerID, personOCount } = require('./silo-backdrop-hover.user.js');
test('person O count uses exact Stash identity, including legacy enriched homepages', () => {
 assert.equal(performerID({plex_guid:'stash:42'}),'42');
 assert.equal(performerID({provider_ids:{stash:'43'}}),'43');
 assert.equal(performerID({homepage:'https://beacon.example/api/v1/integrations/performers/44/stash'}),'44');
 assert.equal(performerID({name:'Same Name',plex_guid:'tmdb:42'}),'');
 assert.equal(performerID({homepage:'https://example/performers/44'}),'');
});
test('person O badge hides missing, zero and invalid counts without guessing identities', async () => {
 let calls=0;
 assert.equal(await personOCount({name:'Unknown'},()=>{calls++;}),null);
 assert.equal(calls,0);
 for (const value of [null,undefined,0,-1,'3',1.5]) {
  assert.equal(await personOCount({plex_guid:'stash:42'},async(query,variables)=>{
   assert.match(query,/findPerformer/); assert.deepEqual(variables,{id:'42'});
   return {findPerformer:{o_counter:value}};
  }),null);
 }
 assert.equal(await personOCount({plex_guid:'stash:42'},async()=>({findPerformer:{o_counter:7}})),7);
});
