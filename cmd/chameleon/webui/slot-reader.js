"use strict";
(() => {
 const featureID='slot.read-current';
 class DeskSlotReader {
  constructor({post,features,run,busy=()=>false,libraryBusy=()=>false,importRead}){
   Object.assign(this,{post,features,run,busy,libraryBusy,importRead});this.get=id=>document.getElementById(id);this.epoch=0;this.previewEpoch=0;
   this.get('slot-read-sense').onchange=()=>{this.invalidate();this.preview();};
   this.get('slot-read-go').onclick=()=>run('hardware',()=>this.read());
   this.get('slot-read-import').onclick=()=>run('database',()=>this.import());
   this.get('slot-read-export').onclick=()=>this.export();
   document.addEventListener('desk-feature-device',()=>this.clear());this.clear();
  }
  invalidate(){this.epoch++;this.previewEpoch++;this.result=null;this.get('slot-read-result').replaceChildren();this.sync();}
  clear(){this.observed=null;this.invalidate();this.get('slot-read-source').textContent='请先读取槽位，确认当前激活槽位及工作模式。';const policy=this.features.state(featureID)?.reasons?.filter(r=>['usb_bridge_required','transport_restricted','unsupported_model'].includes(r.code))||[];this.publish({state:policy.length?'unavailable':'unknown',reasons:policy.length?policy:[{message:'请先读取当前槽位信息'}]});}
  observe(info){
   this.invalidate();const active=info?.slots?.find(slot=>slot.active);
   this.observed=active?{slot:active,mode:info.mode,device:this.features.device}:null;
   this.get('slot-read-source').textContent=active?`当前槽位 ${active.number} · ${info.mode==='emulator'?'模拟模式':info.mode==='reader'?'读卡模式':'模式未知'} · HF ${active.hf_name||active.hf_type} / LF ${active.lf_name||active.lf_type}`:'未返回激活槽位，请重新读取。';
   this.preview();
  }
  publish(state){this.features.states.set(featureID,{id:featureID,...state});this.features.render?.();this.features.changed?.();this.sync();}
  input(){
   if(!this.observed||this.observed.device!==this.features.device)throw Error('请重新读取当前槽位信息');
   if(this.observed.mode!=='emulator')throw Error('需要设备已处于模拟模式；此操作不切换模式');
   const sense=Number(this.get('slot-read-sense').value),slot=this.observed.slot;
   return {slot:slot.number,sense,tag_type:sense===2?slot.hf_type:slot.lf_type};
  }
  async preview(){
   const epoch=++this.previewEpoch;let input;
   try{input=this.input();}catch(error){this.publish({state:'unavailable',reasons:[{message:error.message}]});return;}
   this.publish({state:'unknown',reasons:[{message:'正在检查所选通道和固件能力'}]});
   try{
    const result=await this.post('/api/features/check',{id:featureID,device:this.features.device,device_mode:0,slot_read:input});
    if(epoch!==this.previewEpoch)return;
    if(result.schema_version!==1||result.state?.id!==featureID||!['available','unavailable','unknown'].includes(result.state.state)||!Array.isArray(result.state.reasons)||result.state.reasons.some(r=>typeof r?.message!=='string'))throw Error('槽位读取能力结果无效');
    this.publish(result.state);
   }catch(error){if(epoch===this.previewEpoch)this.publish({state:'unknown',reasons:[{message:error.message}]});}
  }
  sync(){
   const result=this.result,source=result?.value?.slot_snapshot,state=this.features.state(featureID);
   this.get('slot-read-go').disabled=this.busy()||state.state!=='available';
   const reason=!result?'请先读取并预览当前槽位':!source||result.value.units_read<1?'没有已确认的数据，无法导入':source.source_changed?'槽位来源已变化，不能导入此结果':!this.importRead?'卡库入口未就绪':this.features.state('library.database').state!=='available'?'卡库能力尚未就绪':this.libraryBusy()?'卡库正在处理或恢复保存版本':'';
   this.get('slot-read-import').disabled=this.busy()||!!reason;this.get('slot-read-import-reason').textContent=reason;
   this.get('slot-read-export').disabled=this.busy()||!result;
  }
  show(value,error=''){
   const out=this.get('slot-read-result'),node=(tag,text)=>{const n=document.createElement(tag);n.textContent=text;return n;};out.replaceChildren();
   const s=value?.slot_snapshot,hex=text=>typeof text==='string'&&/^(?:[0-9a-f]{2})*$/i.test(text);
   const valid=value?.schema===1&&value.kind==='slot'&&s?.schema===1&&s.source==='emulator_slot'&&[1,2].includes(s.sense)&&Number.isInteger(s.slot)&&s.slot>=1&&s.slot<=8&&Number.isInteger(s.units)&&s.units>=1&&s.units<=256&&value.requested_units===s.units&&Number.isInteger(s.units_read)&&s.units_read>=0&&s.units_read<=s.units&&value.units_read===s.units_read&&Number.isInteger(s.unit_bytes)&&s.unit_bytes>=1&&s.unit_bytes<=16&&hex(s.data)&&s.data.length===s.units_read*s.unit_bytes*2&&typeof s.complete==='boolean'&&value.complete===s.complete&&(!s.complete||s.units_read===s.units)&&typeof s.source_changed==='boolean'&&Array.isArray(s.unknown_ranges)&&Array.isArray(s.uncaptured)&&hex(s.sha256)&&s.sha256.length===64;
   if(!valid){this.result=null;out.append(node('p','槽位快照格式无效，未启用导出或导入'));this.sync();return;}
   this.result={value,error};this.get('slot-read-name').value=s.nickname??`槽位 ${s.slot} · ${s.sense===2?'HF':'LF'}`;
   if(error)out.append(node('p','读取未完成：'+error));
   out.append(node('h4',`槽位 ${s.slot} · ${s.sense===2?'HF':'LF'} · 类型 ${s.tag_type}`),node('p',`${s.complete?'所选内存 / 身份读取完成':'部分 / 中断结果'}：${s.units_read} / ${s.units} 个单元 · 通道${s.metadata_read?(s.enabled?'启用':'停用'):'未确认'} · 昵称 ${s.metadata_read?(s.nickname??'未设置'):'未确认'}`));
   if(s.identity)out.append(node('p',`模拟身份：UID ${s.identity.uid} · ATQA ${s.identity.atqa}（数值顺序）· SAK ${s.identity.sak} · ATS ${s.identity.ats||'空'}`));
   if(s.record)out.append(node('p',`LF 模拟身份：${s.record.protocol} · ${s.record.id}`));
   if(s.source_changed)out.append(node('p','检测到槽位来源变化；此结果只能导出检查，不能导入卡库。'));
   out.append(node('p','未知单元以 ?? 显示，不填充零。此结果来自模拟器内存，不证明实体卡内容或 Flash 已保存。配置、版本、签名和计数器不在本次读取范围。'));
   const lines=[];for(let i=0;i<s.units;i++){const row=i<s.units_read?s.data.slice(i*s.unit_bytes*2,(i+1)*s.unit_bytes*2):'??'.repeat(s.unit_bytes);lines.push(`${i}: ${row.match(/../g).join(' ')}`)}out.append(node('pre',lines.join('\n')));
   const details=document.createElement('details');details.append(node('summary','快照校验值与原始结果'),node('pre',JSON.stringify(value,null,2)));out.append(details);this.sync();
  }
  async read(){
   if(this.features.state(featureID).state!=='available')return;
   const epoch=++this.epoch;this.result=null;this.sync();
   try{const input=this.input();this.get('slot-read-result').textContent='正在读取当前槽位；可在后台任务中查看进度或取消。';const value=await this.post('/api/slot/read-current',input);if(epoch===this.epoch)this.show(value);}
   catch(error){if(error.staleSession||epoch!==this.epoch)return;this.features.clear?.('槽位读取中断，请核对设备状态并重新读取槽位');if(error.result)this.show(error.result,error.message);else{this.get('slot-read-result').textContent=error.message;this.sync();}}
  }
  async import(){
   const result=this.result,name=this.get('slot-read-name').value,epoch=this.epoch;
   if(!result||result.value.units_read<1||result.value.slot_snapshot?.source_changed||!this.importRead)return;
   await this.importRead({...result,name},()=>this.result===result&&epoch===this.epoch&&name===this.get('slot-read-name').value);
  }
  export(){
   if(!this.result)return;
   const value={schema:1,read_result:this.result.value,error:this.result.error||''};
   const url=URL.createObjectURL(new Blob([JSON.stringify(value,null,2)],{type:'application/json'})),a=document.createElement('a');a.href=url;a.download=`slot-${value.read_result.slot_snapshot.slot}-${value.read_result.complete?'complete':'partial'}.json`;a.click();setTimeout(()=>URL.revokeObjectURL(url),0);
  }
 }
 window.DeskSlotReader=DeskSlotReader;
})();
