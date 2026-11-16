const {test}=require('node:test');
const assert=require('node:assert/strict');
const fs=require('node:fs');
const {JSDOM}=require('jsdom');
const copy=value=>JSON.parse(JSON.stringify(value));
const available=id=>({schema_version:1,state:{id,state:'available',reasons:[]}});
const deferred=()=>{let resolve,reject;const promise=new Promise((a,b)=>{resolve=a;reject=b;});return {promise,resolve,reject};};
function fixture(t,handler,options={}){
 const dom=new JSDOM(fs.readFileSync('cmd/chameleon/webui/index.html','utf8'),{url:'http://127.0.0.1/',runScripts:'outside-only'});
 dom.window.eval(fs.readFileSync('cmd/chameleon/webui/reader-workflows.js','utf8'));t.after(()=>dom.window.close());
 const $=id=>dom.window.document.getElementById(id),calls=[],downloads=[];
 dom.window.Blob=global.Blob;dom.window.URL.createObjectURL=blob=>{downloads.push({blob});return 'blob:workflow';};dom.window.URL.revokeObjectURL=()=>{};dom.window.HTMLAnchorElement.prototype.click=function(){downloads.at(-1).name=this.download;};
 const features={device:{model_id:0},state:()=>({state:'available'})};
 const controller=new dom.window.DeskReaderWorkflows({features,post:async(path,value)=>{calls.push({path,value:copy(value)});return handler?handler(path,value):available(value.id);},run:async(_group,fn)=>fn(),...options});
 const select=kind=>{$('workflow-kind').value=kind;controller.template();};
 const input=value=>{$('workflow-json').value=JSON.stringify(value);$('workflow-json').dispatchEvent(new dom.window.Event('input'));};
 return {dom,$,calls,downloads,features,controller,select,input};
}
const type2Result=extra=>({schema:1,kind:'type2',complete:true,requested_units:4,units_read:4,data:'11'.repeat(16),...extra});

