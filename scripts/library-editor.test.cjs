const {test}=require('node:test');
const assert=require('node:assert/strict');
const fs=require('node:fs');
const {JSDOM}=require('jsdom');
function fixture(t,handler){
 const dom=new JSDOM(fs.readFileSync('cmd/chameleon/webui/index.html','utf8'),{url:'http://127.0.0.1/',runScripts:'outside-only'});t.after(()=>dom.window.close());
 const w=dom.window,$=id=>w.document.getElementById(id);w.eval(fs.readFileSync('cmd/chameleon/webui/library-editor.js','utf8'));w.confirm=()=>true;w.Blob=global.Blob;
 let text='{"custom":42}',changes=0,enabled=true,busy=false;const calls=[],downloads=[],runs=[];
 w.URL.createObjectURL=blob=>{downloads.push({blob});return 'blob:test'};w.URL.revokeObjectURL=()=>{};w.HTMLAnchorElement.prototype.click=function(){downloads.at(-1).name=this.download};
 const editor=new w.DeskLibraryEditor({post:async(path,value)=>{calls.push({path,value});return handler(value,path)},run:(group,fn)=>{runs.push(group);return fn()},getText:()=>text,setText:v=>text=v,changed:()=>changes++,features:{state:()=>({state:enabled?'available':'unknown'})},busy:()=>busy,notice:()=>{}});
 const input=(id,value)=>{$(id).value=value;$(id).dispatchEvent(new w.Event('input',{bubbles:true}))};
 return {w,$,editor,input,calls,downloads,runs,get text(){return text},set text(v){text=v},get changes(){return changes},set enabled(v){enabled=v;editor.sync()},set busy(v){busy=v;editor.sync()}};
}
const card={id:'draft',name:'read',uid:'01020304',tag:0,atqa:[],ats:[],sak:0,data:[],desk_read:{schema:1,read_result:{schema:1,kind:'type2',complete:false,start:1,requested_units:2,units_read:1},rows:['????????','01020304','????????']}};
const envelope={value:card.desk_read.read_result,error:'interrupted'};
test('read import uses the current library, retains failure status and opens an editable draft',async t=>{
 const p=fixture(t,()=>({database:{custom:42,cards:['card']},card}));await p.editor.importRead(envelope);
 assert.equal(p.calls[0].path,'/api/database');assert.equal(p.calls[0].value.op,'import-read');assert.equal(p.calls[0].value.read_error,'interrupted');assert.equal(p.calls[0].value.database.custom,42);assert.equal(p.changes,1);
 assert.equal(p.$('card-edit-rows').value,'?? ?? ?? ??\n01 02 03 04\n?? ?? ?? ??');assert.match(p.$('card-edit-limit').textContent,/部分.*不能补值.*禁止槽位加载/);
});
test('failed data saves keep the editor dirty and do not replace the library or source',async t=>{
 const p=fixture(t,()=>{throw Error('unknown bytes must remain')});p.editor.open(card);p.input('card-edit-rows','00');
 await assert.rejects(p.editor.save(),/unknown bytes/);assert.equal(p.changes,0);assert.equal(p.text,'{"custom":42}');assert(p.editor.dirty);assert.equal(p.$('card-edit-rows').value,'00');assert.match(p.$('card-edit-state').textContent,/未保存/);
});
test('an edited response cannot overwrite newer local input or a changed database',async t=>{
 let resolve;const p=fixture(t,()=>new Promise(r=>resolve=r));p.editor.open(card);p.input('card-edit-name','first');const pending=p.editor.save();
 assert(p.editor.pending);p.input('card-edit-name','second');resolve({database:{new:1},card});await assert.rejects(pending,/已变化/);assert.equal(p.changes,0);assert.equal(p.$('card-edit-name').value,'second');assert(!p.editor.pending);
 p.text='{"newer":true}';p.editor.sync();assert(p.$('card-edit-save').disabled);assert.match(p.$('card-edit-state').textContent,/卡库已变化/);
});
test('stale read results and pending database replacements cannot be imported',async t=>{
 let resolve;const p=fixture(t,()=>new Promise(r=>resolve=r));const pending=p.editor.importRead(envelope,()=>false);resolve({database:{bad:1},card});await assert.rejects(pending,/已变化/);assert.equal(p.changes,0);
 const second=p.editor.importRead(envelope);p.text='{"newer":true}';resolve({database:{bad:1},card});await assert.rejects(second,/已变化/);assert.equal(p.text,'{"newer":true}');
});
test('offline capability and load state disable mutations while editor drafts remain exportable',async t=>{
 const p=fixture(t,()=>({}));p.editor.open(card);p.input('card-edit-name','new');p.enabled=false;
 assert(p.$('card-edit-save').disabled);assert(p.$('read-import-go').disabled);assert.match(p.$('read-import-reason').textContent,/能力尚未就绪/);assert(!p.$('card-edit-export').disabled);
 await assert.rejects(p.editor.importRead(envelope),/能力/);assert.equal(p.calls.filter(c=>c.path==='/api/database').length,0);p.enabled=true;p.busy=true;assert(p.$('card-edit-rows').disabled);p.busy=false;assert(!p.$('card-edit-save').disabled);
});
test('unsaved and invalid editor text can be exported without claiming a saved card',async t=>{
 const p=fixture(t,()=>({}));p.editor.open(card);p.input('card-edit-rows','not complete hex');p.editor.exportDraft();
 const value=JSON.parse(await p.downloads[0].blob.text());assert.equal(value.format,'chameleon-desk-editor-draft');assert.equal(value.rows_text,'not complete hex');assert.deepEqual(value.card,card);assert.equal(p.changes,0);assert(p.editor.dirty);
});
test('selection asks before discarding edits and malicious names remain text',t=>{
 const p=fixture(t,()=>({}));p.editor.open(card);p.input('card-edit-name','<img src=x>');p.w.confirm=()=>false;p.editor.open({...card,id:'other'});
 assert.equal(p.editor.card.id,'draft');assert.equal(p.$('card-edit-name').value,'<img src=x>');assert.equal(p.w.document.querySelector('.card-editor img'),null);
});
test('ordinary memory edits submit rows with the selected ID and keep file metadata in the server workflow',async t=>{
 const ordinary={id:'original',name:'old',tag:1100,data:[[1,2,3,4],[]]};const p=fixture(t,value=>({database:{custom:42},card:{...ordinary,name:value.name,data:[[170,2,3,4],[]]}}));
 p.editor.open(ordinary);assert.equal(p.$('card-edit-rows').value,'01 02 03 04\n');p.input('card-edit-name','new');p.input('card-edit-rows','AA 02 03 04\n');await p.editor.save();
 assert.equal(p.calls[0].value.id,'original');assert.equal(p.calls[0].value.op,'edit-data');assert.deepEqual([...p.calls[0].value.rows],['AA 02 03 04','']);assert(!p.editor.dirty);assert.equal(p.changes,1);
});

