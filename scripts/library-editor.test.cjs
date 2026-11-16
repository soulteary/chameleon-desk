const {test}=require('node:test');
const assert=require('node:assert/strict');
const fs=require('node:fs');
const {JSDOM}=require('jsdom');
function fixture(t,handler){
 const dom=new JSDOM(fs.readFileSync('cmd/chameleon/webui/index.html','utf8'),{url:'http://127.0.0.1/',runScripts:'outside-only'});t.after(()=>dom.window.close());
 const w=dom.window,$=id=>w.document.getElementById(id);w.eval(fs.readFileSync('cmd/chameleon/webui/library-editor.js','utf8'));w.confirm=()=>true;w.Blob=global.Blob;
 let text='{"custom":42}',changes=0,enabled=true,busy=false;const calls=[],downloads=[];
 w.URL.createObjectURL=blob=>{downloads.push({blob});return 'blob:test'};w.URL.revokeObjectURL=()=>{};w.HTMLAnchorElement.prototype.click=function(){downloads.at(-1).name=this.download};
 const editor=new w.DeskLibraryEditor({post:async(path,value)=>{calls.push({path,value});return handler(value)},run:(_group,fn)=>fn(),getText:()=>text,setText:v=>text=v,changed:()=>changes++,features:{state:()=>({state:enabled?'available':'unknown'})},busy:()=>busy,notice:()=>{}});
 const input=(id,value)=>{$(id).value=value;$(id).dispatchEvent(new w.Event('input',{bubbles:true}))};
 return {w,$,editor,input,calls,downloads,get text(){return text},set text(v){text=v},get changes(){return changes},set enabled(v){enabled=v;editor.sync()},set busy(v){busy=v;editor.sync()}};
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
 await assert.rejects(p.editor.importRead(envelope),/能力/);assert.equal(p.calls.length,0);p.enabled=true;p.busy=true;assert(p.$('card-edit-rows').disabled);p.busy=false;assert(!p.$('card-edit-save').disabled);
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
