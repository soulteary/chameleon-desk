"use strict";
(() => {
 class DeskLibraryEditor {
  constructor({post,run,getText,setText,changed,busy,features,notice,display=()=>{}}) {
   Object.assign(this,{post,run,getText,setText,changed,busy,features,notice,display});this.get=id=>document.getElementById(id);this.dirty=false;this.card=null;this.revision=0;
   for(const id of ['card-edit-name','card-edit-rows'])this.get(id).addEventListener('input',()=>{this.dirty=true;this.failure="";this.revision++;this.sync();});
   this.get('card-edit-export').onclick=()=>this.exportDraft();
   this.get('card-edit-save').onclick=()=>run('database',()=>this.save());
   this.get('card-edit-discard').onclick=()=>{if(this.dirty&&!window.confirm('放弃尚未保存到卡库的编辑？'))return;this.clear();};
   this.get('read-import-go').onclick=()=>run('database',async()=>{
    const envelope=JSON.parse(this.get('read-import-json').value);
    if(envelope.schema!==1||!envelope.read_result)throw Error('请选择 Desk 读取结果 JSON');
    const original=this.get('read-import-json').value;
    await this.importRead({value:envelope.read_result,error:envelope.error},()=>this.get('read-import-json').value===original);
   });
   this.sync();
  }
  allowed(){return this.features.state('library.database').state==='available';}
  sync(){
   const changed=this.card&&this.base!==this.getText(),blocked=this.busy()||!this.allowed();
   this.get('card-edit-save').disabled=blocked||!this.card||!this.dirty||changed;
   this.get('card-edit-discard').disabled=this.busy()||!this.card;
   this.get('card-edit-export').disabled=this.busy()||!this.card;
   for(const id of ['card-edit-name','card-edit-rows'])this.get(id).disabled=this.busy()||!this.card;
   this.get('read-import-go').disabled=blocked;
   this.get('card-edit-state').setAttribute('role',this.failure?'alert':'status');
   this.get('card-edit-state').textContent=this.failure?'未保存：'+this.failure:changed?'卡库已变化；编辑草稿仍保留，请导出草稿或放弃后重新选择卡片':this.dirty?'编辑尚未保存到卡库；关闭或刷新页面前请保存':this.card?'编辑已加入卡库；本地持久化状态见上方':'请在卡库中查看一张卡片后编辑';
   this.get('read-import-reason').textContent=!this.allowed()?'卡库能力尚未就绪，请重试读取功能清单':this.busy()?'卡库正在处理或读取保存版本':'';
  }
  clear(){this.failure="";this.card=null;this.dirty=false;this.revision++;this.get('card-edit-name').value='';this.get('card-edit-rows').value='';this.get('card-edit-limit').textContent='';this.sync();}
  open(card){
   if(this.dirty&&!window.confirm('选择卡片将替换尚未保存的编辑，确认继续？'))return;
   const draft=card.desk_read;
   let rows;
   if(draft){if(draft.schema!==1||!Array.isArray(draft.rows)||!draft.rows.every(v=>typeof v==='string'))throw Error('读取草稿格式无效');rows=draft.rows;}
   else if([1000,1001,1002,1003,1100,1101,1102,1103,1104,1105,1106,1107,1108].includes(card.tag)&&Array.isArray(card.data)&&card.data.length<=256)rows=card.data.map(row=>row.map(v=>v.toString(16).padStart(2,'0')).join(''));
   else {this.clear();this.get('card-edit-limit').textContent='此卡片类型没有受支持的数据编辑入口';return;}
   this.failure="";this.card=card;this.base=this.getText();this.dirty=false;this.revision++;
   this.get('card-edit-name').value=card.name;
   this.get('card-edit-rows').value=rows.map(row=>row.match(/.{2}/g)?.join(' ')||'').join('\n');
   const r=draft?.read_result;
   this.get('card-edit-limit').textContent=draft?`Desk 读取草稿 · ${r.kind} · ${r.complete?'请求范围读取完成':'部分 / 中断读取'} · ${r.units_read} / ${r.requested_units} 单元。行号从 0 开始；?? 为未知字节，不能补值。原始读取结果保留。尚无完整身份与容量确认，禁止槽位加载、NDEF 与二进制 / 镜像转换；JSON 导出保留标记。`:'每行对应一个块或页，从 0 开始；保持行数与宽度，未知空行必须保留。只修改文件，不写实体卡片。';
   this.sync();
  }
  async importRead(result,current=()=>true){
   if(!this.allowed())throw Error('卡库能力尚未就绪');
   if(this.dirty)throw Error('请先保存或放弃当前卡片编辑');
   const original=this.getText(),value=await this.request({op:'import-read',database:JSON.parse(original),read_result:result.value,read_error:result.error||'',name:`${result.value.kind} read ${result.value.uid||result.value.record?.protocol||''}`});
   if(!current()||this.getText()!==original||this.dirty)throw Error('读取结果或卡库已变化；未导入，请重新操作');
   this.commit(value);this.open(value.card);this.display(value.card);this.notice('读取结果已加入卡库，将自动保存；可在卡片库中编辑');
  }
  async request(payload){this.pending=true;this.failure='';try{return await this.post('/api/database',payload);}catch(error){this.failure=error.message;this.sync();throw error;}finally{this.pending=false;}}
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
