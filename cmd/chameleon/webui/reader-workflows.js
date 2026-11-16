"use strict";
(() => {
 const operations={
  type2:{id:'reader.type2-workflow',field:'type2_workflow',path:'/api/reader/type2-workflow',template:{uid:'',full:true},help:'识别受支持型号后读取全部页；可提供 password（8 位十六进制）读取受保护数据。真实凭据仍为未知，结果保留掩码。'},
  'classic-repair':{id:'reader.classic-repair',field:'classic_repair',path:'/api/reader/classic-repair',template:{source:{},read:{uid:'',sectors:[]}},help:'source 为保存的 Classic 草稿，read 为显式分扇区密钥。可添加 blocks 数组读取指定块；原始证据不会被覆盖。'},
  'classic-dictionary':{id:'reader.classic-dictionary',field:'classic_dictionary',path:'/api/reader/classic-dictionary',template:{identity:{schema:1,uid:'',atqa:'',sak:8,ats:''},sectors:16,keys:[],limit_seconds:10},help:'仅使用显式提供的 1–4096 个密钥，运行时间有上限。结果需重新读取验证，不会修改卡库。'},
  'classic-verify':{id:'reader.classic-verify',field:'classic_verify',path:'/api/reader/classic-verify',template:{identity:{schema:1,uid:'',atqa:'',sak:8,ats:''},keys:[{sector:0,key_type:96,key:''}]},help:'只验证显式候选密钥，不把验证成功等同于完成数据读取。'},
  'classic-recover':{id:'reader.classic-recover',field:'classic_recovery',path:'/api/reader/classic-recover',template:{identity:{schema:1,uid:'',atqa:'',sak:8,ats:''},strategy:'nested',known_block:0,known_type:96,known_key:'',target_block:4,target_type:96,limit_seconds:10,max_runs:1,max_attempts:1},help:'仅使用服务端配置的恢复工具。单次运行有时间与尝试次数上限；结果需要重新验证和读取。'},
  'type2-write':{id:'reader.type2-write',field:'type2_write',planFeature:'reader.type2-workflow',planPath:'/api/reader/type2-write-plan',path:'/api/reader/type2-write',template:{uid:'',page:6,expected:'00000000',data:'00000000'},help:'仅允许受支持型号的普通用户页。计划和执行均核对 UID、型号、配置与原始数据；禁止锁位、配置和凭据页。'},
  'classic-write':{id:'reader.classic-write',field:'classic_write',planPath:'/api/reader/classic-write-plan',path:'/api/reader/classic-write',template:{source:{},target:{uid:'',sectors:[]},backup:{},write_trailers:false},help:'提供完整目标备份和显式密钥。始终跳过制造商块 0；尾块默认不写入。计划绑定本次会话和全部输入。'},
  'lf-write':{id:'lf.clone',field:'lf_write',planPath:'/api/physical/lf/plan',path:'/api/physical/lf/write',template:{schema:1,source:{},protocol:'em410x',new_password:'00000000',old_passwords:['00000000'],target_isolated:false},help:'仅限隔离的专用 T55xx 测试卡。回读只验证所选协议身份，不能验证密码或唯一绑定实体目标。'}
 };
 class DeskReaderWorkflows {
  constructor({post,features,run,busy=()=>false,libraryBusy=()=>false,importRead,notice=()=>{}}){
   Object.assign(this,{post,features,run,busy,libraryBusy,importRead,notice});
   this.get=id=>document.getElementById(id);this.epoch=0;this.preview=0;this.result=null;this.plan=null;this.state=null;this.stateInput=null;this.pending=false;
   this.get('workflow-kind').onchange=()=>this.template();this.get('workflow-template').onclick=()=>this.template();
   this.get('workflow-json').addEventListener('input',()=>this.invalidate());
   this.get('workflow-confirm').onchange=()=>this.sync();this.get('workflow-check').onclick=()=>run('hardware',()=>this.check());
   this.get('workflow-read').onclick=()=>run('hardware',()=>this.read());this.get('workflow-plan').onclick=()=>run('hardware',()=>this.makePlan());this.get('workflow-write').onclick=()=>run('hardware',()=>this.write());
   this.get('workflow-import').onclick=()=>this.importFromButton();this.get('workflow-export').onclick=()=>this.export();
   document.addEventListener('desk-feature-device',()=>{this.invalidate();this.get('workflow-json').value='';});this.template();
  }
  operation(){return operations[this.get('workflow-kind').value];}
  input(){let value;try{value=JSON.parse(this.get('workflow-json').value);}catch(_){throw Error('请输入有效的操作参数 JSON');}if(!value||typeof value!=='object'||Array.isArray(value))throw Error('操作参数必须为对象');return value;}
  snapshot(){return {epoch:this.epoch,raw:this.get('workflow-json').value,kind:this.get('workflow-kind').value,device:this.features.device};}
  current(value){return !!value&&value.epoch===this.epoch&&value.raw===this.get('workflow-json').value&&value.kind===this.get('workflow-kind').value&&value.device===this.features.device;}
  available(){return this.state?.state==='available'&&this.current(this.stateInput);}
  invalidate(){this.epoch++;this.preview++;this.plan=null;this.state=null;this.stateInput=null;this.result=null;this.get('workflow-confirm').checked=false;this.get('workflow-result').textContent='';this.sync();}
  template(){this.invalidate();const op=this.operation();this.get('workflow-json').value=JSON.stringify(op.template,null,2);this.get('workflow-help').textContent=op.help;this.sync();}
  canImport(ownDatabaseGroup=false){
   const source=this.result,read=source?.value;
   if(this.busy()||this.pending||(!ownDatabaseGroup&&this.libraryBusy())||!this.importRead||!this.current(source?.input)||this.features.state?.('library.database').state!=='available')return false;
   if(read?.schema!==1||!['classic','type2','lf'].includes(read.kind)||!Number.isInteger(read.units_read)||read.units_read<1||!Number.isInteger(read.requested_units)||read.units_read>read.requested_units||typeof read.complete!=='boolean'||read.complete&&read.units_read!==read.requested_units)return false;
   const hex=(text,length)=>typeof text==='string'&&text.length===length&&/^[0-9a-f]+$/i.test(text);
   if(read.kind==='classic')return hex(read.dump?.data,read.units_read*32);
   if(read.kind==='type2')return hex(read.data,read.units_read*8);
   return read.present===true&&!!read.record&&typeof read.record==='object';
  }
  sync(){const op=this.operation(),blocked=this.busy()||this.pending,available=this.available();this.get('workflow-check').disabled=blocked;this.get('workflow-read').hidden=!!op.planPath;this.get('workflow-plan').hidden=!op.planPath;this.get('workflow-write').hidden=!op.planPath;this.get('workflow-confirm').parentElement.hidden=!op.planPath;
   this.get('workflow-read').disabled=blocked||!available;this.get('workflow-plan').disabled=blocked||!this.features.device;this.get('workflow-write').disabled=blocked||!available||!this.current(this.plan?.input)||!this.get('workflow-confirm').checked;
   this.get('workflow-import').disabled=!this.canImport();this.get('workflow-export').disabled=blocked||!this.current(this.result?.input);
   this.get('workflow-reason').textContent=this.state?.reasons?.map(r=>r.message).join('；')||(!this.features.device?'请先连接设备并读取能力':available?'参数检查通过，实际操作仍会再次校验':'请检查能力；生成计划不执行写入');
  }
  async check(){
   const op=this.operation(),input=this.input(),rev=++this.preview,snapshot=this.snapshot();this.state=null;this.stateInput=null;this.sync();
   const result=await this.post('/api/features/check',{id:op.id,device:snapshot.device,[op.field]:input});
   if(rev!==this.preview||!this.current(snapshot))return;
   if(result.schema_version!==1||result.state?.id!==op.id||!['available','unavailable','unknown'].includes(result.state.state)||!Array.isArray(result.state.reasons))throw Error('能力检查返回格式无效');
   this.state=result.state;this.stateInput=snapshot;this.sync();
  }
  async read(){
   if(this.pending||!this.available()||this.operation().planPath)return;
   const op=this.operation(),input=this.input(),snapshot=this.snapshot();this.result=null;this.pending=true;this.sync();
   try{const value=await this.post(op.path,input);if(this.current(snapshot))this.show(value,'',snapshot);}
   catch(error){if(!error.staleSession&&this.current(snapshot))this.show(error.result,error.message,snapshot);throw error;}
   finally{this.pending=false;this.sync();}
  }
  show(value,error='',snapshot=this.snapshot()){this.result={value,error,input:snapshot};this.get('workflow-result').textContent=JSON.stringify({result:value,error:error||null},null,2);this.sync();}
  async makePlan(){
   if(this.pending||!this.operation().planPath||!this.features.device)return;
   const op=this.operation(),input=this.input(),snapshot=this.snapshot();this.plan=null;this.result=null;this.pending=true;this.get('workflow-confirm').checked=false;this.sync();
   try{
    const payload=op.id==='reader.classic-write'?input:{request:input};const value=await this.post(op.planPath,payload);
    if(!this.current(snapshot))return;
    if(typeof value.confirmation!=='string'||!value.confirmation||!value.plan||typeof value.plan!=='object')throw Error('写入计划格式无效');
    this.plan={value,input:snapshot};this.get('workflow-result').textContent=JSON.stringify(value.plan,null,2);await this.check();
   }finally{this.pending=false;this.sync();}
  }
  async write(){
   const op=this.operation(),plan=this.plan;
   if(this.pending||!op.planPath||!this.current(plan?.input)||!this.get('workflow-confirm').checked||!this.available())throw Error('请重新生成并确认当前输入的写入计划');
   const input=this.input(),snapshot=this.snapshot();if(op.id==='reader.classic-write'){input.confirm=true;input.plan_digest=plan.value.plan.digest;}
   this.plan=null;this.result=null;this.pending=true;this.get('workflow-confirm').checked=false;this.sync();
   try{const value=await this.post(op.path,{request:input,confirmation:plan.value.confirmation,...(op.id==='reader.type2-write'?{confirm:true}:{})});if(this.current(snapshot))this.show(value,'',snapshot);}
   catch(error){if(!error.staleSession&&this.current(snapshot))this.show(error.result,error.message,snapshot);throw error;}
   finally{this.pending=false;this.sync();}
  }
  async importFromButton(){
   // Validate before run() acquires the database group. Only this invocation may
   // ignore that group's own busy flag; hardware and stale-source guards remain.
   if(!this.canImport())throw Error('没有可导入的当前读取结果');
   const permit={source:this.result};this.importOwner=permit;
   try{return await this.run('database',()=>this.import(permit));}
   finally{if(this.importOwner===permit)this.importOwner=null;this.sync();}
  }
  async import(permit){
   const ownDatabaseGroup=!!permit&&permit===this.importOwner&&permit.source===this.result;
   if(!this.canImport(ownDatabaseGroup))throw Error('没有可导入的当前读取结果');
   const source=this.result;await this.importRead(source,()=>this.result===source&&this.current(source.input));
  }
  export(){
   if(!this.current(this.result?.input))return;
   const value=this.result.value?.kind?{schema:1,read_result:this.result.value,error:this.result.error||null}:{schema:1,result:this.result.value,error:this.result.error||null},url=URL.createObjectURL(new Blob([JSON.stringify(value,null,2)],{type:'application/json'})),a=document.createElement('a');a.href=url;a.download='desk-reader-workflow-result.json';a.click();setTimeout(()=>URL.revokeObjectURL(url),0);
  }
 }
 window.DeskReaderWorkflows=DeskReaderWorkflows;
})();
