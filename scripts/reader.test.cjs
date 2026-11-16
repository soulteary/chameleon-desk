const {test}=require('node:test');
const assert=require('node:assert/strict');
const fs=require('node:fs');
const {JSDOM}=require('jsdom');
function fixture(t,handler,options={}){
 const dom=new JSDOM(fs.readFileSync('cmd/chameleon/webui/index.html','utf8'),{url:'http://127.0.0.1/',runScripts:'outside-only'});
 dom.window.eval(fs.readFileSync('cmd/chameleon/webui/feature-availability.js','utf8'));
 dom.window.eval(fs.readFileSync('cmd/chameleon/webui/reader.js','utf8'));t.after(()=>dom.window.close());
 const $=id=>dom.window.document.getElementById(id),calls=[],downloads=[];
 dom.window.Blob=global.Blob;dom.window.URL.createObjectURL=blob=>{downloads.push({blob});return 'blob:test';};dom.window.URL.revokeObjectURL=()=>{};dom.window.HTMLAnchorElement.prototype.click=function(){downloads.at(-1).name=this.download;};
 const features=new dom.window.DeskFeatureAvailability({api:async()=>({}),changed(){features.apply();},baseDisabled:()=>false,writeAllowed:()=>false});
 const controller=new dom.window.DeskReader({features,post:async(path,value)=>{calls.push({path,value});return handler?handler(path,value):{schema_version:1,state:{id:value.id,state:'available',reasons:[]}};},run:async(_group,fn)=>fn(),...options});features.device={model_id:0};
 return {dom,$,calls,features,controller,downloads};
}
const tick=()=>new Promise(resolve=>setImmediate(resolve));
test('reader preview uses selected LF parameters and read-only controls show firmware reasons',async t=>{
 const p=fixture(t,(_path,value)=>({schema_version:1,state:{id:value.id,state:'unavailable',reasons:[{code:'missing_commands',message:'missing',commands:[3014]}]}}));
 p.$('reader-lf-protocol').value='pac';await p.controller.preview('lf');
 assert.deepEqual(JSON.parse(JSON.stringify(p.calls[0].value.lf_read)),{protocol:'pac'});
 assert.equal(p.$('reader-lf-go').disabled,true);assert.match(p.$('reader-lf-go').nextElementSibling.textContent,/3014/);
});
test('model and native-transport reasons remain visible when input is incomplete',async t=>{
 const p=fixture(t);p.features.states.set('reader.type2-range',{id:'reader.type2-range',state:'unavailable',reasons:[{code:'unsupported_model',message:'unsupported'}]});p.features.apply();
 await p.controller.preview('type2');assert.equal(p.calls.length,0);assert.match(p.$('reader-type2-go').nextElementSibling.textContent,/当前设备型号/);
});
test('late preview cannot enable a read after parameters change or session clear',async t=>{
 let resolve;const p=fixture(t,()=>new Promise(r=>resolve=r));
 const first=p.controller.preview('lf');p.$('reader-lf-protocol').value='em4x05';p.$('reader-lf-password').value='invalid';await p.controller.preview('lf');
 resolve({schema_version:1,state:{id:'reader.lf',state:'available',reasons:[]}});await first;assert.equal(p.$('reader-lf-go').disabled,true);
 p.$('reader-lf-password').value='12345678';const second=p.controller.preview('lf');p.features.device=null;p.controller.clear();resolve({schema_version:1,state:{id:'reader.lf',state:'available',reasons:[]}});await second;assert.equal(p.$('reader-lf-password').value,'');
});
test('stale read results cannot enter a new form and no mutation is submitted',async t=>{
 let resolve;const p=fixture(t,(path,value)=>path==='/api/features/check'?{schema_version:1,state:{id:value.id,state:'available',reasons:[]}}:new Promise(r=>resolve=r));
 p.$('reader-type2-uid').value='01020304';await p.controller.preview('type2');const pending=p.controller.read('type2');p.controller.clear();
 resolve({schema:1,kind:'type2',complete:true,requested_units:4,units_read:4,data:'AB'.repeat(16)});await pending;
 assert.equal(p.$('reader-type2-result').textContent,'');assert.equal(p.$('reader-type2-export').disabled,true);assert.equal(p.calls.filter(c=>c.path.startsWith('/api/reader/')).length,1);
});
test('partial blocks remain explicit on failure and export never presents them as a card image',async t=>{
 const p=fixture(t,(path,value)=>{if(path==='/api/features/check')return {schema_version:1,state:{id:value.id,state:'available',reasons:[]}};const e=Error('card changed');e.result={schema:1,kind:'classic',complete:false,start:0,requested_units:20,units_read:1,dump:{data:'11'.repeat(16),unknown_keys:[]}};throw e;});
 p.$('reader-classic-json').value=JSON.stringify({uid:'01020304',sectors:[]});await p.controller.preview('classic');await p.controller.read('classic');
 assert.equal(p.$('reader-classic-json').value,'');assert.match(p.$('reader-classic-result').textContent,/部分.*1 \/ 20/);p.controller.export('classic');
 assert.equal(p.downloads[0].name,'classic-read-partial.json');const result=JSON.parse(await p.downloads[0].blob.text());assert.equal(result.read_result.complete,false);assert.equal(result.error,'card changed');assert.equal(result.tag,undefined);
});
test('LF passwords are submitted once, cleared and never copied into result rendering',async t=>{
 const p=fixture(t,(path,value)=>path==='/api/features/check'?{schema_version:1,state:{id:value.id,state:'available',reasons:[]}}:{schema:1,kind:'lf',complete:true,requested_units:1,units_read:1,present:true,record:{protocol:'em4x05'}});
 p.$('reader-lf-protocol').value='em4x05';p.$('reader-lf-password').value='12345678';await p.controller.preview('lf');await p.controller.read('lf');
 assert.equal(p.calls.find(c=>c.path==='/api/reader/lf').value.password,0x12345678);assert.equal(p.$('reader-lf-password').value,'');assert.doesNotMatch(p.$('reader-lf-result').textContent,/12345678/);
});
test('rendering is text-only and a new input invalidates exported data',async t=>{
 const p=fixture(t);p.controller.show('lf',{schema:1,kind:'lf',complete:true,requested_units:1,units_read:1,present:true,record:{name:'<img src=x onerror=alert(1)>'}});
 assert.equal(p.$('reader-lf-result').querySelector('img'),null);p.$('reader-lf-protocol').dispatchEvent(new p.dom.window.Event('input',{bubbles:true}));await tick();assert.equal(p.$('reader-lf-export').disabled,true);
});
test('inconsistent completed byte counts cannot be exported as a successful read',async t=>{
 const p=fixture(t);p.controller.show('type2',{schema:1,kind:'type2',complete:true,requested_units:4,units_read:4,data:'00'});
 assert.match(p.$('reader-type2-result').textContent,/格式无效/);assert.equal(p.$('reader-type2-export').disabled,true);
});