test('workflow templates retain typed KeyType values and bounded recovery fields',t=>{
 const p=fixture(t);
 p.select('classic-verify');assert.equal(p.controller.input().keys[0].key_type,96);
 p.select('classic-recover');const recovery=copy(p.controller.input());assert.equal(recovery.known_type,96);assert.equal(recovery.target_type,96);assert.equal(recovery.limit_seconds,10);assert.equal(recovery.max_runs,1);assert.equal(recovery.max_attempts,1);
 p.select('classic-dictionary');assert.equal(p.controller.input().sectors,16);assert(Array.isArray(p.controller.input().keys));
 p.select('lf-write');assert.equal(p.controller.input().target_isolated,false);assert.equal(p.controller.input().new_password,'00000000');
 p.select('classic-write');assert.equal(p.controller.input().write_trailers,false);assert.equal(p.controller.input().confirm,undefined);
});
test('capability denial shows its reason and prevents operation submission',async t=>{
 const p=fixture(t,(_path,value)=>({schema_version:1,state:{id:value.id,state:'unavailable',reasons:[{message:'missing command 2012'}]}}));
 p.select('classic-dictionary');await p.controller.check();await p.controller.read();
 assert.equal(p.$('workflow-read').disabled,true);assert.match(p.$('workflow-reason').textContent,/2012/);assert.equal(p.calls.length,1);
});
test('capability response must match the requested feature and current raw input',async t=>{
 const pending=deferred();const p=fixture(t,()=>pending.promise);
 const check=p.controller.check();p.$('workflow-json').value='{"uid":"new","full":true}';pending.resolve(available('reader.type2-workflow'));await check;
 assert.equal(p.$('workflow-read').disabled,true);await p.controller.read();assert.equal(p.calls.length,1);
 const q=fixture(t,()=>available('reader.classic-verify'));await assert.rejects(q.controller.check(),/格式无效/);assert.equal(q.$('workflow-read').disabled,true);
});
test('later capability response cannot override newer parameters or device state',async t=>{
 const first=deferred(),second=deferred();let count=0;const p=fixture(t,()=>++count===1?first.promise:second.promise);
 const old=p.controller.check();p.input({uid:'01020304',full:true});const latest=p.controller.check();second.resolve(available('reader.type2-workflow'));await latest;
 first.resolve({schema_version:1,state:{id:'reader.type2-workflow',state:'unavailable',reasons:[{message:'old'}]}});await old;assert.equal(p.$('workflow-read').disabled,false);
 p.features.device={model_id:1};p.controller.sync();assert.equal(p.$('workflow-read').disabled,true);
});
test('device changes clear credentials and discard late read results',async t=>{
 const pending=deferred();const p=fixture(t,(path,value)=>path==='/api/features/check'?available(value.id):pending.promise);
 p.input({uid:'01020304',full:true,password:'12345678'});await p.controller.check();const read=p.controller.read();
 p.features.device=null;p.dom.window.document.dispatchEvent(new p.dom.window.Event('desk-feature-device'));pending.resolve(type2Result());await read;
 assert.equal(p.$('workflow-json').value,'');assert.equal(p.$('workflow-result').textContent,'');assert.equal(p.$('workflow-export').disabled,true);assert.equal(p.$('workflow-import').disabled,true);
});
test('a new read clears the previous import source while its task runs',async t=>{
 const pending=deferred();const p=fixture(t,(path,value)=>path==='/api/features/check'?available(value.id):pending.promise,{importRead:async()=>{}});
 await p.controller.check();p.controller.show(type2Result());assert.equal(p.$('workflow-import').disabled,false);
 const read=p.controller.read();assert.equal(p.$('workflow-import').disabled,true);assert.equal(p.controller.result,null);
 pending.resolve(type2Result());await read;assert.equal(p.$('workflow-import').disabled,false);
});
test('input changes and device events discard outstanding plans and confirmations',async t=>{
 const pending=deferred();const p=fixture(t,(path,value)=>path==='/api/features/check'?available(value.id):pending.promise);
 p.select('classic-write');const plan=p.controller.makePlan();p.input({source:{changed:true}});pending.resolve({plan:{digest:'old'},confirmation:'old'});await plan;
 assert.equal(p.controller.plan,null);assert.equal(p.$('workflow-write').disabled,true);assert.equal(p.calls.filter(c=>c.path==='/api/features/check').length,0);
 const q=fixture(t,(path,value)=>path==='/api/features/check'?available(value.id):{plan:{digest:'reviewed'},confirmation:'receipt'});q.select('classic-write');await q.controller.makePlan();q.$('workflow-confirm').checked=true;q.controller.sync();assert.equal(q.$('workflow-write').disabled,false);
 q.$('workflow-json').dispatchEvent(new q.dom.window.Event('input'));assert.equal(q.controller.plan,null);assert.equal(q.$('workflow-confirm').checked,false);
 await q.controller.makePlan();q.dom.window.document.dispatchEvent(new q.dom.window.Event('desk-feature-device'));assert.equal(q.controller.plan,null);assert.equal(q.$('workflow-json').value,'');
});
test('late plan response is discarded if raw input changes without a DOM event',async t=>{
 const pending=deferred();const p=fixture(t,()=>pending.promise);p.select('type2-write');const plan=p.controller.makePlan();p.$('workflow-json').value='{"uid":"different"}';pending.resolve({plan:{page:6},confirmation:'receipt'});await plan;
 assert.equal(p.controller.plan,null);assert.equal(p.$('workflow-write').disabled,true);assert.equal(p.calls.length,1);
});
for(const kind of ['classic-write','type2-write','lf-write']){
 test(`${kind} uses the protocol-specific plan and execution confirmation shape`,async t=>{
  const p=fixture(t,(path,value)=>path==='/api/features/check'?available(value.id):path.endsWith('plan')?{plan:{digest:'reviewed-digest',blocks:[1]},confirmation:'signed-receipt'}:{complete:true});
  p.select(kind);const request=copy(p.controller.input());await p.controller.makePlan();assert.equal(p.$('workflow-write').disabled,true);
  const planCall=p.calls.find(c=>c.path.includes('plan'));assert.deepEqual(planCall.value,kind==='classic-write'?request:{request});
  p.$('workflow-confirm').checked=true;p.controller.sync();await p.controller.write();
  const writes=p.calls.filter(c=>c.path.endsWith('/write')||c.path.endsWith('-write'));assert.equal(writes.length,1);
  const expectedRequest=kind==='classic-write'?{...request,confirm:true,plan_digest:'reviewed-digest'}:request;
  assert.deepEqual(writes[0].value,{request:expectedRequest,confirmation:'signed-receipt',...(kind==='type2-write'?{confirm:true}:{})});
  assert.equal(p.controller.plan,null);assert.equal(p.$('workflow-confirm').checked,false);assert.equal(p.$('workflow-write').disabled,true);
 });
}
test('an uncertain write consumes its plan and is never retried automatically',async t=>{
 const p=fixture(t,(path,value)=>{if(path==='/api/features/check')return available(value.id);if(path.endsWith('plan'))return {plan:{digest:'reviewed'},confirmation:'receipt'};const error=Error('write response lost');error.result={outcome:'write_outcome_unknown',write_attempted:true};throw error;});
 p.select('classic-write');await p.controller.makePlan();p.$('workflow-confirm').checked=true;await assert.rejects(p.controller.write(),/lost/);
 assert.match(p.$('workflow-result').textContent,/write_outcome_unknown/);assert.equal(p.controller.plan,null);await assert.rejects(p.controller.write(),/重新生成/);
 assert.equal(p.calls.filter(c=>c.path==='/api/reader/classic-write').length,1);assert.equal(p.$('workflow-export').disabled,false);
});
test('a write cannot consume a reviewed plan after silent input or device changes',async t=>{
 const p=fixture(t,(path,value)=>path==='/api/features/check'?available(value.id):{plan:{digest:'reviewed'},confirmation:'receipt'});p.select('classic-write');await p.controller.makePlan();p.$('workflow-confirm').checked=true;
 p.features.device={model_id:0};await assert.rejects(p.controller.write(),/重新生成/);assert.equal(p.calls.filter(c=>c.path==='/api/reader/classic-write').length,0);
});
test('only current confirmed read bytes can be imported, with a stale-source guard',async t=>{
 let received,current;const p=fixture(t,null,{importRead:async(value,guard)=>{received=value;current=guard;}});
 p.controller.show(type2Result({complete:false,requested_units:8}),'interrupted');assert.equal(p.$('workflow-import').disabled,false);await p.controller.import();assert.equal(received.error,'interrupted');assert(current());
 p.$('workflow-json').value='{"changed":true}';assert(!current());await assert.rejects(p.controller.import(),/没有可导入/);
 p.controller.show(type2Result({data:'11'}));assert.equal(p.$('workflow-import').disabled,true);
 p.controller.show(type2Result({complete:true,units_read:3}));assert.equal(p.$('workflow-import').disabled,true);
 p.controller.show({schema:1,kind:'lf',complete:true,requested_units:1,units_read:0,present:false});assert.equal(p.$('workflow-import').disabled,true);
});
test('selected-block evidence exports an envelope and cannot import as a standard read',async t=>{
 const p=fixture(t,null,{importRead:async()=>{}});p.select('classic-repair');const selected={schema:1,identity:{schema:1,uid:'01020304',atqa:'0004',sak:9,ats:''},geometry_blocks:20,requested_blocks:[4],observations:[{block:4,data:'11'.repeat(16)}],complete:false,error:'cancelled'};
 p.controller.show(selected,'cancelled');assert.equal(p.$('workflow-import').disabled,true);p.controller.export();
 assert.deepEqual(JSON.parse(await p.downloads[0].blob.text()),{schema:1,result:selected,error:'cancelled'});
 p.input({changed:true});p.controller.export();assert.equal(p.downloads.length,1);
});
test('workflow results render as text and library policy prevents importing',t=>{
 const p=fixture(t,null,{importRead:async()=>{},libraryBusy:()=>true});p.controller.show(type2Result({note:'<img src=x onerror=alert(1)>'}));assert.equal(p.$('workflow-result').querySelector('img'),null);assert.equal(p.$('workflow-import').disabled,true);
});

