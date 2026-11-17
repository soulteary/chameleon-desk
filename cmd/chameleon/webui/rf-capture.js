/* Hardware acquisition stays session-bound; file analysis is a separate action. */
(() => {
 'use strict';
 class DeskRFCapture {
  constructor({post,api,features,run,busy=()=>false,analysisBusy=()=>false,session=()=>'',useFile,impact=()=>{}}){
   Object.assign(this,{post,api,features,run,busy,analysisBusy,session,useFile,impact});this.get=id=>document.getElementById(id);this.epoch=0;
   this.fields=['rf-capture-action','rf-capture-duration','rf-capture-block','rf-capture-key-type','rf-capture-key','rf-capture-confirm'];
   for(const id of this.fields)this.get(id).oninput=()=>{if(id!=='rf-capture-confirm')this.get('rf-capture-confirm').checked=false;this.invalidate();this.sync();};
   this.get('rf-capture-check').onclick=()=>run('hardware',()=>this.check());this.get('rf-capture-go').onclick=()=>run('hardware',()=>this.capture());
   this.get('rf-capture-query').onclick=()=>run('rf-query',()=>this.query());this.get('rf-capture-export').onclick=()=>this.export();
   this.get('rf-capture-analyze').onclick=()=>{if(this.result?.file&&!this.pending&&!this.analysisBusy())this.useFile(this.result.file);};
   document.addEventListener('desk-feature-device',()=>{this.get('rf-capture-confirm').checked=false;this.invalidate();this.sync();});this.sync();
  }
  invalidate(){this.epoch++;this.state=null;this.checked=null;this.result=null;this.get('rf-capture-result').textContent='';}
  feature(){return this.get('rf-capture-action').value.startsWith('hf-')?'rf.hf-trace':'rf.lf-samples';}
  input(){
   const action=this.get('rf-capture-action').value,auth=action==='hf-auth';
   const number=id=>{const v=Number(this.get(id).value);if(!Number.isSafeInteger(v)||v<0)throw Error('时长及块地址必须为非负整数。');return v;};
   const value={action,duration_ms:action==='lf-adc'?0:number('rf-capture-duration'),acknowledge_slot_reload:this.get('rf-capture-confirm').checked};
   if(auth){value.block=number('rf-capture-block');value.key_type=number('rf-capture-key-type');value.key=this.get('rf-capture-key').value.trim();if(!/^[0-9a-f]{12}$/i.test(value.key))throw Error('认证 trace 需要显式提供六字节已知密钥。');}
   return value;
  }
  snapshot(){return {epoch:this.epoch,session:this.session(),device:this.features.device};}
  current(s){return s.epoch===this.epoch&&s.session===this.session()&&s.device===this.features.device;}
  sync(){
   const action=this.get('rf-capture-action').value,blocked=this.pending||this.busy(),auth=action==='hf-auth',adc=action==='lf-adc';
   this.get('rf-capture-auth').hidden=!auth;this.get('rf-capture-duration-label').hidden=adc;this.get('rf-capture-duration').max=action==='lf-samples'?'10000':'30000';
   this.get('rf-capture-help').textContent=action==='hf-sniff'?'HF 1..30000 ms；必须已有可用的 HF 模拟槽位。原始 RX/TX 可能具有不同奇偶位布局；不自动解码。':auth?'主动认证 1..30000 ms；使用已知密钥，不猜测、不恢复密钥。固件返回的部分防冲突帧可能为合成。':adc?'使用固件固定采集；采样率和实际时长未知。':'LF 1..10000 ms；125000 Hz 是 SDK / 固件约定值。固件缓冲满可能提前结束，不能据请求时长推断完整采样。';
   this.get('rf-capture-check').disabled=blocked||!this.features.device;this.get('rf-capture-go').disabled=blocked||!this.checked||!this.current(this.checked)||this.state?.state!=='available'||!this.get('rf-capture-confirm').checked;
   const catalog=this.features.state(this.feature()),reason=this.state||catalog;
   this.get('rf-capture-reason').textContent=this.state?.state==='available'?'参数能力检查通过；服务端仍会校验实际设备和命令超时。':(reason?.reasons||[]).map(r=>r.code==='parameters_required'?'请确认本次采集影响并重新检查参数能力。':r.code==='writes_disabled'?'HF sniff 需要以 --allow-writes 启动服务。':r.message).join('；')||'请确认槽位影响并检查采集能力。';
   this.get('rf-capture-file').textContent=this.result?.file?this.result.file.name+' · '+this.result.file.format+' · '+this.result.file.data.length/2+' 文件字节；转入离线分析后可选择布局、选区及导出原文件。':'';
   this.get('rf-capture-query').disabled=this.pending;this.get('rf-capture-export').disabled=this.pending||!this.result;this.get('rf-capture-analyze').disabled=this.pending||this.analysisBusy()||!this.result?.file;
  }
  async check(){
   const input=this.input(),s=this.snapshot();this.state=null;this.checked=null;this.sync();
   const value=await this.post('/api/features/check',{id:this.feature(),device:s.device,rf_capture:input});if(!this.current(s))return;
   if(value?.schema_version!==1||value.state?.id!==this.feature()||!['available','unavailable','unknown'].includes(value.state.state)||!Array.isArray(value.state.reasons))throw Error('采集能力响应无效。');
   this.state=value.state;this.checked=s;this.sync();
  }
  async capture(){
   if(this.pending||!this.checked||!this.current(this.checked)||this.state?.state!=='available'||!this.get('rf-capture-confirm').checked)return;
   const input=this.input(),s=this.snapshot();this.result=null;this.pending=true;this.get('rf-capture-status').textContent='正在执行一次采集；可在任务中心请求取消，不会自动重放。';this.sync();
   try{const value=await this.post(input.action==='hf-sniff'?'/api/rf/hf-sniff':input.action==='hf-auth'?'/api/rf/hf-auth':'/api/rf/lf-capture',input);if(this.current(s))this.show(value);}
   catch(error){if(this.current(s)&&!error.staleSession){if(error.result?.kind==='rf_capture')this.show(error.result);this.get('rf-capture-status').textContent='采集未完整结束：'+error.message+'；请核对设备模式和槽位。';}throw error;}
   finally{if(this.current(s)){this.impact();this.get('rf-capture-confirm').checked=false;this.state=null;this.checked=null;}this.pending=false;this.sync();}
  }
  show(value){
   if(value?.schema_version!==1||value.kind!=='rf_capture'||!['hf-sniff','hf-auth','lf-samples','lf-adc'].includes(value.action)||!['captured','no_capture','failed'].includes(value.status)||!Array.isArray(value.notes))throw Error('采集结果格式无效。');
   const file=value.file;if(file&&(!['hf-json','lf-json','lf-u8'].includes(file.format)||typeof file.data!=='string'||file.data.length<2||file.data.length>2097152||file.data.length%2||!/^[0-9a-f]+$/i.test(file.data)))throw Error('采集文件超限或无效。');
   this.result=value;this.get('rf-capture-result').textContent=JSON.stringify(value,null,2);
   this.get('rf-capture-status').textContent=(value.status==='captured'?'已返回采集数据；不保证完整交换或请求时段。':value.status==='no_capture'?'本次没有采集数据；不会自动重试。':'未确认采集结果。')+' 原模式 '+(value.mode_before||'未知')+' → 回读 '+(value.mode_after||'未知')+'；'+(value.mode_restored?'已回读确认原模式，但未验证槽位内存恢复。':'模式未确认，请核对。')+' 来源会话 '+(value.session_id||'未知')+'。';this.sync();
  }
  async query(){
   const id=this.get('rf-capture-task').value.trim();if(!/^[0-9a-f]{32}$/.test(id))throw Error('请输入完整的 32 位采集任务 ID。');const epoch=++this.epoch;this.pending=true;this.state=null;this.checked=null;this.result=null;this.get('rf-capture-result').textContent='';this.get('rf-capture-status').textContent='正在查询既有任务；不会重新采集。';this.sync();
   try{const task=await this.api('/api/tasks/'+id);if(epoch!==this.epoch)return;
    if(!['rf.hf-sniff','rf.hf-auth','rf.lf-capture'].includes(task.operation_id)||!['succeeded','failed','interrupted'].includes(task.state)||task.result_retained===false||!task.result)throw Error('采集正文未保留或任务未结束；不会自动重新采集。');
    const result=task.result.kind==='rf_capture'?task.result:task.result.result;
    if(result?.session_id!==task.session_id)throw Error('采集来源会话与任务不一致。');this.show(result);this.get('rf-capture-status').textContent+=' 已查询历史任务；此结果不代表当前设备状态。';
   }catch(error){if(epoch===this.epoch)this.get('rf-capture-status').textContent=error.message;throw error;}finally{this.pending=false;this.sync();}
  }
  export(){if(!this.result)return;const url=URL.createObjectURL(new Blob([JSON.stringify(this.result,null,2)],{type:'application/json'})),a=document.createElement('a');a.href=url;a.download='desk-rf-capture.json';a.click();setTimeout(()=>URL.revokeObjectURL(url),1000);}
 }
 window.DeskRFCapture=DeskRFCapture;
})();