test('reader import requires confirmed data and offline library availability and rejects a stale source',async t=>{
 let received,current;const p=fixture(t,null,{importRead:async(result,check)=>{received=result;current=check;}});
 p.features.states.set('library.database',{id:'library.database',state:'available',reasons:[]});
 p.controller.show('type2',{schema:1,kind:'type2',complete:false,start:2,requested_units:4,units_read:1,data:'01020304'});assert.equal(p.$('reader-type2-import').disabled,false);
 await p.$('reader-type2-import').onclick();assert.equal(received.value.start,2);assert(current());
 p.controller.invalidate('type2');assert(!current());assert(p.$('reader-type2-import').disabled);
 p.controller.show('type2',{schema:1,kind:'type2',complete:false,start:2,requested_units:4,units_read:0});assert(p.$('reader-type2-import').disabled);assert.match(p.$('reader-type2-import-reason').textContent,/没有已确认/);
 p.controller.show('lf',{schema:1,kind:'lf',complete:true,requested_units:1,units_read:0,present:false});assert(p.$('reader-lf-import').disabled);
 p.features.states.set('library.database',{id:'library.database',state:'unknown',reasons:[]});p.controller.show('classic',{schema:1,kind:'classic',complete:false,start:0,requested_units:20,units_read:1,dump:{data:'11'.repeat(16)}});assert(p.$('reader-classic-import').disabled);assert.match(p.$('reader-classic-import-reason').textContent,/能力尚未就绪/);
});

test('Classic identity fields are displayed in numeric ATQA order without changing result bytes',t=>{
 const p=fixture(t);const value={schema:1,kind:'classic',complete:true,start:0,requested_units:20,units_read:20,identity:{schema:1,uid:'01020304',atqa:'0004',sak:9,ats:''},dump:{data:'11'.repeat(320)}};
 p.controller.show('classic',value);assert.match(p.$('reader-classic-result').textContent,/UID 01020304.*ATQA 0004.*SAK 09.*ATS 空/);assert.equal(p.controller.results.get('classic').value.dump.data,value.dump.data);
});
