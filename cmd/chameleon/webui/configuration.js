"use strict";
(() => {
 class DeskConfiguration {
  constructor({api,post,features,run,writeAllowed}){
   Object.assign(this,{api,post,features,run,writeAllowed});this.revisions={settings:0,slot:0};
   const read=id=>document.getElementById(id);this.read=read;
   read('settings-read').onclick=()=>run('hardware',()=>this.load('settings'));
   read('slot-edit-read').onclick=()=>run('hardware',()=>this.load('slot'));
   for(const kind of ['settings','slot']){
    read(kind==='settings'?'settings-apply':'slot-edit-apply').onclick=()=>run('hardware',()=>this.apply(kind));
    read(kind==='settings'?'settings-editor':'slot-editor').addEventListener('input',()=>this.preview(kind));
    read(kind==='settings'?'settings-editor':'slot-editor').addEventListener('change',event=>{if(event.target.tagName==='SELECT'||event.target.type==='checkbox')this.preview(kind);});
   }
   document.addEventListener('desk-feature-device',()=>{this.clear();this.preview('settings');this.preview('slot');});
   const tags=[[100,'EM410X'],[101,'EM410X/16'],[102,'EM410X/32'],[103,'EM410X/64'],[104,'EM410X Electra'],[150,'PAC'],[170,'Viking'],[180,'Jablotron'],[200,'HIDProx'],[201,'ioProx'],[202,'Pyramid'],[310,'IDTECK'],[1000,'Mini'],[1001,'Classic 1K'],[1002,'Classic 2K'],[1003,'Classic 4K'],[1100,'NTAG213'],[1101,'NTAG215'],[1102,'NTAG216'],[1103,'Ultralight'],[1104,'Ultralight C'],[1105,'Ultralight EV1 640'],[1106,'Ultralight EV1 1312'],[1107,'NTAG210'],[1108,'NTAG212'],[3000,'ISO14443-4'],[3001,'SEOS']];
   for(const [value,label]of tags){const o=document.createElement('option');o.value=value;o.textContent=label+' ('+value+')';read('slot-edit-type').append(o);}
   this.refreshFields();
  }
  clear(){for(const kind of ['settings','slot']){this.revisions[kind]++;this.read(kind==='settings'?'settings-result':'slot-edit-result').textContent='';}this.read('settings-value').value='';}
  refreshFields(){
   const field=this.read('settings-field').value;this.read('settings-value').type=field==='ble_key'?'password':'text';
   this.read('settings-value').placeholder=({animation:'0–3',sleep_seconds:'0–255 秒',long_press_ms:'0–65535 毫秒',ble_key:'六位数字 PIN',ble_pairing:'true 或 false'})[field]||'按钮动作 0–5';
   const action=this.read('slot-edit-action').value;
   for(const [id,show]of [['slot-edit-name-label',action==='nickname'],['slot-edit-type-label',['type','initialize'].includes(action)],['slot-edit-enabled-label',action==='enable'],['slot-edit-confirm-label',action==='initialize']])this.read(id).hidden=!show;
   const hf=this.read('slot-edit-sense').value==='2';for(const o of this.read('slot-edit-type').options)o.disabled=(Number(o.value)>=1000)!==hf;
   if(this.read('slot-edit-type').selectedOptions[0]?.disabled)this.read('slot-edit-type').value=hf?'1001':'100';
  }
  input(kind){
   const bool=value=>{if(value!=='true'&&value!=='false')throw Error('布尔值必须为 true 或 false');return value==='true';};
   if(kind==='settings'){
    const field=this.read('settings-field').value,value=this.read('settings-value').value;
    let parsed=value;if(field==='ble_pairing')parsed=bool(value);else if(field!=='ble_key'){if(!/^\d+$/.test(value))throw Error('请输入非负整数');parsed=Number(value);if(!Number.isSafeInteger(parsed))throw Error('数值超出范围');}
    return {patch:{[field]:parsed},persist:this.read('settings-persist').checked};
   }
   const action=this.read('slot-edit-action').value;
   const edit={slot:Number(this.read('slot-edit-slot').value),sense:Number(this.read('slot-edit-sense').value),action,persist:this.read('slot-edit-persist').checked,confirm_initialize:action==='initialize'&&this.read('slot-edit-confirm').checked};
   if(action==='nickname'){edit.name=this.read('slot-edit-name').value;if(!edit.name)throw Error('请输入昵称或选择删除昵称');}
   if(action==='enable')edit.enabled=this.read('slot-edit-enabled').value==='true';
   if(['type','initialize'].includes(action))edit.tag=Number(this.read('slot-edit-type').value);
   return edit;
  }
  publish(kind,state){const id=kind==='settings'?'device.configure':'slot.configure';this.features.states.set(id,{id,...state});this.features.render();this.features.changed();}
  async preview(kind){
   this.refreshFields();const revision=++this.revisions[kind];const id=kind==='settings'?'device.configure':'slot.configure';
   this.publish(kind,{state:'unknown',reasons:[{code:'device_unknown',message:'请读取设备能力'}]});
   if(!this.features.device||!this.writeAllowed())return;
   let value;try{value=this.input(kind);}catch(error){this.publish(kind,{state:'unavailable',reasons:[{code:'invalid_parameters',message:error.message}]});return;}
   this.publish(kind,{state:'unknown',reasons:[{message:'正在检查所选参数'}]});
   try{const body={id,device:this.features.device};if(kind==='settings'){body.settings_patch=value.patch;body.persist=value.persist;}else body.slot_edit=value;
    const result=await this.post('/api/features/check',body);
    if(revision!==this.revisions[kind])return;
    if(result.schema_version!==1||result.state?.id!==id||!['available','unavailable','unknown'].includes(result.state.state)||!Array.isArray(result.state.reasons))throw Error('功能检查结果无效');
    this.publish(kind,result.state);
   }catch(error){if(revision===this.revisions[kind])this.publish(kind,{state:'unknown',reasons:[{message:error.message}]});}
  }
  result(kind,value){
   const out=this.read(kind==='settings'?'settings-result':'slot-edit-result'),node=(tag,text)=>{const n=document.createElement(tag);if(text!=null)n.textContent=text;return n;};out.replaceChildren();
   if(value.error)out.append(node('p','操作未确认：'+value.error));
   const data=value.error?value.result:value;
   if(data){
    const settings=data.settings||(kind==='settings'&&Object.hasOwn(data,'animation')?data:null);
    if(settings){const list=node('dl');for(const [key,label]of [['animation','动画模式'],['sleep_seconds','休眠秒数'],['long_press_ms','长按阈值（毫秒）'],['short_a','A 短按动作'],['short_b','B 短按动作'],['long_a','A 长按动作'],['long_b','B 长按动作'],['ble_pairing','BLE 配对']]){const v=settings[key];list.append(node('dt',label),node('dd',v==null?'未返回 / 不支持':typeof v==='boolean'?(v?'开启':'关闭'):String(v)));}out.append(list);if(settings.unsupported?.length)out.append(node('p','固件不支持的可选字段命令：'+settings.unsupported.join('、')));}
    const slots=Array.isArray(data)?data:data.after?[data.after]:[];
    for(const slot of slots)out.append(node('p',`槽位 ${slot.number} · HF ${slot.hf_name||slot.hf_type}（${slot.hf_enabled?'启用':'停用'}）昵称：${slot.hf_nickname??'未返回 / 未设置'} · LF ${slot.lf_name||slot.lf_type}（${slot.lf_enabled?'启用':'停用'}）昵称：${slot.lf_nickname??'未返回 / 未设置'}`));
    if(Object.hasOwn(data,'readback_verified'))out.append(node('p',data.readback_verified?'设置回读已验证':'设置回读未确认'));
    if(Object.hasOwn(data,'configuration_verified'))out.append(node('p',data.configuration_verified?'槽位配置回读已验证':'槽位配置回读未确认'));
    if(data.action?.startsWith('nickname'))out.append(node('p','昵称：'+(data.nickname??'未设置')));
    if(data.initialization_acknowledged)out.append(node('p','初始化命令已确认；尚未验证默认卡片内存。'));
    if(Object.hasOwn(data,'save_confirmed'))out.append(node('p',data.save_confirmed?'保存命令已确认':'保存未确认（可能未请求或操作未完整成功）'));
   }
   const details=node('details');details.append(node('summary','原始结果'),node('pre',JSON.stringify(value,null,2)));out.append(details);
  }
  async load(kind){const revision=++this.revisions[kind];const out=this.read(kind==='settings'?'settings-result':'slot-edit-result');out.textContent='正在读取…';try{const value=await this.api(kind==='settings'?'/api/settings/read':'/api/slot/details');if(revision===this.revisions[kind])this.result(kind,value);}catch(error){if(!error.staleSession&&revision===this.revisions[kind]){this.features.clear?.("配置读取失败，请重新读取设备能力");out.textContent=error.message;}}}
  async apply(kind){
   const id=kind==='settings'?'device.configure':'slot.configure';if(this.features.state(id).state!=='available'||!this.writeAllowed())return;
   const revision=++this.revisions[kind],out=this.read(kind==='settings'?'settings-result':'slot-edit-result');let value;
   try{value=this.input(kind);const summary=kind==='settings'?'修改设置 '+this.read('settings-field').selectedOptions[0].textContent:'槽位 '+value.slot+' · '+(value.sense===2?'HF':'LF')+' · '+value.action;
    if(!window.confirm(summary+'？写入后将回读；失败不会自动重试。'))return;
    out.textContent='正在修改并回读；进度和取消请查看后台任务。';
    const pending=this.post(kind==='settings'?'/api/settings/configure':'/api/slot/edit',value);
    if(kind==='settings'&&value.patch.ble_key!=null)this.read('settings-value').value='';
    const result=await pending;if(revision===this.revisions[kind])this.result(kind,result);
   }catch(error){if(!error.staleSession&&revision===this.revisions[kind]){this.features.clear?.("修改未确认，请重新读取设备能力并核对状态");this.result(kind,{error:error.message,result:error.result});}}
   finally{this.preview(kind);}
  }
 }
 window.DeskConfiguration=DeskConfiguration;
})();
