const {test}=require('node:test');const assert=require('node:assert/strict');const fs=require('node:fs');const {JSDOM}=require('jsdom');
const tick=()=>new Promise(resolve=>setImmediate(resolve));
function fixture(t,handler,options={}){
 const dom=new JSDOM(fs.readFileSync('cmd/chameleon/webui/index.html','utf8'),{url:'http://127.0.0.1/',runScripts:'outside-only'});t.after(()=>dom.window.close());
 dom.window.eval(fs.readFileSync('cmd/chameleon/webui/slot-reader.js','utf8'));
 const states=new Map([['library.database',{state:'available'}]]),calls=[],downloads=[],$=id=>dom.window.document.getElementById(id);
 const features={device:{model_id:0},states,state:id=>states.get(id)||{state:'unknown',reasons:[]},render(){},changed(){}};
 dom.window.Blob=global.Blob;dom.window.URL.createObjectURL=blob=>{downloads.push(blob);return 'blob:slot'};dom.window.URL.revokeObjectURL=()=>{};dom.window.HTMLAnchorElement.prototype.click=function(){};
 const controller=new dom.window.DeskSlotReader({features,post:async(path,value)=>{calls.push({path,value});return handler?handler(path,value):{schema_version:1,state:{id:value.id,state:'available',reasons:[]}}},run:async(_group,fn)=>fn(),...options});
 const observe=mode=>controller.observe({mode:mode||'emulator',slots:[{number:3,active:true,hf_type:1001,hf_name:'Classic 1K',lf_type:100,lf_name:'EM410x'}]});
 return {dom,$,controller,features,calls,downloads,observe};
}
function snapshot(extra={}){return {schema:1,kind:'slot',complete:false,start:0,requested_units:64,units_read:32,uid:'01020304',slot_snapshot:{schema:1,source:'emulator_slot',slot:3,sense:2,tag_type:1001,metadata_read:true,enabled:true,nickname:'<img src=x onerror=alert(1)>',identity:{schema:1,uid:'01020304',atqa:'0004',sak:8,ats:''},unit_bytes:16,units:64,units_read:32,data:'42'.repeat(512),unknown_ranges:[{start:32,count:32,reason:'not_read_or_unconfirmed'}],uncaptured:['emulator_configuration'],complete:false,source_changed:false,sha256:'AB'.repeat(32),...extra}}}
test('slot capability preview binds active slot/channel and refuses unknown or reader mode',async t=>{
 const p=fixture(t);assert.equal(p.$('slot-read-go').disabled,true);p.observe('reader');await tick();assert.equal(p.calls.length,0);assert.match(p.$('slot-read-go').disabled.toString(),/true/);assert.equal(p.features.state('slot.read-current').state,'unavailable');
 p.observe();await tick();assert.equal(p.$('slot-read-go').disabled,false);assert.deepEqual(JSON.parse(JSON.stringify(p.calls[0].value.slot_read)),{slot:3,sense:2,tag_type:1001});
 p.$('slot-read-sense').value='1';p.$('slot-read-sense').dispatchEvent(new p.dom.window.Event('change'));await tick();assert.equal(p.calls.at(-1).value.slot_read.tag_type,100);assert.equal(p.calls.at(-1).value.slot_read.sense,1);
});
test('missing firmware commands and native-maintenance policy keep reads disabled',async t=>{
 const p=fixture(t,(_path,value)=>({schema_version:1,state:{id:value.id,state:'unavailable',reasons:[{code:'missing_commands',message:'missing 4008',commands:[4008]}]}}));p.observe();await tick();assert.equal(p.$('slot-read-go').disabled,true);assert.equal(p.features.state('slot.read-current').reasons[0].commands[0],4008);
});
test('partial slot data preserves unknown rows, exports source and imports through existing library callback',async t=>{
 let imported;const p=fixture(t,async(path,value)=>{if(path==='/api/features/check')return {schema_version:1,state:{id:value.id,state:'available',reasons:[]}};const e=Error('interrupted');e.result=snapshot();throw e;},{importRead:async(value,current)=>{assert.equal(current(),true);imported=value;}});
 p.observe();await tick();await p.controller.read();assert.match(p.$('slot-read-result').textContent,/32 \/ 64/);assert.match(p.$('slot-read-result').textContent,/32: \?\?/);assert.equal(p.$('slot-read-result').querySelector('img'),null);assert.equal(p.$('slot-read-import').disabled,false);
 p.controller.export();const saved=JSON.parse(await p.downloads[0].text());assert.equal(saved.read_result.kind,'slot');assert.equal(saved.read_result.slot_snapshot.unknown_ranges[0].start,32);assert.equal(saved.error,'interrupted');
 await p.controller.import();assert.equal(imported.value.slot_snapshot.data,snapshot().slot_snapshot.data);assert.equal(p.calls.filter(c=>c.path==='/api/slot/read-current').length,1);assert.equal(p.calls.some(c=>c.path.includes('write')),false);
});
test('late source, capability and task responses cannot restore stale slot data',async t=>{
 let resolve;const p=fixture(t,(path,value)=>path==='/api/features/check'?{schema_version:1,state:{id:value.id,state:'available',reasons:[]}}:new Promise(r=>resolve=r));p.observe();await tick();const read=p.controller.read();p.controller.clear();resolve(snapshot());await read;assert.equal(p.controller.result,null);assert.equal(p.$('slot-read-result').textContent,'');assert.equal(p.$('slot-read-import').disabled,true);
 let resolvePreview;const q=fixture(t,()=>new Promise(r=>resolvePreview=r));q.observe();q.controller.clear();resolvePreview({schema_version:1,state:{id:'slot.read-current',state:'available',reasons:[]}});await tick();assert.equal(q.$('slot-read-go').disabled,true);
});
test('source changes, empty results and malformed response shapes do not enable import',async t=>{
 const p=fixture(t,null,{importRead:async()=>{throw Error('must not import')}});p.controller.show(snapshot({source_changed:true}));assert.equal(p.$('slot-read-import').disabled,true);assert.match(p.$('slot-read-result').textContent,/来源变化/);
 p.controller.show(snapshot({data:'00'}));assert.equal(p.controller.result,null);assert.equal(p.$('slot-read-export').disabled,true);
});
test('library/source/name changes invalidate an in-flight slot import guard',async t=>{
 let guard;const p=fixture(t,null,{importRead:async(_value,current)=>{guard=current}});p.controller.show(snapshot());await p.controller.import();assert.equal(guard(),true);p.$('slot-read-name').value='changed';assert.equal(guard(),false);p.controller.clear();assert.equal(guard(),false);
});