test('workflow import validates before acquiring its own database busy group',async t=>{
 let databaseBusy=false,hardwareBusy=false,imports=0,guard;
 const p=fixture(t,null,{busy:()=>hardwareBusy,libraryBusy:()=>databaseBusy,run:async(group,action)=>{assert.equal(group,'database');assert.equal(databaseBusy,false);databaseBusy=true;try{return await action();}finally{databaseBusy=false;}},importRead:async(_source,current)=>{assert.equal(databaseBusy,true);imports++;guard=current;}});
 p.controller.show(type2Result());assert.equal(p.$('workflow-import').disabled,false);
 await p.$('workflow-import').onclick();assert.equal(imports,1);assert(guard());assert.equal(p.$('workflow-import').disabled,false);
 databaseBusy=true;await assert.rejects(p.controller.import(),/没有可导入/);await assert.rejects(p.$('workflow-import').onclick(),/没有可导入/);assert.equal(imports,1);databaseBusy=false;
 hardwareBusy=true;await assert.rejects(p.$('workflow-import').onclick(),/没有可导入/);assert.equal(imports,1);
});
test('the workflow import busy exemption cannot admit a replaced result',async t=>{
 let databaseBusy=false,imports=0;
 const p=fixture(t,null,{libraryBusy:()=>databaseBusy,run:async(_group,action)=>{databaseBusy=true;p.controller.invalidate();try{return await action();}finally{databaseBusy=false;}},importRead:async()=>{imports++;}});
 p.controller.show(type2Result());await assert.rejects(p.$('workflow-import').onclick(),/没有可导入/);assert.equal(imports,0);assert.equal(p.controller.importOwner,null);
});
