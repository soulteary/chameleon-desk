"use strict";
(() => {
 const kinds={classic:{id:'reader.classic-keyed',field:'classic_read'},type2:{id:'reader.type2-range',field:'type2_read'},lf:{id:'reader.lf',field:'lf_read'}};
 class DeskReader {
  constructor({post,features,run,busy=()=>false,importRead=null,libraryBusy=()=>false}){
   Object.assign(this,{post,features,run,busy,importRead,libraryBusy});this.epochs={};this.previews={};this.results=new Map();
   this.get=id=>document.getElementById(id);
   for(const kind of Object.keys(kinds)){
    this.epochs[kind]=0;this.previews[kind]=0;
    this.get(`reader-${kind}-form`).addEventListener('input',()=>{this.invalidate(kind);this.preview(kind);});
    this.get(`reader-${kind}-form`).addEventListener('change',event=>{if(event.target.tagName==='SELECT'){this.invalidate(kind);this.preview(kind);}});
    this.get(`reader-${kind}-go`).onclick=()=>run('hardware',()=>this.read(kind));
    this.get(`reader-${kind}-export`).onclick=()=>this.export(kind);
    this.get(`reader-${kind}-import`).onclick=()=>run("database",async()=>{const result=this.results.get(kind);if(result&&this.importRead)await this.importRead(result,()=>this.results.get(kind)===result);});
   }
   this.get('reader-classic-template').onclick=()=>{
    const sectors=Number(this.get('reader-classic-size').value);
    this.get('reader-classic-json').value=JSON.stringify({uid:this.get('reader-classic-uid').value.trim(),sectors:Array.from({length:sectors},(_,sector)=>({sector,a:'',b:''}))},null,2);
    this.invalidate('classic');this.preview('classic');
   };
   document.addEventListener('desk-feature-device',()=>{this.clear();for(const kind of Object.keys(kinds))this.preview(kind);});
   for(const kind of Object.keys(kinds))this.preview(kind);
  }
  invalidate(kind){this.epochs[kind]++;this.previews[kind]++;this.results.delete(kind);this.get(`reader-${kind}-result`).replaceChildren();this.get(`reader-${kind}-export`).disabled=true;this.sync();}
  sync(){for(const kind of Object.keys(kinds)){
   const result=this.results.get(kind),reason=!result?'请先完成读取':result.value.units_read<1?'没有已确认的数据，无法导入':kind==='lf'&&(!result.value.complete||!result.value.present)?'LF 导入要求成功读取且发现卡片':!this.importRead?'卡库入口未就绪':this.features.state('library.database').state!=='available'?'卡库能力尚未就绪':this.libraryBusy()?'卡库正在处理或恢复保存版本':'';
   this.get(`reader-${kind}-export`).disabled=this.busy()||!result;
   this.get(`reader-${kind}-import`).disabled=this.busy()||!!reason;
   this.get(`reader-${kind}-import-reason`).textContent=reason;
  }}
  clear(){for(const kind of Object.keys(kinds))this.invalidate(kind);for(const id of ['reader-classic-json','reader-classic-uid','reader-type2-uid','reader-lf-password'])this.get(id).value='';}
  input(kind){
   if(kind==='classic'){
    let value;try{value=JSON.parse(this.get('reader-classic-json').value);}catch(_){throw Error('请输入有效的分扇区密钥 JSON');}
    if(!value||typeof value!=='object'||Array.isArray(value))throw Error('密钥输入必须为 JSON 对象');return value;
   }
   const integer=(id)=>{const value=this.get(id).value;if(!/^\d+$/.test(value)||!Number.isSafeInteger(Number(value)))throw Error('请输入非负整数');return Number(value);};
   if(kind==='type2'){const uid=this.get('reader-type2-uid').value.trim();if(!/^(?:[0-9a-fA-F]{8}|[0-9a-fA-F]{14}|[0-9a-fA-F]{20})$/.test(uid))throw Error('请输入四、七或十字节 UID');return {uid,start:integer('reader-type2-start'),count:integer('reader-type2-count')};}
   const protocol=this.get('reader-lf-protocol').value,value={protocol};
   if(protocol==='hidprox')value.format=integer('reader-lf-format');
   if(protocol==='em4x05'){
    const password=this.get('reader-lf-password').value.trim();if(password&&!/^[0-9a-fA-F]{8}$/.test(password))throw Error('EM4x05 密码应为八位十六进制数');
    value.password=password?parseInt(password,16):0;
   }
   return value;
  }
  publish(kind,state){const id=kinds[kind].id;this.features.states.set(id,{id,...state});this.features.render();this.features.changed();}
  async preview(kind){
   const revision=++this.previews[kind];
   if(kind==='lf'){this.get('reader-lf-format-label').hidden=this.get('reader-lf-protocol').value!=='hidprox';this.get('reader-lf-password-label').hidden=this.get('reader-lf-protocol').value!=='em4x05';if(this.get('reader-lf-protocol').value!=='em4x05')this.get('reader-lf-password').value='';}
   if(!this.features.device)return;
   const state=this.features.state(kinds[kind].id);
   if(state.state==='unavailable'&&state.reasons?.some(r=>['unsupported_model','model_unknown','usb_bridge_required','transport_restricted'].includes(r.code)))return;
   let value;try{value=this.input(kind);}catch(error){this.publish(kind,{state:'unavailable',reasons:[{code:'invalid_parameters',message:error.message}]});return;}
   this.publish(kind,{state:'unknown',reasons:[{message:'正在检查读取参数'}]});
   try{
    const result=await this.post('/api/features/check',{id:kinds[kind].id,device:this.features.device,[kinds[kind].field]:value});
    if(revision!==this.previews[kind])return;
    if(result.schema_version!==1||result.state?.id!==kinds[kind].id||!['available','unavailable','unknown'].includes(result.state.state)||!Array.isArray(result.state.reasons)||result.state.reasons.some(r=>!r||typeof r.message!=='string'))throw Error('功能检查结果无效');
    this.publish(kind,result.state);
   }catch(error){if(revision===this.previews[kind])this.publish(kind,{state:'unknown',reasons:[{message:error.message}]});}
  }
  show(kind,value,error){
   const out=this.get(`reader-${kind}-result`),node=(tag,text)=>{const n=document.createElement(tag);n.textContent=text;return n;};out.replaceChildren();
   if(error)out.append(node('p','读取未完成：'+error));
   let valid=value?.schema===1&&value.kind===kind&&typeof value.complete==='boolean'&&Number.isInteger(value.requested_units)&&value.requested_units>=1&&value.requested_units<=256&&Number.isInteger(value.units_read)&&value.units_read>=0&&value.units_read<=value.requested_units;
   if(valid&&kind!=='lf'){const bytes=kind==='classic'?value.dump?.data:(value.data||'');valid=typeof bytes==='string'&&/^(?:[0-9a-fA-F]{2})*$/.test(bytes)&&bytes.length===value.units_read*(kind==='classic'?32:8)&&(!value.complete||value.units_read===value.requested_units);}
   if(valid&&kind==='lf'&&value.complete)valid=typeof value.present==='boolean'&&value.units_read===(value.present?1:0);
   if(!valid){out.append(node('p','读取结果格式无效，无法导出'));return;}
   this.results.set(kind,{value,error});this.get(`reader-${kind}-export`).disabled=false;this.sync();
   if(kind==='lf'){out.append(node('p',value.present===false?'未发现卡片':value.complete?'LF 读取完成':'LF 读取未完成'));}
   else{
    const unit=kind==='classic'?'块':'页';out.append(node('p',`${value.complete?'读取完成':'部分 / 中断结果'}：${value.units_read} / ${value.requested_units} ${unit}`));
    const unknown=value.dump?.unknown_keys?.length||0;if(unknown)out.append(node('p',`仍有 ${unknown} 个未知密钥；这些尾块不能视为完整密钥数据。`));
    const bytes=kind==='classic'?value.dump?.data:value.data;
    if(typeof bytes==='string'&&/^(?:[0-9a-fA-F]{2})*$/.test(bytes)){
     const width=kind==='classic'?32:8,lines=[];
     for(let offset=0;offset<bytes.length;offset+=width)lines.push(`${(value.start||0)+offset/width}: ${bytes.slice(offset,offset+width).match(/../g)?.join(' ')||''}`);
     out.append(node('pre',lines.join('\n')));
    }
   }
   const details=document.createElement('details');details.append(node('summary','原始读取结果'),node('pre',JSON.stringify(value,null,2)));out.append(details);
  }
  async read(kind){
   if(this.features.state(kinds[kind].id).state!=='available')return;
   const epoch=++this.epochs[kind];this.results.delete(kind);this.get(`reader-${kind}-export`).disabled=true;
   try{
    const value=this.input(kind);this.get(`reader-${kind}-result`).textContent='正在读取；可在后台任务中查看进度或取消。';
    const pending=this.post('/api/reader/'+kind,value);
    if(kind==='classic')this.get('reader-classic-json').value='';
    if(kind==='lf')this.get('reader-lf-password').value='';
    const result=await pending;if(epoch===this.epochs[kind])this.show(kind,result);
   }catch(error){
    if(!error.staleSession&&epoch===this.epochs[kind]){this.features.clear?.('读取中断，请核对设备状态并刷新能力');this.show(kind,error.result,error.message);}
   }finally{this.preview(kind);}
  }
  export(kind){
   const result=this.results.get(kind);if(!result)return;
   const blob=new Blob([JSON.stringify({schema:1,read_result:result.value,error:result.error||null},null,2)],{type:'application/json'}),url=URL.createObjectURL(blob),link=document.createElement('a');
   link.href=url;link.download=`${kind}-read-${result.value.complete?'complete':'partial'}.json`;link.click();setTimeout(()=>URL.revokeObjectURL(url),0);
  }
 }
 window.DeskReader=DeskReader;
})();