test('renaming an ordinary card with no memory does not invent an empty memory row',async t=>{
 const empty={id:'empty',name:'old',tag:1100,data:[]};const p=fixture(t,value=>({database:{cards:[]},card:{...empty,name:value.name}}));
 p.editor.open(empty);p.input('card-edit-name','new');await p.editor.save();assert.deepEqual([...p.calls[0].value.rows],[]);assert(!p.editor.dirty);
});

const classicDraft=()=>({...card,desk_read:{...card.desk_read,read_result:{...card.desk_read.read_result,kind:'classic'},rows:['11'.repeat(16)]}});
const promotionState=(state='available',message='')=>({schema_version:1,state:{id:'library.promote-classic',state,reasons:message?[{code:'invalid_parameters',message}]:[],required_commands:[]}});
test('Classic conversion is explicitly previewed and appends a standard card without a write flag',async t=>{
 const source=classicDraft(),standard={id:'converted',name:'read',tag:1000,uid:'01020304',data:Array.from({length:20},()=>Array(16).fill(0))};
 const p=fixture(t,value=>value.id==='library.promote-classic'?promotionState():{database:{cards:['draft','converted'],custom:42},card:standard});
 p.editor.open(source);await p.editor.previewReady;assert.equal(p.$('card-promote-classic').disabled,false);await p.editor.promote();
 assert.equal(p.calls[1].value.op,'promote-classic');assert.equal(p.calls[1].value.id,'draft');assert.equal(p.calls[1].value.allow_writes,undefined);assert.equal(p.changes,1);assert.equal(p.editor.card.id,'converted');assert(p.$('card-promote-classic').disabled);
});
test('partial, legacy and unknown-key rejection reasons are visible and no conversion is posted',async t=>{
 for(const message of ['Classic identity is missing','all sector keys must be known','complete read required']){
  const p=fixture(t,()=>promotionState('unavailable',message));p.editor.open(classicDraft());await p.editor.previewReady;
  assert(p.$('card-promote-classic').disabled);assert.match(p.$('card-promote-reason').textContent,new RegExp(message));await p.editor.promote();assert.equal(p.calls.length,1);
 }
});
test('late promotion preview cannot enable a changed or replaced draft',async t=>{
 let resolve;const p=fixture(t,()=>new Promise(r=>resolve=r));p.editor.open(classicDraft());const pending=p.editor.previewReady;p.input('card-edit-name','new');resolve(promotionState());await pending;
 assert(p.$('card-promote-classic').disabled);assert.match(p.$('card-promote-reason').textContent,/先保存/);
 p.editor.open(classicDraft());const next=p.editor.previewReady;p.editor.clear();resolve(promotionState());await next;assert(p.$('card-promote-classic').disabled);
});
test('a changed library invalidates an in-flight conversion without replacing local data',async t=>{
 let resolve;const p=fixture(t,value=>value.id==='library.promote-classic'?promotionState():new Promise(r=>resolve=r));p.editor.open(classicDraft());await p.editor.previewReady;
 const pending=p.editor.promote();p.text='{"newer":true}';resolve({database:{old:true},card:{id:'converted'}});await assert.rejects(pending,/已变化/);assert.equal(p.text,'{"newer":true}');assert.equal(p.changes,0);
});
test('conversion errors keep the saved draft and allow explicit retry',async t=>{
 const p=fixture(t,value=>{if(value.id==='library.promote-classic')return promotionState();throw Error('invalid block 0')});p.editor.open(classicDraft());await p.editor.previewReady;
 await assert.rejects(p.editor.promote(),/invalid block/);assert.equal(p.changes,0);assert.equal(p.editor.card.id,'draft');assert.match(p.$('card-edit-state').textContent,/未保存.*invalid block/);assert.equal(p.$('card-promote-classic').disabled,false);
});
test('Type2 and LF conversion use their own capability checks and explicit Type2 options',async t=>{
 for(const kind of ['type2','lf']){
  const source=classicDraft();source.desk_read.read_result.kind=kind;const standard={id:'converted-'+kind,name:'read',tag:kind==='lf'?150:1100,uid:'3132333435363738',data:[]};
  const p=fixture(t,value=>value.id==='library.promote-'+kind?{schema_version:1,state:{id:value.id,state:kind==='type2'&&!value.type2_promotion.confirm_emulator_credentials?'unavailable':'available',reasons:kind==='type2'&&!value.type2_promotion.confirm_emulator_credentials?[{message:'explicit emulator credentials required'}]:[]}}:{database:{cards:['draft',standard.id]},card:standard});
  p.editor.open(source);await p.editor.previewReady;
  assert.equal(p.$('card-type2-options').hidden,kind!=='type2');assert.equal(p.calls[0].value.id,'library.promote-'+kind);
  if(kind==='type2'){
   assert(p.$('card-promote-classic').disabled);assert.match(p.$('card-promote-reason').textContent,/explicit emulator/);
   p.input('card-type2-password','01020304');p.input('card-type2-pack','ABCD');p.$('card-type2-confirm').checked=true;p.$('card-type2-confirm').dispatchEvent(new p.w.Event('input'));await p.editor.previewReady;
  }
  assert.equal(p.$('card-promote-classic').disabled,false);await p.editor.promote();const request=p.calls.find(c=>c.value.op==='promote-'+kind).value;
  assert.equal(request.id,'draft');if(kind==='type2')assert.deepEqual(JSON.parse(JSON.stringify(request.type2_promotion)),{emulator_password:'01020304',emulator_pack:'ABCD',confirm_emulator_credentials:true});assert.equal(p.changes,1);
 }
});
test('late Type2 conversion cannot discard changed emulator choices',async t=>{
 let resolve;const source=classicDraft();source.desk_read.read_result.kind='type2';
 const p=fixture(t,(value,path)=>path==='/api/features/check'?{schema_version:1,state:{id:'library.promote-type2',state:'available',reasons:[]}}:new Promise(r=>resolve=r));p.editor.open(source);await p.editor.previewReady;
 const pending=p.editor.promote();p.input('card-type2-password','11223344');resolve({database:{old:true},card:{id:'converted'}});await assert.rejects(pending,/已变化/);assert.equal(p.changes,0);assert.equal(p.$('card-type2-password').value,'11223344');
});
const recovery=source=>({schema:1,format:'chameleon-desk-editor-draft',card:source,name:'recovered',rows_text:'?? ?? ?? ??\nAA BB CC DD\n?? ?? ?? ??'});
test('editor recovery matches the exact saved card snapshot and restores unknown rows without saving',async t=>{
 const p=fixture(t,value=>value.op==='show'?JSON.parse(JSON.stringify(card)):promotionState('unavailable','partial'));p.input('card-edit-recovery-json',JSON.stringify(recovery(card)));await p.editor.restoreDraft();
 assert.equal(p.calls[0].value.op,'show');assert.equal(p.calls[0].value.id,'draft');assert(p.editor.dirty);assert.equal(p.$('card-edit-name').value,'recovered');assert.equal(p.$('card-edit-rows').value,'?? ?? ?? ??\nAA BB CC DD\n?? ?? ?? ??');assert.equal(p.changes,0);assert.equal(p.text,'{"custom":42}');assert(p.$('card-promote-classic').disabled);
});
test('recovery refuses a newer saved card and retains existing dirty edits',async t=>{
 const p=fixture(t,value=>value.op==='show'?{...card,name:'newer'}:promotionState('unavailable','partial'));p.editor.open(card);p.input('card-edit-name','local unsaved');p.input('card-edit-recovery-json',JSON.stringify(recovery(card)));
 await assert.rejects(p.editor.restoreDraft(),/原卡片已变化/);assert(p.editor.dirty);assert.equal(p.$('card-edit-name').value,'local unsaved');assert.equal(p.changes,0);
});
test('recovery refuses stale file, selection and database responses',async t=>{
 for(const change of ['file','selection','database']){
  let resolve;const p=fixture(t,value=>value.op==='show'?new Promise(r=>resolve=r):promotionState('unavailable','partial'));p.input('card-edit-recovery-json',JSON.stringify(recovery(card)));const pending=p.editor.restoreDraft();
  if(change==='file')p.input('card-edit-recovery-json','{}');else if(change==='selection')p.editor.open({...card,id:'other'});else p.text='{"newer":true}';resolve(card);
  await assert.rejects(pending,/已变化/);assert.equal(p.changes,0);assert.notEqual(p.$('card-edit-name').value,'recovered');
 }
});
test('Classic merge previews without mutation and appends a new draft with originals retained',async t=>{
 const source=classicDraft(),merged={...classicDraft(),id:'merged'};const p=fixture(t,(value,path)=>path==='/api/features/check'?promotionState('unavailable','partial'):value.op==='merge-classic-preview'?{conflicts:[{block:1,byte:0}]}:{database:{cards:['draft','other','merged'],custom:42},card:merged});
 p.editor.open(source);await p.editor.previewReady;p.input('card-merge-id','other');p.input('card-merge-options',JSON.stringify({confirm_same_card:true,resolutions:[{block:1,byte:0,source:'right'}]}));await p.editor.merge(true);assert.equal(p.changes,0);assert.match(p.$('card-merge-result').textContent,/conflicts/);
 await p.editor.merge(false);const value=p.calls.find(c=>c.value.op==='merge-classic').value;assert.equal(value.id,'draft');assert.equal(value.other_id,'other');assert.equal(value.merge_options.confirm_same_card,true);assert.deepEqual(JSON.parse(p.text).cards,['draft','other','merged']);assert.equal(p.editor.card.id,'merged');assert.equal(p.changes,1);
});
test('Classic merge cannot replace changed or dirty editor data',async t=>{
 let resolve;const p=fixture(t,(value,path)=>path==='/api/features/check'?promotionState('unavailable','partial'):new Promise(r=>resolve=r));p.editor.open(classicDraft());await p.editor.previewReady;p.input('card-merge-id','other');const pending=p.editor.merge(false);p.input('card-edit-name','newer');resolve({database:{bad:true},card:{id:'merged'}});await assert.rejects(pending,/已变化/);assert.equal(p.changes,0);assert.equal(p.$('card-edit-name').value,'newer');await assert.rejects(p.editor.merge(true),/已保存/);
});
const lfCard={id:'pac',name:'PAC',uid:'3132333435363738',tag:150,sak:0,atqa:[],ats:[],data:[],custom:{keep:true}};
test('LF structured edits preserve file identity metadata and use the host codec route',async t=>{
 const p=fixture(t,value=>({database:{cards:['pac'],custom:42},card:{...lfCard,uid:'4142434431323334'}}));p.editor.open(lfCard);assert.equal(p.$('card-lf-editor').hidden,false);assert.equal(p.$('card-edit-rows').value,'31 32 33 34 35 36 37 38');p.input('card-lf-identity',JSON.stringify({protocol:'pac',tag_type:150,pac:'ABCD1234'}));
 await p.editor.applyLFIdentity();assert.equal(p.calls[0].path,'/api/database');assert.equal(p.calls[0].value.op,'edit-lf');assert.equal(p.calls[0].value.lf_identity.pac,'ABCD1234');assert.equal(p.calls[0].value.id,'pac');assert.equal(p.editor.card.id,'pac');assert.equal(p.editor.card.custom.keep,true);assert.equal(p.changes,1);
});
test('ioProx structured edits require firmware fields and route to the managed hardware endpoint',async t=>{
 const io={...lfCard,id:'io',tag:201,uid:'01531234007854E03A5D65AB00000000'};const p=fixture(t,(value,path)=>path==='/api/features/check'?{schema_version:1,state:{id:'library.edit-lf-device',state:'available',reasons:[]}}:{database:{cards:['io']},card:io});p.editor.open(io);await p.editor.lfPreviewReady;p.input('card-lf-identity',JSON.stringify({protocol:'ioprox',tag_type:201,id:io.uid}));assert(p.$('card-lf-apply').disabled);await assert.rejects(p.editor.applyLFIdentity(),/ioprox 字段/);assert.equal(p.calls.filter(c=>c.path!=='/api/features/check').length,0);
 p.input('card-lf-identity',JSON.stringify({protocol:'ioprox',tag_type:201,ioprox:{version:1,facility:83,number:4660}}));await p.editor.lfPreviewReady;await p.$('card-lf-apply').onclick();assert.equal(p.runs[0],'hardware');const edit=p.calls.find(c=>c.path==='/api/reader/lf-edit');assert(edit);assert.equal(edit.value.identity.ioprox.facility,83);assert.equal(p.changes,1);
});
test('LF structured edits preserve dirty text and reject stale results and busy device gates',async t=>{
 let resolve;const p=fixture(t,()=>new Promise(r=>resolve=r));p.editor.open(lfCard);p.input('card-edit-rows','AA');await assert.rejects(p.editor.applyLFIdentity(),/先保存/);assert.equal(p.calls.length,0);assert.equal(p.$('card-edit-rows').value,'AA');
 p.editor.open(lfCard);const pending=p.editor.applyLFIdentity();p.input('card-lf-identity',JSON.stringify({protocol:'pac',tag_type:150,pac:'ABCDEFGH'}));resolve({database:{bad:true},card:lfCard});await assert.rejects(pending,/已变化/);assert.equal(p.changes,0);
 const io={...lfCard,tag:201,uid:'01531234007854E03A5D65AB00000000'};p.editor.open(io);p.$('read').setAttribute('aria-busy','true');p.editor.sync();assert(p.$('card-lf-apply').disabled);p.$('card-lf-apply').onclick();assert.equal(p.runs.length,0);
});
test('selected Classic repair requires current preview and creates a new evidence draft',async t=>{
 const source=classicDraft(),next={...classicDraft(),id:'repaired'},repair={schema:1,complete:true,requested_blocks:[1],observations:[{block:1,data:'22'.repeat(16)}]};
 const p=fixture(t,(value,path)=>path==='/api/features/check'?promotionState('unavailable','partial'):value.op==='repair-classic-preview'?{conflicts:[],known_bytes_added:16}:{database:{cards:['draft','repaired']},card:next});p.editor.open(source);await p.editor.previewReady;p.input('card-selected-repair',JSON.stringify(repair));await assert.rejects(p.editor.selectedRepair(false),/先预览/);await p.editor.selectedRepair(true);assert.equal(p.changes,0);assert.equal(p.$('card-repair-apply').disabled,false);await p.editor.selectedRepair(false);
 const value=p.calls.find(c=>c.value.op==='repair-classic').value;assert.equal(value.id,'draft');assert.equal(value.selected_repair.observations[0].block,1);assert.deepEqual(JSON.parse(p.text).cards,['draft','repaired']);assert.equal(p.editor.card.id,'repaired');
});
test('selected repair preview is invalidated by changed conflict options and stale replies do not replace data',async t=>{
 let resolve;const p=fixture(t,(value,path)=>path==='/api/features/check'?promotionState('unavailable','partial'):new Promise(r=>resolve=r));p.editor.open(classicDraft());await p.editor.previewReady;p.input('card-selected-repair','{"schema":1}');const pending=p.editor.selectedRepair(true);p.input('card-merge-options','{"confirm_same_card":true,"resolutions":[]}');resolve({conflicts:[]});await assert.rejects(pending,/已变化/);assert.equal(p.$('card-repair-apply').disabled,true);assert.equal(p.changes,0);assert.equal(p.$('card-repair-result').textContent,'');
});
test('selected repair envelopes preserve interruption instead of upgrading partial evidence',async t=>{
 const p=fixture(t,(value,path)=>path==='/api/features/check'?promotionState('unavailable','partial'):{conflicts:[]});p.editor.open(classicDraft());await p.editor.previewReady;
 p.input('card-selected-repair',JSON.stringify({schema:1,result:{schema:1,complete:true,observations:[]},error:'reader cleanup interrupted'}));await p.editor.selectedRepair(true);
 const request=p.calls.find(c=>c.value.op==='repair-classic-preview').value;assert.equal(request.selected_repair.complete,false);assert.equal(request.selected_repair.error,'reader cleanup interrupted');assert.equal(p.changes,0);
 p.input('card-selected-repair',JSON.stringify({schema:1,result:{schema:1,complete:false,error:'original error',observations:[]},error:'wrapper error'}));await p.editor.selectedRepair(true);assert.equal(p.calls.at(-1).value.selected_repair.error,'original error');
});
test('merge and selected repair refuse server results that reuse the source ID',async t=>{
 const p=fixture(t,(value,path)=>path==='/api/features/check'?promotionState('unavailable','partial'):value.op==='repair-classic-preview'?{conflicts:[]}:{database:{cards:['draft']},card:classicDraft()});p.editor.open(classicDraft());await p.editor.previewReady;p.input('card-merge-id','other');await assert.rejects(p.editor.merge(false),/新草稿标识/);assert.equal(p.changes,0);
 p.input('card-selected-repair','{"schema":1}');await p.editor.selectedRepair(true);await assert.rejects(p.editor.selectedRepair(false),/新草稿标识/);assert.equal(p.changes,0);
});
test('ioProx checks the selected firmware command and shows missing 3012 or 3013',async t=>{
 const io={...lfCard,tag:201,uid:'01531234007854E03A5D65AB00000000'};const p=fixture(t,value=>{const command=value.lf_identity.ioprox.raw?3012:3013;return {schema_version:1,state:{id:'library.edit-lf-device',state:'unavailable',required_commands:[command],reasons:[{code:'missing_commands',message:'The device does not advertise required commands',commands:[command]}]}}});p.editor.open(io);await p.editor.lfPreviewReady;assert.equal(p.calls[0].value.lf_identity.ioprox.raw,'007854E03A5D65AB');assert(p.$('card-lf-apply').disabled);assert.match(p.$('card-lf-reason').textContent,/3012/);
 p.input('card-lf-identity',JSON.stringify({protocol:'ioprox',tag_type:201,ioprox:{version:1,facility:83,number:4660}}));await p.editor.lfPreviewReady;assert.match(p.$('card-lf-reason').textContent,/3013/);await assert.rejects(p.editor.applyLFIdentity(),/能力尚未就绪/);assert.equal(p.calls.filter(c=>c.path!=='/api/features/check').length,0);
});
test('stale ioProx previews cannot enable changed fields, a changed library or a replaced device',async t=>{
 const io={...lfCard,tag:201,uid:'01531234007854E03A5D65AB00000000'},ready={schema_version:1,state:{id:'library.edit-lf-device',state:'available',reasons:[]}};
 for(const change of ['fields','database','device']){
  let resolve;const p=fixture(t,()=>new Promise(r=>resolve=r));p.editor.features.device={session:'old'};p.editor.open(io);const pending=p.editor.lfPreviewReady,oldResolve=resolve;
  if(change==='fields')p.input('card-lf-identity','{}');else if(change==='database')p.text='{"newer":true}';else {p.editor.features.device={session:'new'};p.w.document.dispatchEvent(new p.w.Event('desk-feature-device'));}
  oldResolve(ready);await pending;assert(p.$('card-lf-apply').disabled);assert.notEqual(p.editor.lfState?.state,'available');assert.equal(p.changes,0);
 }
});
test('device replacement rejects an in-flight firmware-backed LF edit before library commit',async t=>{
 const io={...lfCard,tag:201,uid:'01531234007854E03A5D65AB00000000'};let resolve;const p=fixture(t,(_value,path)=>path==='/api/features/check'?{schema_version:1,state:{id:'library.edit-lf-device',state:'available',reasons:[]}}:new Promise(r=>resolve=r));p.editor.features.device={session:'old'};p.editor.open(io);await p.editor.lfPreviewReady;
 const pending=p.editor.applyLFIdentity();p.editor.features.device={session:'new'};p.w.document.dispatchEvent(new p.w.Event('desk-feature-device'));resolve({database:{bad:true},card:io});await assert.rejects(pending,/已变化/);assert.equal(p.changes,0);
});
