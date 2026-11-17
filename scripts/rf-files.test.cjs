const {test}=require('node:test');
const assert=require('node:assert/strict');
const fs=require('node:fs');
const {JSDOM}=require('jsdom');
const tick=()=>new Promise(resolve=>setImmediate(resolve));
const lf=()=>({schema_version:1,kind:'offline_rf',complete:true,parameters:{start:0,count:0,clock:64,phase:0,decode:false,invert:false,rate_hz:0},source:{name:'capture.bin',format:'lf-u8',bytes:100,sha256:'a'.repeat(64)},notes:['Declarations only'],issues:[],lf:{total:100,start:0,rate_hz:0,rate_source:'unknown',data:'00'.repeat(100),analysis:{min:0,max:0,mean:0,threshold:0,transitions:0,heuristic_hint:'flat'},rf_heuristic_available:false,buckets:[{start:0,end:100,min:0,max:0}]}});
function setup(t,handler=async()=>lf(),state='available'){
 const dom=new JSDOM(fs.readFileSync('cmd/chameleon/webui/index.html','utf8'),{url:'http://127.0.0.1/',runScripts:'outside-only'});t.after(()=>dom.window.close());dom.window.eval(fs.readFileSync('cmd/chameleon/webui/rf-files.js','utf8'));const calls=[];
 const c=new dom.window.DeskRFFiles({post:async(path,body)=>{calls.push({path,body});return handler(path,body)},api:async path=>{calls.push({path});return handler(path)},features:{state:()=>({state,reasons:[{message:'catalog missing'}]})},run:async(_g,fn)=>fn()});
 const select=async(bytes=new Uint8Array(100),name='capture.bin')=>{const file={name,size:bytes.length,arrayBuffer:async()=>bytes.buffer};Object.defineProperty(dom.window.document.getElementById('rf-file'),'files',{configurable:true,value:[file]});await c.loadFile();};
 return {dom,c,calls,select,$:id=>dom.window.document.getElementById(id)};
}
test('LF workbench submits explicit file bytes without device calls and paginates only visible rows',async t=>{
 const p=setup(t);p.$('rf-format').value='lf-u8';await p.select();await p.c.analyze();assert.equal(p.calls.length,1);assert.equal(p.calls[0].path,'/api/files/rf/analyze');assert.equal(p.calls[0].body.data,'00'.repeat(100));assert.equal(p.calls[0].body.rate_hz,0);assert.equal(p.$('rf-table-body').children.length,50);assert.match(p.$('rf-page').textContent,/1 \/ 2/);p.$('rf-next').click();assert.match(p.$('rf-page').textContent,/2 \/ 2/);assert.equal(p.$('rf-csv').disabled,true);assert.equal(p.$('rf-report').disabled,false);assert.equal(p.$('rf-waveform').querySelectorAll('svg').length,1);assert.equal(JSON.parse(p.$('rf-parameters').textContent).clock,64);
});
test('new file or parameters discard late results and a failed parse keeps original bytes',async t=>{
 let resolve;const p=setup(t,()=>new Promise(r=>resolve=r));p.$('rf-format').value='lf-u8';await p.select();const pending=p.c.analyze();p.$('rf-start').value='4';p.$('rf-start').dispatchEvent(new p.dom.window.Event('input'));resolve(lf());await pending;assert.equal(p.c.result,null);assert.equal(p.$('rf-report').disabled,true);
 const q=setup(t,async()=>{throw Error('bad file')});await q.select();await assert.rejects(q.c.analyze(),/bad file/);assert.equal(q.c.bytes.length,100);assert.equal(q.$('rf-original').disabled,false);assert.equal(q.calls.length,1);
});
test('device fact changes do not invalidate an offline result or generate a second submission',async t=>{
 const p=setup(t);p.$('rf-format').value='lf-u8';await p.select();await p.c.analyze();p.dom.window.document.dispatchEvent(new p.dom.window.Event('desk-feature-device'));assert.equal(p.$('rf-report').disabled,false);assert.equal(p.calls.length,1);
});
test('queried tasks restore analysis after reload without source guessing or execution',async t=>{
 const p=setup(t,async()=>({operation_id:'files.rf-analyze',state:'succeeded',result_retained:true,result:lf()}));p.$('rf-task-id').value='a'.repeat(32);await p.c.query();assert.deepEqual(p.calls,[{path:'/api/tasks/'+'a'.repeat(32)}]);assert.equal(p.$('rf-report').disabled,false);assert.equal(p.$('rf-original').disabled,true);assert.equal(p.$('rf-csv').disabled,true);assert.match(p.$('rf-status').textContent,/不会重跑/);
 const q=setup(t,async()=>({operation_id:'files.rf-analyze',state:'succeeded',result_retained:false}));q.$('rf-task-id').value='b'.repeat(32);await assert.rejects(q.c.query(),/不会自动重新提交/);assert.equal(q.calls.length,1);
});
test('untrusted file names/notes are text and HF filters retain original frame indices',t=>{
 const p=setup(t);const value={...lf(),lf:undefined,hf:{parsed:2,reader_frames:1,card_frames:1,timing:'not_captured',auth_candidates:[],frames:[{index:0,from_card:false,bits:7,raw_bits:7,data:'26',parity_check:'not_captured',parity_errors:0,crc_a_candidate:'not_applicable',label:'REQA candidate'},{index:1,from_card:true,bits:8,raw_bits:8,data:'AA',parity_check:'not_captured',parity_errors:0,crc_a_candidate:'not_applicable',label:'unknown'}]}};value.source.name='<img src=x onerror=alert(1)>';value.notes=['<script>alert(1)</script>'];p.c.render(value);assert.equal(p.c.root.querySelector('img'),null);assert.equal(p.c.root.querySelector('script'),null);p.$('rf-direction').value='card';p.$('rf-direction').dispatchEvent(new p.dom.window.Event('input'));assert.equal(p.$('rf-table-body').children.length,1);assert.match(p.$('rf-table-body').textContent,/1 \/ JSON 记录/);
});
test('format/file bounds and shared capability state disable unavailable analysis',async t=>{
 const p=setup(t,async()=>lf(),'unknown');await p.select();assert.equal(p.$('rf-analyze').disabled,true);assert.match(p.$('rf-reason').textContent,/catalog missing/);await assert.rejects(p.select(new Uint8Array(1048577)),/不会自动截断/);assert.equal(p.calls.length,0);
});

test('changing analysis options during file reading does not lose the selected source',async t=>{
 const p=setup(t);let resolve;const file={name:'late.bin',size:2,arrayBuffer:()=>new Promise(r=>resolve=r)};Object.defineProperty(p.$('rf-file'),'files',{configurable:true,value:[file]});const pending=p.c.loadFile();p.$('rf-format').value='hf-packed';p.$('rf-format').dispatchEvent(new p.dom.window.Event('input'));resolve(new Uint8Array([0,1]).buffer);await pending;assert.equal(p.c.hex,'0001');assert.equal(p.c.fileName,'late.bin');
 p.$('rf-short-markers').checked=true;await p.select();assert.equal(p.$('rf-short-markers').checked,false);
});
