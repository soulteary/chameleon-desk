const {test}=require('node:test');
const assert=require('node:assert/strict');
const fs=require('node:fs');
const {JSDOM}=require('jsdom');
const captured=(action='lf-samples')=>({schema_version:1,kind:'rf_capture',action,session_id:'session-1',requested_ms:1,status:'captured',mode_before:'emulator',mode_after:'emulator',mode_restored:true,notes:['File declarations only'],file:{name:'capture.json',format:'lf-json',data:Buffer.from(JSON.stringify({rate_hz:125000,data:'00C800C8',requested_ms:1})).toString('hex')}});
function setup(t,handler=async()=>captured()){
 const d=new JSDOM(fs.readFileSync('cmd/chameleon/webui/index.html','utf8'),{url:'http://127.0.0.1/',runScripts:'outside-only'});t.after(()=>d.window.close());for(const name of ['rf-files.js','rf-capture.js'])d.window.eval(fs.readFileSync('cmd/chameleon/webui/'+name,'utf8'));
 const $=id=>d.window.document.getElementById(id),calls=[],features={device:{model_id:0},state:()=>({state:'unknown',reasons:[{message:'Select parameters'}]})};let session='session-1',impact=0;
 const post=async(path,body)=>{calls.push({path,body});return path==='/api/features/check'?{schema_version:1,state:{id:body.id,state:'available',reasons:[]}}:handler(path,body)};
 const files=new d.window.DeskRFFiles({post,api:async()=>{},features:{state:()=>({state:'available',reasons:[]})},run:async(_g,fn)=>fn()});
 const c=new d.window.DeskRFCapture({post,api:async path=>{calls.push({path});return handler(path)},features,run:async(_g,fn)=>fn(),session:()=>session,useFile:file=>files.useCapture(file),impact:()=>impact++});
 const ready=async(action='lf-samples')=>{$('rf-capture-action').value=action;$('rf-capture-duration').value='1';$('rf-capture-confirm').checked=true;c.invalidate();await c.check();};
 return {d,$,c,files,calls,features,ready,setSession:value=>session=value,get impacts(){return impact}};
}
test('capture submits once with explicit impact acknowledgment, and file handoff never reacquires or analyzes',async t=>{
 const p=setup(t);assert(p.$('rf-capture-go').disabled);await p.ready();await p.c.capture();assert.equal(p.calls.length,2);assert.equal(p.calls[1].path,'/api/rf/lf-capture');assert.equal(p.calls[1].body.acknowledge_slot_reload,true);assert.equal(p.impacts,1);assert.equal(p.$('rf-capture-confirm').checked,false);assert(p.$('rf-capture-go').disabled);
 p.$('rf-capture-analyze').click();assert.equal(p.calls.length,2);assert.equal(p.$('rf-format').value,'lf-json');assert.equal(p.$('rf-rate').value,'0');assert.equal(p.$('rf-files').open,true);assert.match(p.$('rf-status').textContent,/尚未执行/);assert.equal(p.files.hex,captured().file.data);assert.equal(p.$('rf-short-markers').checked,false);
});
test('late capture response after a session or parameter change cannot populate the current view',async t=>{
 let resolve;const p=setup(t,()=>new Promise(r=>resolve=r));await p.ready();const request=p.c.capture();p.setSession('session-2');p.d.window.document.dispatchEvent(new p.d.window.Event('desk-feature-device'));resolve(captured());await request;assert.equal(p.c.result,null);assert(p.$('rf-capture-analyze').disabled);assert.equal(p.impacts,0);
});
test('empty capture is explicit, cleanup failure retains confirmed bytes without retry',async t=>{
 const p=setup(t,async()=>({...captured(),status:'no_capture',file:undefined}));await p.ready();await p.c.capture();assert.match(p.$('rf-capture-status').textContent,/没有采集数据/);assert(p.$('rf-capture-analyze').disabled);assert.equal(p.calls.length,2);
 const error=Error('restore failed');error.result={...captured(),mode_after:undefined,mode_restored:false};const q=setup(t,async()=>{throw error});await q.ready();await assert.rejects(q.c.capture(),/restore failed/);assert.equal(q.c.result.file.data,captured().file.data);assert.equal(q.$('rf-capture-analyze').disabled,false);assert.match(q.$('rf-capture-status').textContent,/核对设备模式/);assert.equal(q.calls.length,2);
});
test('historical task query after reload restores file but never claims the current mode or replays capture',async t=>{
 const p=setup(t,async()=>({operation_id:'rf.lf-capture',session_id:'session-1',state:'succeeded',result_retained:true,result:captured()}));p.setSession('other-device');p.$('rf-capture-task').value='a'.repeat(32);await p.c.query();assert.deepEqual(p.calls,[{path:'/api/tasks/'+'a'.repeat(32)}]);assert.match(p.$('rf-capture-status').textContent,/不代表当前设备状态/);assert.equal(p.$('rf-capture-analyze').disabled,false);assert(p.$('rf-capture-go').disabled);
 const q=setup(t,async()=>({operation_id:'rf.lf-capture',state:'interrupted',result_retained:false}));q.$('rf-capture-task').value='b'.repeat(32);await assert.rejects(q.c.query(),/不会自动重新采集/);assert(q.$('rf-capture-analyze').disabled);assert.equal(q.calls.length,1);
});
test('keyed authentication is explicit and bound to its action; HF sniff uses its mutating operation',async t=>{
 const p=setup(t);p.$('rf-capture-action').value='hf-auth';await assert.rejects(p.c.check(),/已知密钥/);assert.equal(p.calls.length,0);p.$('rf-capture-key').value='010203040506';await p.ready('hf-auth');await p.c.capture();assert.equal(p.calls[1].path,'/api/rf/hf-auth');assert.equal(p.calls[1].body.key,'010203040506');
 const q=setup(t,async()=>captured('hf-sniff'));await q.ready('hf-sniff');await q.c.capture();assert.equal(q.calls[1].path,'/api/rf/hf-sniff');assert.equal(Object.hasOwn(q.calls[1].body,'key'),false);
});
test('ADC handoff keeps unknown rate and rejects oversized or malformed capture files',async t=>{
 const p=setup(t,async()=>({...captured('lf-adc'),file:{name:'adc.bin',format:'lf-u8',data:'00FF'}}));await p.ready('lf-adc');await p.c.capture();assert.equal(p.calls[1].body.duration_ms,0);p.$('rf-capture-analyze').click();assert.equal(p.$('rf-rate').value,'0');assert.equal(p.$('rf-format').value,'lf-u8');
 assert.throws(()=>p.files.useCapture({format:'lf-u8',data:'00'.repeat(1048577)}),/大小无效/);assert.throws(()=>p.c.show({...captured(),file:{format:'lf-u8',data:'00Z'}}),/无效/);
});
test('capture output and source names render as text; invalidation requires another capability check',async t=>{
 const p=setup(t);await p.ready();p.$('rf-capture-duration').value='2';p.$('rf-capture-duration').dispatchEvent(new p.d.window.Event('input'));assert(p.$('rf-capture-go').disabled);const v=captured();v.notes=['<img src=x onerror=alert(1)>'];p.c.show(v);assert.equal(p.$('rf-capture-result').querySelector('img'),null);assert.match(p.$('rf-capture-result').textContent,/<img/);
});
