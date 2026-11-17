const {test}=require('node:test');const assert=require('node:assert/strict');const fs=require('node:fs');const {JSDOM}=require('jsdom');
function fixture(t,api,storage){const dom=new JSDOM('<div id="tasks"></div>',{url:'http://127.0.0.1/',runScripts:'outside-only'});dom.window.eval(fs.readFileSync('cmd/chameleon/webui/background-tasks.js','utf8'));t.after(()=>dom.window.close());const controller=new dom.window.DeskBackgroundTasks({api,container:dom.window.document.querySelector('#tasks'),interval:1,storage,operations:require("../internal/operations/catalog.json").operations.map(o=>({...o,retry_policy:"never"}))});return {controller,dom};}
const task=(extra={})=>({id:'task-1',request_id:'receipt-1',session_id:'session',path:'/api/slot/activate',write:true,state:'queued',phase:'queued',outcome:'not_started',commands_attempted:0,responses_received:0,...extra});
test('task client submits once and polls progress and terminal results',async t=>{
 const calls=[];let reads=0;const result={slot:1};const api=async(path,opts)=>{calls.push({path,opts});if(path==='/api/tasks'&&!opts)return {server_id:'server',tasks:reads?[task({state:'succeeded',result})]:[]};if(opts?.method==='POST')return task();reads++;return reads===1?task({state:'running',commands_attempted:1}):task({state:'succeeded',result});};
 const {controller,dom}=fixture(t,api);const got=await controller.execute('/api/slot/activate',{headers:{'X-Chameleon-Session':'session'},body:'{"slot":1}'});assert.deepEqual(got,result);assert.equal(calls.filter(x=>x.opts?.method==='POST').length,1);assert.match(dom.window.document.body.textContent,/已完成/);
});
test('ambiguous submission and server restart never resubmit a write',async t=>{
 let posts=0;const receipts=new Map();const storage={getItem:k=>receipts.get(k),setItem:(k,v)=>receipts.set(k,v)};
 const first=fixture(t,async(path,opts)=>{if(opts){posts++;throw Error('lost response')}return {server_id:'old',tasks:[]}},storage);
 await assert.rejects(()=>first.controller.execute('/api/slot/activate',{headers:{'X-Chameleon-Session':'session'},body:'{"slot":1}'}),/不会自动重新提交/);
 const reload=fixture(t,async()=>({server_id:'new',tasks:[]}),storage);await reload.controller.refresh();assert.match(reload.dom.window.document.body.textContent,/服务已重启.*写入结果无法确认/);assert.equal(posts,1);
});
test('refreshed page queries existing tasks, exposes cancel and retains unknown write details',async t=>{
 const calls=[];let canceled=false;const {controller,dom}=fixture(t,async(path,opts)=>{calls.push({path,opts});if(opts){canceled=true;return task({cancel_requested:true})}return {server_id:'server',tasks:[task(canceled?{state:'interrupted',outcome:'write_outcome_unknown',cancel_requested:true,result:{error:'timeout',result:{completed_steps:2}}}:{state:'running'})]};});
 await controller.refresh();dom.window.document.querySelector('button').click();await new Promise(r=>setTimeout(r,10));assert.equal(calls.filter(c=>c.opts?.method==='POST').length,1);assert.equal(calls.find(c=>c.opts)?.path,'/api/tasks/task-1/cancel');assert.match(dom.window.document.body.textContent,/写入结果未知/);assert.match(dom.window.document.body.textContent,/completed_steps/);assert.equal(dom.window.document.querySelector('button'),null);
});
test('old-session results remain task history and are rendered as text',async t=>{
 const {controller,dom}=fixture(t,async()=>({server_id:'server',tasks:[task({session_id:'old',state:'succeeded',path:'<img src=x onerror=alert(1)>',result:{text:'<script>bad()</script>'}})]}));await controller.refresh();assert.equal(dom.window.document.querySelector('img,script'),null);assert.match(dom.window.document.body.textContent,/会话 old/);
});
test('unavailable receipt storage does not block querying or explicit cancellation',async t=>{
 const storage={getItem(){throw Error('blocked')},setItem(){throw Error('blocked')}};const {controller,dom}=fixture(t,async()=>({server_id:'server',tasks:[]}),storage);await controller.refresh();await controller.refresh();assert.match(dom.window.document.body.textContent,/无法保存本页任务收据/);
});
test('metadata polling fetches a full result only on explicit query',async t=>{
 const calls=[];const snapshot=task({state:'succeeded'});const {controller,dom}=fixture(t,async path=>{calls.push(path);if(path==='/api/tasks/task-1')return {...snapshot,result:{verified:true}};return {server_id:'server',tasks:[snapshot]};});
 await controller.refresh();await controller.refresh();assert.deepEqual(calls,['/api/tasks','/api/tasks']);
 dom.window.document.querySelector('button').click();await new Promise(r=>setTimeout(r,10));assert.match(dom.window.document.body.textContent,/verified/);await controller.refresh();assert.equal(calls.filter(p=>p==='/api/tasks/task-1').length,1);assert.equal(dom.window.document.querySelector('details').open,true);
});
test('corrupt receipt entries cannot prevent history queries',async t=>{
 const storage={getItem:()=>JSON.stringify([null,{},'bad',{requestID:'old',serverID:'old',write:true}]),setItem(){}};
 const {controller,dom}=fixture(t,async()=>({server_id:'new',tasks:[]}),storage);await controller.refresh();assert.match(dom.window.document.body.textContent,/写入结果无法确认/);
});
test('definitive rejection is not reported as an ambiguous write',async t=>{
 const {controller}=fixture(t,async(path,opts)=>{if(opts){const e=Error('writes disabled');e.status=403;throw e;}return {server_id:'server',tasks:[]};});await assert.rejects(()=>controller.execute('/api/slot/activate',{headers:{'X-Chameleon-Session':'session'},body:'{"slot":1}'}),/任务未提交/);assert.equal(controller.pending.length,0);
});
test('unknown write receipts remain after terminal query and can be acknowledged after restart',async t=>{
 const storage=new Map();const store={getItem:k=>storage.get(k),setItem:(k,v)=>storage.set(k,v)};
 store.setItem('desk.pending-tasks',JSON.stringify([{serverID:'old',requestID:'receipt-1',write:true}]));
 const first=fixture(t,async()=>({server_id:'old',tasks:[task({state:'interrupted',outcome:'write_outcome_unknown'})]}),store);await first.controller.refresh();assert.equal(first.controller.pending.length,1);
 const next=fixture(t,async()=>({server_id:'new',tasks:[]}),store);await next.controller.refresh();assert.match(next.dom.window.document.body.textContent,/写入结果无法确认/);next.dom.window.document.querySelector('button').click();assert.equal(next.controller.pending.length,0);
});
test('configuration receipts distinguish setting writes from slot-detail reads',async t=>{
 for(const [path,write]of [['/api/settings/configure',true],['/api/slot/edit',true],['/api/slot/details',false]]){const {controller}=fixture(t,async(_path,opts)=>{if(opts)throw Error('lost response');return {server_id:'server',tasks:[]};});await assert.rejects(()=>controller.execute(path,{headers:{'X-Chameleon-Session':'session'}}));assert.equal(controller.pending[0].write,write);}
});
test('physical write receipts retain uncertainty while plans remain read tasks',async t=>{
 for(const [path,write]of [['/api/reader/classic-write',true],['/api/reader/type2-write',true],['/api/physical/lf/write',true],['/api/reader/classic-write-plan',false],['/api/reader/type2-write-plan',false],['/api/physical/lf/plan',false]]){
  const {controller}=fixture(t,async(_path,opts)=>{if(opts)throw Error('lost response');return {server_id:'server',tasks:[]};});
  await assert.rejects(()=>controller.execute(path,{headers:{'X-Chameleon-Session':'session'},body:'{}'}));assert.equal(controller.pending[0].write,write,path);
 }
});
test('an older list response cannot restore a previous server identity',async t=>{
 let resolveOld,calls=0;const {controller}=fixture(t,async()=>{if(++calls===1)return new Promise(resolve=>resolveOld=resolve);return {server_id:'new',tasks:[]};});
 const old=controller.refresh();await controller.refresh();resolveOld({server_id:'old',tasks:[task()]});await old;assert.equal(controller.serverID,'new');assert.equal(controller.visibleTasks.size,0);
});
test('a delayed result from a previous server is discarded',async t=>{
 let resolveResult,server='old';const snapshot=task({path:'/api/reader/type2-workflow',write:false,state:'succeeded'});
 const {controller,dom}=fixture(t,async path=>path==='/api/tasks/task-1'?new Promise(resolve=>resolveResult=resolve):{server_id:server,tasks:server==='old'?[snapshot]:[]});
 await controller.refresh();dom.window.document.querySelector('button').click();server='new';await controller.refresh();resolveResult({...snapshot,result:{schema:1,kind:'type2',units_read:1}});await new Promise(resolve=>setTimeout(resolve,10));
 assert.equal(controller.results.size,0);assert.equal(controller.serverID,'new');assert.doesNotMatch(dom.window.document.body.textContent,/导入读取结果/);
});
test('a removed result fails its in-flight import guard',async t=>{
 const value={schema:1,kind:'type2',units_read:1};let tasks=[task({path:'/api/reader/type2-workflow',write:false,state:'succeeded',result:value})],current;
 const {controller,dom}=fixture(t,async()=>({server_id:'server',tasks}));controller.onReaderResult=async(_value,guard)=>{current=guard;};
 await controller.refresh();[...dom.window.document.querySelectorAll('button')].find(button=>button.textContent.includes('导入')).click();await new Promise(resolve=>setTimeout(resolve,0));assert.equal(current(),true);
 tasks=[];await controller.refresh();assert.equal(current(),false);
});
test('durable restart restores metadata without offering result recovery or replay',async t=>{
 const storage={getItem:()=>JSON.stringify([{serverID:'old',requestID:'receipt-1',write:true}]),setItem(){}};
 const calls=[];const {controller,dom}=fixture(t,async path=>{calls.push(path);return {server_id:'new',tasks:[task({state:'interrupted',outcome:'write_outcome_unknown',recovered:true,result_retained:false})]}},storage);
 await controller.refresh();assert.match(dom.window.document.body.textContent,/从持久记录恢复/);assert.match(dom.window.document.body.textContent,/结果正文未保留/);assert.match(dom.window.document.body.textContent,/写入结果未知/);assert.doesNotMatch(dom.window.document.body.textContent,/服务已重启、回执丢失/);assert.equal(dom.window.document.querySelectorAll('button').length,0);assert.deepEqual(calls,['/api/tasks']);assert.equal(controller.pending.length,1);
});
test('invalid local JSON and unregistered operations leave no acceptance receipt',async t=>{
 const calls=[];const {controller}=fixture(t,async path=>{calls.push(path);return {server_id:'server',tasks:[]}});
 await assert.rejects(()=>controller.execute('/api/slot/activate',{headers:{'X-Chameleon-Session':'session'},body:'{'}));
 await assert.rejects(()=>controller.execute('/api/arbitrary',{headers:{'X-Chameleon-Session':'session'},body:'{}'}),/未登记/);
 assert.equal(controller.pending.length,0);assert.equal(calls.length,0);
});
test('journal failure is visible and operation metadata sets write receipts',async t=>{
 const {controller,dom}=fixture(t,async()=>({server_id:'server',journal_error:'disk failed',tasks:[]}));await controller.refresh();assert.match(dom.window.document.body.textContent,/任务记录保存失败/);
 assert.equal(controller.operation('/api/reader/classic-write').write,true);assert.equal(controller.operation('/api/reader/classic-write-plan').write,false);
 assert.throws(()=>controller.configureOperations([{id:'raw',path:'/api/raw',method:'POST',write:true,retry_policy:'automatic'}]),/清单无效/);
});
test('historical slot snapshots import through the read-library callback and source changes remain blocked',async t=>{
 let got;const value={schema:1,kind:'slot',units_read:1,slot_snapshot:{source_changed:false}};
 const p=fixture(t,async()=>({server_id:'server',tasks:[task({path:'/api/slot/read-current',write:false,state:'failed',outcome:'read_interrupted',result:{error:'partial',result:value}})]}));p.controller.onReaderResult=async(result,current)=>{got=result;assert.equal(current(),true)};
 await p.controller.refresh();const button=[...p.dom.window.document.querySelectorAll('button')].find(b=>b.textContent.includes('导入'));assert(button);button.click();await new Promise(resolve=>setImmediate(resolve));assert.equal(got.value.kind,'slot');assert.equal(got.error,'partial');
 value.slot_snapshot.source_changed=true;await p.controller.refresh();assert.equal([...p.dom.window.document.querySelectorAll('button')].some(b=>b.textContent.includes('导入')),false);
});

test('offline registered operations need no device session and submit exactly once',async t=>{
 const {controller}=fixture(t,async(path,options)=>{if(path==='/api/tasks'&&options){const value=JSON.parse(options.body);assert.equal(value.session_id,'offline');return {id:'task-1',state:'succeeded',result:{kind:'offline_rf'}};}return {server_id:'server',tasks:[]};});
 controller.configureOperations([{id:'files.rf-analyze',path:'/api/files/rf/analyze',method:'POST',write:false,retry_policy:'never',session_required:false,scope:'offline'}]);
 const value=await controller.execute('/api/files/rf/analyze',{body:'{"format":"lf-u8","data":"00"}'});assert.equal(value.kind,'offline_rf');
});
