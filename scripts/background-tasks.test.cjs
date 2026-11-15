const {test}=require('node:test');const assert=require('node:assert/strict');const fs=require('node:fs');const {JSDOM}=require('jsdom');
function fixture(t,api,storage){const dom=new JSDOM('<div id="tasks"></div>',{url:'http://127.0.0.1/',runScripts:'outside-only'});dom.window.eval(fs.readFileSync('cmd/chameleon/webui/background-tasks.js','utf8'));t.after(()=>dom.window.close());const controller=new dom.window.DeskBackgroundTasks({api,container:dom.window.document.querySelector('#tasks'),interval:1,storage});return {controller,dom};}
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
