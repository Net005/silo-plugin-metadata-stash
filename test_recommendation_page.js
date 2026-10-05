const {test}=require('node:test');
const assert=require('node:assert/strict');
const fs=require('node:fs');
const vm=require('node:vm');
const source=fs.readFileSync('recommendation_page.go','utf8');
const auth=source.slice(source.indexOf(' function sessionHeaders()'),source.indexOf(' async function load()'));
test('expired plugin cookie is renewed from Silo login before reading private report',async()=>{
 const calls=[];
 const context={base:'/api/v2/plugin-content/plugins/15/recommendations',localStorage:{getItem:k=>({access_token:'login-session',profile_id:'owner',profile_token:'profile-proof'})[k]||null},fetch:async(url,opts)=>{calls.push({url,opts});return calls.length===1?{status:401,json:async()=>({})}:url==='/api/v2/auth/plugin-launch'?{ok:true}:{status:200,ok:true,json:async()=>({report:{status:'complete'}})}}};
 vm.createContext(context);vm.runInContext(auth,context);
 const report=await context.request('report');assert.equal(report.report.status,'complete');assert.equal(calls.length,3);
 assert.equal(calls[1].opts.headers.Authorization,'Bearer login-session');assert.equal(calls[1].opts.headers['X-Profile-Token'],'profile-proof');assert.equal(calls[1].opts.credentials,'same-origin');assert.ok(calls.every(c=>!c.url.includes('login-session')));
});
test('anonymous viewer cannot read report and receives sign-in guidance',async()=>{
 let calls=0;const context={base:'/api/v2/plugin-content/plugins/15/recommendations',localStorage:{getItem:()=>null},fetch:async()=>{calls++;return {status:401,json:async()=>{throw Error("Unauthorized is not JSON")}}}};vm.createContext(context);vm.runInContext(auth,context);
 await assert.rejects(context.request('report'),/administrator/);assert.equal(calls,1);
});
test('only the empty HTML shell is public; report data and mutations require admin',()=>{
 const manifest=JSON.parse(fs.readFileSync('manifest.json','utf8'));
 for(const r of manifest.http_routes){if(r.path==='/recommendations')assert.equal(r.access,'public');else assert.equal(r.access,'admin');if(r.navigable)assert.notEqual(r.access,'public')}
});

test('report requests use the signed-in bearer even without a launch cookie',async()=>{let parsed=false;const context={base:'/recommendations',localStorage:{getItem:k=>k==='access_token'?'admin-token':null},fetch:async(u,o)=>{assert.equal(o.headers.Authorization,'Bearer admin-token');return{ok:true,status:200,json:async()=>{parsed=true;return{report:{status:'complete'}}}}}};vm.createContext(context);vm.runInContext(auth,context);await context.request('report');assert.ok(parsed)});
test('plain-text permission denial is checked before attempting JSON',async()=>{const context={base:'/recommendations',localStorage:{getItem:()=>null},fetch:async()=>({status:403,json:async()=>{throw Error('JSON parser must not be called')}})};vm.createContext(context);vm.runInContext(auth,context);await assert.rejects(context.request('report'),/profile is locked/)});
test('expired login refreshes tokens, renews launch and retries with the fresh bearer',async()=>{const storage={access_token:'expired',refresh_token:'renew'};const calls=[];const context={base:'/recommendations',localStorage:{getItem:k=>storage[k]||null,setItem:(k,v)=>storage[k]=v},fetch:async(u,o)=>{calls.push(u);if(u==='/api/v2/auth/refresh')return{ok:true,json:async()=>({access_token:'fresh',refresh_token:'rotated'})};if(u==='/api/v2/auth/plugin-launch')return{status:storage.access_token==='fresh'?200:401,ok:storage.access_token==='fresh'};if(storage.access_token==='expired')return{status:401};assert.equal(o.headers.Authorization,'Bearer fresh');return{status:200,ok:true,json:async()=>({report:{status:'complete'}})}}};vm.createContext(context);vm.runInContext(auth,context);await context.request('report');assert.equal(storage.refresh_token,'rotated');assert.equal(calls.length,5)});
