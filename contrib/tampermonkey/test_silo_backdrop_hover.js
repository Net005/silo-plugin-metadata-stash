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
