const {test}=require('node:test');const assert=require('node:assert/strict');const fs=require('node:fs');const {JSDOM}=require('jsdom');
function fixture(t,handler=async()=>({})){
 const dom=new JSDOM(fs.readFileSync('cmd/chameleon/webui/index.html','utf8'),{url:'http://127.0.0.1/',runScripts:'outside-only'});t.after(()=>dom.window.close());const w=dom.window,$=id=>w.document.getElementById(id),calls=[];
 w.eval(fs.readFileSync('cmd/chameleon/webui/memory-editor.js','utf8'));
 const editor=new w.DeskMemoryEditor({post:async(path,value)=>{calls.push({path,value});return handler(path,value);},changed:()=>{}});
 const open=(card,rows)=>{$('card-edit-rows').value=rows.join('\n');editor.open(card);};
 return {w,$,calls,editor,open};
}
const classic={tag:1000,data:Array.from({length:20},()=>Array(16).fill(0))};const trailer='010203040506FF078869AABBCCDDEEFF';
test('unit editor uses absolute row addresses and shows changed rows without markup',t=>{
 const p=fixture(t);p.open({tag:1100,data:[[1,2,3,4],[5,6,7,8]]},['01020304','05060708']);p.$('card-edit-address').value='1';p.editor.renderUnit();assert.equal(p.$('card-edit-unit-hex').value,'05 06 07 08');
 p.$('card-edit-unit-hex').value='AA BB CC DD';p.editor.applyUnit();assert.equal(p.editor.rows()[1],'AABBCCDD');assert.match(p.$('card-edit-diff').textContent,/单元 1: 05060708 → AABBCCDD/);assert.match(p.$('card-edit-diff-summary').textContent,/2 个单元 · 1 行/);assert(p.$('card-edit-acl-tools').hidden);
 for(const address of ['-1','2','1.0','9007199254740992','']){p.$('card-edit-address').value=address;p.editor.applyUnit();assert.match(p.$('card-edit-unit-status').textContent,/单元地址/);}
});
test('unknown bytes cannot be filled and widths cannot change',t=>{
 const p=fixture(t);p.open({tag:0,desk_read:{read_result:{kind:'type2'}}},['????????','01020304']);p.$('card-edit-unit-hex').value='00 00 00 00';p.editor.applyUnit();assert.equal(p.editor.rows()[0],'????????');assert.match(p.$('card-edit-unit-status').textContent,/未知字节/);
 p.$('card-edit-address').value='1';p.$('card-edit-unit-hex').value='AA';p.editor.applyUnit();assert.equal(p.editor.rows()[1],'01020304');assert.match(p.$('card-edit-unit-status').textContent,/保持单元宽度/);
});
test('ACL encoding changes only access bytes, preserving keys, GPB and unknown masks',async t=>{
 const p=fixture(t,async()=>({access_hex:'7F0788'}));const rows=Array(4).fill('00'.repeat(16));rows[3]='????????????FF078869????????????';p.open(classic,rows);p.$('card-edit-sector').value='0';p.$('card-edit-acl-values').value='0,1,2,3';await p.editor.acl(true);
 assert.equal(p.editor.rows()[3],'????????????7F078869????????????');assert.equal(p.calls[0].path,'/api/acl/encode');assert.deepEqual([...p.calls[0].value.conditions],[0,1,2,3]);assert.match(p.$('card-edit-unit-status').textContent,/密钥与 GPB 保留/);assert(!p.$('card-edit-acl-tools').hidden);
});
test('ACL decoder resolves 4K sector trailer addresses and rejects unknown access bits',async t=>{
 const p=fixture(t,async()=>({conditions:[0,1,2,3]}));const rows=Array(144).fill('00'.repeat(16));rows[143]=trailer;p.open({tag:1003},rows);p.$('card-edit-sector').value='32';await p.editor.acl(false);assert.equal(p.calls[0].value.access,'FF0788');assert.equal(p.$('card-edit-acl-values').value,'0,1,2,3');
 rows[143]='010203040506??????69AABBCCDDEEFF';p.open({tag:1003},rows);p.$('card-edit-sector').value='32';await p.editor.acl(false);assert.equal(p.calls.length,1);assert.match(p.$('card-edit-unit-status').textContent,/访问位尚未读取/);
});
test('late ACL responses cannot overwrite changed rows, selected cards or ACL inputs',async t=>{
 for(const change of ['rows','card','sector','conditions']){
  let resolve;const p=fixture(t,()=>new Promise(r=>resolve=r));const rows=['00'.repeat(16),'00'.repeat(16),'00'.repeat(16),trailer];p.open(classic,rows);p.$('card-edit-sector').value='0';p.$('card-edit-acl-values').value='0,1,2,3';const pending=p.editor.acl(true);
  if(change==='rows')p.$('card-edit-rows').value='AA'+p.$('card-edit-rows').value.slice(2);if(change==='card')p.open({...classic,id:'new'},rows);if(change==='sector')p.$('card-edit-sector').value='1';if(change==='conditions')p.$('card-edit-acl-values').value='1,2,3,4';
  resolve({access_hex:'7F0788'});await pending;assert.equal(p.editor.rows()[3],trailer);assert.match(p.$('card-edit-unit-status').textContent,/已变化/);
 }
});
test('external busy state and pending ACL requests keep all memory controls disabled',async t=>{
 let resolve;const p=fixture(t,()=>new Promise(r=>resolve=r));p.open(classic,['00'.repeat(16),'00'.repeat(16),'00'.repeat(16),trailer]);p.$('card-edit-sector').value='0';p.$('card-edit-acl-values').value='0,1,2,3';p.editor.sync(true);p.editor.render();assert(p.$('card-edit-unit-apply').disabled);await p.editor.acl(true);assert.equal(p.calls.length,0);
 p.editor.sync(false);const pending=p.editor.acl(true);p.editor.render();assert(p.$('card-edit-unit-apply').disabled);assert(p.$('card-edit-acl-read').disabled);await p.editor.acl(true);assert.equal(p.calls.length,1);resolve({access_hex:'7F0788'});await pending;assert(!p.$('card-edit-unit-apply').disabled);
 p.editor.open(null);assert(p.$('card-edit-unit-apply').disabled);
});
test('blank sectors and empty ACL conditions are rejected without requests',async t=>{
 const p=fixture(t);p.open(classic,['00'.repeat(16),'00'.repeat(16),'00'.repeat(16),trailer]);p.$('card-edit-sector').value='';await p.editor.acl(false);assert.equal(p.calls.length,0);p.$('card-edit-sector').value='0';p.$('card-edit-acl-values').value='1,,3,4';await p.editor.acl(true);assert.equal(p.calls.length,0);
});
