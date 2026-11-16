"use strict";
(() => {
 class DeskLibraryEditor {
  constructor({post,run,getText,setText,changed,busy,features,notice,display=()=>{}}) {
   Object.assign(this,{post,run,getText,setText,changed,busy,features,notice,display});this.get=id=>document.getElementById(id);this.dirty=false;this.card=null;this.revision=0;
   for(const id of ['card-edit-name','card-edit-rows'])this.get(id).addEventListener('input',()=>{this.dirty=true;this.failure="";this.revision++;this.invalidatePromotion();this.sync();});
   this.memory=window.DeskMemoryEditor?new window.DeskMemoryEditor({post,changed:()=>this.sync()}):null;
   this.get('card-edit-recovery-go').onclick=()=>run('database',()=>this.restoreDraft());
   this.get('card-edit-export').onclick=()=>this.exportDraft();
   this.get('card-promote-classic').onclick=()=>run('database',()=>this.promote());
   for(const id of ['card-type2-password','card-type2-pack','card-type2-confirm'])this.get(id).addEventListener('input',()=>{this.revision++;this.invalidatePromotion();this.previewReady=this.previewPromotion();});
   this.get('card-lf-apply').onclick=()=>{if(this.busy()||this.pending)return;let identity;try{identity=this.lfIdentity();}catch(error){this.failure=error.message;this.sync();return;}if(identity.protocol==='ioprox'&&this.hardwareBusy())return;return run(identity.protocol==='ioprox'?'hardware':'database',()=>this.applyLFIdentity());};
   this.get('card-repair-preview').onclick=()=>run('database',()=>this.selectedRepair(true));this.get('card-repair-apply').onclick=()=>run('database',()=>this.selectedRepair(false));
   for(const id of ['card-lf-identity','card-selected-repair','card-merge-options','card-merge-id'])this.get(id).addEventListener('input',()=>{this.revision++;this.repairPreview=null;this.get('card-repair-result').textContent='';if(id==='card-lf-identity'){this.invalidateLFIdentity();this.lfPreviewReady=this.previewLFIdentity();}this.sync();});
   this.get('card-merge-preview').onclick=()=>run('database',()=>this.merge(true));this.get('card-merge-go').onclick=()=>run('database',()=>this.merge(false));
   this.get('card-edit-save').onclick=()=>run('database',()=>this.save());
   this.get('card-edit-discard').onclick=()=>{if(this.dirty&&!window.confirm('放弃尚未保存到卡库的编辑？'))return;this.clear();};
   this.get('read-import-go').onclick=()=>run('database',async()=>{
    const envelope=JSON.parse(this.get('read-import-json').value);
    if(envelope.schema!==1||!envelope.read_result)throw Error('请选择 Desk 读取结果 JSON');
    const original=this.get('read-import-json').value;
    await this.importRead({value:envelope.read_result,error:envelope.error},()=>this.get('read-import-json').value===original);
   });
   document.addEventListener('desk-feature-device',()=>{this.invalidateLFIdentity();this.lfPreviewReady=this.previewLFIdentity();this.sync();});
   this.sync();
  }
  allowed(){return this.features.state('library.database').state==='available';}
  invalidatePromotion(){this.promotionEpoch=(this.promotionEpoch||0)+1;this.promotion=null;
   if(this.features.states){for(const id of ['library.promote-classic','library.promote-lf','library.promote-type2','library.promote-slot'])this.features.states.set(id,{id,state:'unknown',reasons:[{code:'parameters_required',message:'Select and check a saved read draft'}],required_commands:[]});this.features.render?.();}
  }
  sync(){
   const changed=this.card&&this.base!==this.getText(),blocked=this.busy()||this.pending||!this.allowed();
   const classic=this.card?.desk_read?.read_result?.kind==='classic',lf=this.card&&!this.card.desk_read&&this.lfProtocol(this.card.tag),saved=!!this.card&&!this.dirty&&!changed;
   this.get('card-lf-editor').hidden=!lf;this.get('card-selected-repair-tools').hidden=!classic;
   for(const id of ['card-type2-password','card-type2-pack','card-type2-confirm','card-lf-identity','card-selected-repair','card-merge-options','card-merge-id'])this.get(id).disabled=!!this.busy()||!!this.pending;
   let lfReason=!lf?'请选择支持的标准 LF 卡片':!saved?'请先保存或放弃当前编辑并重新选择卡片':blocked?'卡库正在处理或能力尚未就绪':'';
   if(!lfReason){try{const identity=this.lfIdentity();if(identity.protocol==='ioprox'){const state=this.lfState||{state:'unknown',reasons:[{message:'正在检查 ioProx 编码所需固件命令'}]};if(this.hardwareBusy())lfReason='设备操作正在进行';else if(state.state!=='available')lfReason=state.reasons?.map(r=>r.code==='missing_commands'?'固件未公布所需命令：'+(r.commands||[]).join('、'):r.message).join('；')||'ioProx 固件编码能力尚未就绪';}}catch(error){lfReason=error.message;}}
   this.get('card-lf-apply').disabled=!!lfReason;this.get('card-lf-reason').textContent=lfReason||'校验后仅更新卡库中的编码 ID，保留原类型与其他元数据';
   const repairReason=!classic?'请选择已保存的 Classic 读取草稿':!saved?'请先保存或放弃当前编辑':blocked?'卡库正在处理或能力尚未就绪':'';
   this.get('card-repair-preview').disabled=!!repairReason;this.get('card-repair-apply').disabled=!!repairReason||!this.repairPreview;this.get('card-repair-reason').textContent=repairReason||'先预览补读证据，再明确处理冲突并生成新草稿';
   this.get('card-edit-save').disabled=blocked||!this.card||!this.dirty||changed;
   this.get('card-edit-discard').disabled=this.busy()||!this.card;
   this.get('card-edit-export').disabled=this.busy()||!this.card;
   for(const id of ['card-edit-name','card-edit-rows'])this.get(id).disabled=this.busy()||!this.card;
   this.get('card-promote-classic').disabled=blocked||!this.card?.desk_read||this.dirty||changed||this.promotion?.state!=='available';
   this.get('card-promote-reason').textContent=this.promotion?.state==='available'?'':this.promotion?.reasons?.map(r=>r.message).join('；')||'请选择保存的读取草稿并检查转换条件';
   this.get('card-type2-options').hidden=this.card?.desk_read?.read_result?.kind!=='type2';
   this.get('card-merge-go').disabled=blocked||!classic||this.dirty||changed;this.get('card-merge-preview').disabled=blocked||!classic||this.dirty||changed;
   this.get('read-import-go').disabled=blocked;this.get('card-edit-recovery-go').disabled=blocked;this.memory?.sync(this.busy()||!this.card);
   const reason=!this.allowed()?'卡库能力尚未就绪，请重试读取功能清单':!this.card?'请先选择读取草稿':!this.card.desk_read?'当前卡片不是读取草稿':!['classic','lf','type2','slot'].includes(this.card.desk_read.read_result?.kind)?'此读取类型不支持转换':changed?'卡库已变化，请重新选择草稿':this.dirty?'请先保存或放弃编辑，再转换已保存的草稿':this.busy()||this.pending?'卡库正在处理':!this.promotion?'正在校验转换条件':this.promotion.state!=='available'?this.promotion.reasons.map(r=>r.message).join('；')||'转换条件未满足':'';
   this.get('card-promote-classic').disabled=!!reason;
   this.get('card-promote-reason').textContent=reason||'转换生成新卡片，原始读取草稿保留；此操作不写设备或实体卡片。';
   const origin=this.card?.desk_read_origin,slot=origin?.draft?.read_result?.slot_snapshot;
   this.get('card-slot-origin').hidden=origin?.source!=='emulator_slot'&&origin?.draft?.read_result?.kind!=='slot';
   this.get('card-slot-origin').textContent=slot?`来源：模拟器槽位 ${slot.slot} · ${slot.sense===2?'HF':'LF'} · 类型 ${slot.tag_type}；原草稿 ${origin.source_card_id}。身份和原始快照保留于来源记录；配置、版本、签名和计数器未采集，实体卡写入未开放。`:'';
   this.get('card-edit-state').setAttribute('role',this.failure?'alert':'status');
   this.get('card-edit-state').textContent=this.failure?'未保存：'+this.failure:changed?'卡库已变化；编辑草稿仍保留，请导出草稿或放弃后重新选择卡片':this.dirty?'编辑尚未保存到卡库；关闭或刷新页面前请保存':this.card?'编辑已加入卡库；本地持久化状态见上方':'请在卡库中查看一张卡片后编辑';
   this.get('read-import-reason').textContent=!this.allowed()?'卡库能力尚未就绪，请重试读取功能清单':this.busy()?'卡库正在处理或读取保存版本':'';
  }
  clear(){this.invalidateLFIdentity();this.invalidatePromotion();this.failure="";this.card=null;this.dirty=false;this.repairPreview=null;this.revision++;this.get('card-edit-name').value='';this.get('card-edit-rows').value='';this.memory?.open(null);this.get('card-edit-limit').textContent='';this.sync();}
  open(card){
   if(this.dirty&&!window.confirm('选择卡片将替换尚未保存的编辑，确认继续？'))return;
   const draft=card.desk_read;
   let rows;
   if(draft){if(draft.schema!==1||!Array.isArray(draft.rows)||!draft.rows.every(v=>typeof v==='string'))throw Error('读取草稿格式无效');rows=draft.rows;}
   else if([1000,1001,1002,1003,1100,1101,1102,1103,1104,1105,1106,1107,1108].includes(card.tag)&&Array.isArray(card.data)&&card.data.length<=256)rows=card.data.map(row=>row.map(v=>v.toString(16).padStart(2,'0')).join(''));
   else if([100,101,102,103,104,150,170,180,200,201,310].includes(card.tag)&&typeof card.uid==='string')rows=[card.uid];
   else {this.clear();this.get('card-edit-limit').textContent='此卡片类型没有受支持的数据编辑入口';return;}
   this.invalidateLFIdentity();this.invalidatePromotion();this.repairPreview=null;this.failure="";this.card=card;this.base=this.getText();this.dirty=false;this.revision++;
   this.get('card-edit-name').value=card.name;document.dispatchEvent(new CustomEvent('desk-library-card',{detail:{id:card.id}}));
   this.get('card-repair-result').textContent='';this.get('card-selected-repair').value='';
   const protocol=!draft&&this.lfProtocol(card.tag);this.get('card-lf-identity').value=protocol?JSON.stringify({protocol,tag_type:card.tag,...(protocol==='ioprox'?{ioprox:{raw:card.uid?.slice(8,24)||''}}:{id:card.uid})},null,2):'';
   this.get('card-edit-rows').value=rows.map(row=>row.match(/.{2}/g)?.join(' ')||'').join('\n');
   const r=draft?.read_result;
   this.get('card-edit-limit').textContent=draft?.read_result?.kind==='file'?`外部文件来源草稿 · ${r.file_observation?.format} · ${r.file_observation?.unknown_bytes} 个未知字节；文件声明不是实体卡观察，未知字节不能填值。本阶段保留为 JSON 草稿，不转换为标准卡片。`:draft?.read_result?.kind==='slot'?`槽位快照草稿 · 槽位 ${r.slot_snapshot?.slot} · 类型 ${r.slot_snapshot?.tag_type} · ${r.units_read}/${r.requested_units} 单元；身份与原始数据保留，未知字节不能填值。此来源不作为实体卡读取证据。`:draft?`Desk 读取草稿 · ${r.kind} · ${r.complete?'请求范围读取完成':'部分 / 中断读取'} · ${r.units_read} / ${r.requested_units} 单元。行号从 0 开始；?? 为未知字节，不能补值。原始读取结果保留。未转换的草稿禁止槽位加载、NDEF 与二进制 / 镜像转换；符合条件的 Classic、Type2 和 LF 可显式转换，JSON 导出保留标记。`:'每行对应一个块或页，从 0 开始；保持行数与宽度，未知空行必须保留。只修改文件，不写实体卡片。';
   for(const id of ['card-type2-password','card-type2-pack'])this.get(id).value='';this.get('card-type2-confirm').checked=false;this.memory?.open(card);this.sync();this.previewReady=this.previewPromotion();this.lfPreviewReady=this.previewLFIdentity();
  }
  async importRead(result,current=()=>true){
   if(!this.allowed())throw Error('卡库能力尚未就绪');
   if(this.dirty)throw Error('请先保存或放弃当前卡片编辑');
   const original=this.getText(),value=await this.request({op:'import-read',database:JSON.parse(original),read_result:result.value,read_error:result.error||'',name:result.name||(result.value.kind==='slot'?result.value.slot_snapshot?.nickname||`槽位 ${result.value.slot_snapshot?.slot} 快照`:`${result.value.kind} read ${result.value.uid||result.value.record?.protocol||''}`)});
   if(!current()||this.getText()!==original||this.dirty)throw Error('读取结果或卡库已变化；未导入，请重新操作');
   this.commit(value);this.open(value.card);this.display(value.card);this.notice('读取结果已加入卡库，将自动保存；可在卡片库中编辑');
  }
  async restoreDraft(){
   if(!this.allowed())throw Error('卡库能力尚未就绪');
   let value;try{value=JSON.parse(this.get('card-edit-recovery-json').value);}catch(_){throw Error('请选择有效的编辑草稿 JSON');}
   if(value?.schema!==1||value.format!=='chameleon-desk-editor-draft'||typeof value.card?.id!=='string'||typeof value.name!=='string'||typeof value.rows_text!=='string')throw Error('编辑草稿格式无效');
   if(this.dirty&&!window.confirm('恢复文件将替换当前未保存编辑，确认继续？'))return;
   const base=this.getText(),input=this.get('card-edit-recovery-json').value,revision=this.revision;
   const current=await this.request({op:'show',database:JSON.parse(base),id:value.card.id});
   if(base!==this.getText()||input!==this.get('card-edit-recovery-json').value||revision!==this.revision)throw Error('卡库、文件或编辑已变化，未恢复');
   const normalize=node=>node&&typeof node==='object'?Array.isArray(node)?node.map(normalize):Object.fromEntries(Object.keys(node).sort().map(k=>[k,normalize(node[k])])):node,canonical=node=>JSON.stringify(normalize(node));
   if(canonical(current)!==canonical(value.card))throw Error('卡库中的原卡片已变化；保留恢复文件并先比较差异，不会覆盖新版');
   this.dirty=false;this.open(current);this.get('card-edit-name').value=value.name;this.get('card-edit-rows').value=value.rows_text;this.dirty=true;this.revision++;this.invalidatePromotion();this.memory?.render();this.sync();this.notice('编辑草稿已恢复到本页，校验并保存后才会进入卡库');
  }
  async previewPromotion(){
   if(!this.card?.desk_read)return;const kind=this.card.desk_read.read_result?.kind;if(!['classic','lf','type2','slot'].includes(kind))return;const id='library.promote-'+kind;
   const epoch=this.promotionEpoch,card=this.card,base=this.base;
   try{
    const result=await this.post('/api/features/check',{id,card,type2_promotion:kind==='type2'?this.type2Options():undefined});
    if(epoch!==this.promotionEpoch||card!==this.card||base!==this.getText()||this.dirty)return;
    if(result.schema_version!==1||result.state?.id!==id||!['available','unavailable','unknown'].includes(result.state.state)||!Array.isArray(result.state.reasons)||result.state.reasons.some(r=>typeof r?.message!=='string'))throw Error('转换能力检查结果无效');
    this.promotion=result.state;
   }catch(error){if(epoch!==this.promotionEpoch)return;this.promotion={state:'unknown',reasons:[{message:error.message}]};}
   if(this.features.states){this.features.states.set(id,{id,...this.promotion});this.features.render?.();this.features.changed?.();}this.sync();
  }
  async promote(){
   if(!this.allowed()||!this.card?.desk_read||this.dirty||this.base!==this.getText()||this.promotion?.state!=='available')return;
   const original=this.getText(),revision=this.revision,source=this.card;
   const kind=source.desk_read.read_result.kind,options=kind==='type2'?this.type2Options():undefined;
   const value=await this.request({op:'promote-'+kind,database:JSON.parse(original),id:source.id,type2_promotion:options});
   if(this.getText()!==original||revision!==this.revision||this.card!==source)throw Error('草稿或卡库已变化；未导入转换结果');
   if(value.card?.id===source.id)throw Error('转换结果未保留原草稿标识，未导入');
   this.commit(value);this.open(value.card);this.display(value.card);this.notice('标准卡片已加入卡库，将自动保存；原草稿保留');
  }
  type2Options(){return {emulator_password:this.get('card-type2-password').value.trim(),emulator_pack:this.get('card-type2-pack').value.trim(),confirm_emulator_credentials:this.get('card-type2-confirm').checked};}
  async merge(preview){
   if(!this.allowed()||this.card?.desk_read?.read_result?.kind!=='classic'||this.dirty||this.base!==this.getText())throw Error('请选择已保存的 Classic 草稿');
   const base=this.getText(),source=this.card,otherID=this.get('card-merge-id').value.trim(),options=JSON.parse(this.get('card-merge-options').value),revision=this.revision;
   const value=await this.request({op:preview?'merge-classic-preview':'merge-classic',database:JSON.parse(base),id:source.id,other_id:otherID,merge_options:options});
   if(base!==this.getText()||source!==this.card||revision!==this.revision)throw Error('卡库或编辑已变化；合并结果未导入');
   this.get('card-merge-result').textContent=JSON.stringify(value,null,2);if(preview)document.dispatchEvent(new CustomEvent('desk-classic-conflicts',{detail:{source:this.card.id,value}}));
   if(!preview){if(value.card?.id===source.id||value.card?.id===otherID)throw Error('合并结果必须使用新草稿标识，未导入');this.commit(value);this.open(value.card);this.display(value.card);this.notice('合并草稿已加入卡库，原始草稿保留');}
  }
  invalidateLFIdentity(){this.lfEpoch=(this.lfEpoch||0)+1;this.lfState=null;if(this.features.states){this.features.states.set('library.edit-lf-device',{id:'library.edit-lf-device',state:'unknown',reasons:[{code:'parameters_required',message:'Select and check ioProx fields'}],required_commands:[]});this.features.render?.();}}
  async previewLFIdentity(){
   if(!this.card||this.card.desk_read||this.lfProtocol(this.card.tag)!=='ioprox')return;
   const epoch=this.lfEpoch,revision=this.revision,base=this.base,card=this.card,input=this.get('card-lf-identity').value,device=this.features.device;
   const current=()=>epoch===this.lfEpoch&&revision===this.revision&&base===this.getText()&&card===this.card&&input===this.get('card-lf-identity').value&&device===this.features.device&&!this.dirty;
   try{const identity=this.lfIdentity(),value=await this.post('/api/features/check',{id:'library.edit-lf-device',device,lf_identity:identity});if(!current())return;if(value.schema_version!==1||value.state?.id!=='library.edit-lf-device'||!['available','unavailable','unknown'].includes(value.state.state)||!Array.isArray(value.state.reasons)||value.state.reasons.some(r=>typeof r?.message!=='string'))throw Error('LF 编码能力检查结果无效');this.lfState=value.state;}catch(error){if(!current())return;this.lfState={state:'unknown',reasons:[{message:error.message}]};}
   if(this.features.states){this.features.states.set('library.edit-lf-device',{id:'library.edit-lf-device',...this.lfState});this.features.render?.();this.features.changed?.();}this.sync();
  }
  lfProtocol(tag){return ({100:'em410x',101:'em410x',102:'em410x',103:'em410x',104:'em410x',150:'pac',170:'viking',180:'jablotron',200:'hidprox',201:'ioprox',310:'idteck'})[tag];}
  hardwareBusy(){return ['device','slots','read'].some(id=>this.get(id)?.getAttribute('aria-busy')==='true');}
  lfIdentity(){let value;try{value=JSON.parse(this.get('card-lf-identity').value);}catch(_){throw Error('LF 协议字段必须是有效 JSON');}if(!value||Array.isArray(value)||typeof value!=='object'||value.protocol!==this.lfProtocol(this.card?.tag)||value.tag_type!==this.card?.tag)throw Error('LF protocol 与 tag_type 必须匹配当前标准卡片');if(value.protocol==='ioprox'&&(!value.ioprox||typeof value.ioprox!=='object'))throw Error('ioProx 编辑需要明确 ioprox 字段并由设备校验');return value;}
  async applyLFIdentity(){
   if(!this.allowed()||!this.card||this.card.desk_read||this.dirty||this.base!==this.getText())throw Error('请选择已保存的标准 LF 卡片，先保存或放弃当前编辑');
   const identity=this.lfIdentity(),source=this.card,base=this.getText(),revision=this.revision,lfEpoch=this.lfEpoch,device=this.features.device,input=this.get('card-lf-identity').value,name=this.get('card-edit-name').value;
   if(identity.protocol==='ioprox'&&this.lfState?.state!=='available')throw Error('ioProx 固件编码能力尚未就绪');
   const value=identity.protocol==='ioprox'?await this.requestPath('/api/reader/lf-edit',{database:JSON.parse(base),id:source.id,name,identity}):await this.request({op:'edit-lf',database:JSON.parse(base),id:source.id,name,lf_identity:identity});
   if(base!==this.getText()||revision!==this.revision||source!==this.card||input!==this.get('card-lf-identity').value||identity.protocol==='ioprox'&&(lfEpoch!==this.lfEpoch||device!==this.features.device))throw Error('卡库或 LF 编辑已变化；结果未导入');
   if(value.card?.id!==source.id||value.card?.tag!==source.tag)throw Error('LF 编辑结果改变卡片标识或类型，未导入');
   this.commit(value);this.open(value.card);this.display(value.card);this.notice('LF 字段已加入卡库，将自动保存；没有写入设备或实体卡片');
  }
  async selectedRepair(preview){
   if(!this.allowed()||this.card?.desk_read?.read_result?.kind!=='classic'||this.dirty||this.base!==this.getText())throw Error('请选择已保存的 Classic 草稿，先保存或放弃当前编辑');
   const base=this.getText(),source=this.card,revision=this.revision,input=this.get('card-selected-repair').value,optionsText=this.get('card-merge-options').value;
   let repair,options;try{repair=JSON.parse(input);options=JSON.parse(optionsText);if(repair?.schema===1&&repair.result&&typeof repair.result==='object'){const envelope=repair;repair=envelope.result;if(envelope.error&& !repair.error)repair={...repair,complete:false,error:String(envelope.error)};}}catch(_){throw Error('补读结果与冲突处理选项必须是有效 JSON');}
   if(!preview&&(!this.repairPreview||this.repairPreview.base!==base||this.repairPreview.source!==source||this.repairPreview.input!==input||this.repairPreview.optionsText!==optionsText))throw Error('先预览当前补读结果');
   const value=await this.request({op:preview?'repair-classic-preview':'repair-classic',database:JSON.parse(base),id:source.id,selected_repair:repair,merge_options:options});
   if(base!==this.getText()||revision!==this.revision||source!==this.card||input!==this.get('card-selected-repair').value||optionsText!==this.get('card-merge-options').value)throw Error('卡库、补读或编辑已变化；结果未导入');
   this.get('card-repair-result').textContent=JSON.stringify(value,null,2);
   if(preview){document.dispatchEvent(new CustomEvent('desk-classic-conflicts',{detail:{source:this.card.id,value}}));this.repairPreview={base,source,input,optionsText};this.sync();return;}
   if(value.card?.id===source.id)throw Error('补读结果必须使用新草稿标识，未导入');
   this.commit(value);this.open(value.card);this.display(value.card);this.notice('补读证据已生成新草稿并加入卡库，原草稿保留');
  }
  async request(payload){return this.requestPath('/api/database',payload);}
  async requestPath(path,payload){this.pending=true;this.failure='';this.sync();try{return await this.post(path,payload);}catch(error){this.failure=error.message;this.sync();throw error;}finally{this.pending=false;this.sync();}}
  exportDraft(){
   if(!this.card)return;
   const value={schema:1,format:'chameleon-desk-editor-draft',card:this.card,name:this.get('card-edit-name').value,rows_text:this.get('card-edit-rows').value};
   const url=URL.createObjectURL(new Blob([JSON.stringify(value,null,2)],{type:'application/json'})),link=document.createElement('a');link.href=url;link.download='chameleon-desk-editor-draft.json';link.click();setTimeout(()=>URL.revokeObjectURL(url),0);
  }
  commit(value){
   if(!value?.database||Array.isArray(value.database)||!value.card?.id)throw Error('卡库结果不完整');
   this.setText(JSON.stringify(value.database,null,2));this.changed();
  }
  async save(){
   if(!this.allowed()||!this.card||!this.dirty) return;
   if(this.base!==this.getText())throw Error('卡库已变化，请保留草稿并重新选择卡片');
   const original=this.getText(),revision=this.revision,name=this.get('card-edit-name').value,text=this.get('card-edit-rows').value,rows=text===''&&!this.card.desk_read&&this.card.data.length===0?[]:text.split('\n');
   const value=await this.request({op:'edit-data',database:JSON.parse(original),id:this.card.id,name,rows});
   if(this.getText()!==original||revision!==this.revision)throw Error('编辑或卡库已变化；未替换本页内容');
   this.dirty=false;this.commit(value);this.open(value.card);this.display(value.card);this.notice('编辑已加入卡库，请核对本地保存状态');
  }
 }
 window.DeskLibraryEditor=DeskLibraryEditor;
})();
