const {test}=require('node:test');
const assert=require('node:assert/strict');
const fs=require('node:fs');
const {JSDOM}=require('jsdom');
function panel(t,api,state='available'){
 const dom=new JSDOM(fs.readFileSync('cmd/chameleon/webui/index.html','utf8'),{url:'http://127.0.0.1/',runScripts:'outside-only'});
 t.after(()=>dom.window.close());dom.window.eval(fs.readFileSync('cmd/chameleon/webui/environment.js','utf8'));
 const controller=new dom.window.DeskEnvironment({api,features:{state:()=>({state,reasons:[{message:'not ready'}]})},run:async(_g,fn)=>fn()});
 return {dom,c:controller};
}
const report=()=>({schema_version:1,os:'darwin',arch:'arm64',checks:[{status:'unknown',code:'bridge_host_environment',message:'run local doctor'}],recovery:{checks:[{code:'worker_runtime_unverified',status:'unknown',message:'not executed'}],workers:[{name:'nested',state:'available',code:'file_present_unverified'},{name:'hardnested',state:'unavailable',code:'worker_missing'}]}});
test('environment diagnosis displays failures and unknown observations as text without claiming readiness',async t=>{
 let calls=0;const p=panel(t,async path=>{calls++;assert.equal(path,'/api/environment');const value=report();value.checks[0].message='<img src=x onerror=alert(1)>';return value});
 await p.c.check();assert.equal(calls,1);assert.equal(p.c.list.querySelector('img'),null);assert.match(p.c.list.textContent,/未验证.*nested/);assert.match(p.c.list.textContent,/失败.*hardnested/);assert.match(p.c.status.textContent,/发现环境缺项/);assert.match(p.c.status.textContent,/不代表恢复成功或硬件可用/);assert.equal(p.c.exportButton.disabled,false);
});
test('an old diagnostic cannot populate a new session and failed checks never retry',async t=>{
 let resolve;const p=panel(t,()=>new Promise(r=>resolve=r));const pending=p.c.check();p.dom.window.document.dispatchEvent(new p.dom.window.Event('desk-feature-device'));resolve(report());await pending;assert.equal(p.c.result,null);assert.equal(p.c.list.children.length,0);assert.equal(p.c.exportButton.disabled,true);
 let calls=0;const q=panel(t,async()=>{calls++;throw Error('management timeout')});await assert.rejects(q.c.check(),/management timeout/);assert.equal(calls,1);assert.equal(q.c.result,null);assert.equal(q.c.exportButton.disabled,true);
});
test('environment controls wait for the shared capability inventory',t=>{const p=panel(t,async()=>report(),'unknown');assert.equal(p.c.button.disabled,true);assert.match(p.c.button.title,/not ready/)});
