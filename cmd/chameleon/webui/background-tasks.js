"use strict";
(() => {
 const terminal = task => ["succeeded", "failed", "canceled", "interrupted"].includes(task.state);
 class DeskBackgroundTasks {
  constructor({api, container, storage, interval=1000, onReaderResult=null, operations=[]}) {
   this.operations=new Map();this.configureOperations(operations);this.api=api;this.onReaderResult=onReaderResult;this.importing=new Set();this.container=container;try{this.storage=storage||window.sessionStorage;}catch(_){this.storage=null;}this.interval=interval;this.serverID=null;this.pending=[];this.timer=null;this.stopped=false;this.results=new Map();this.expanded=new Set();this.resultRevision=0;
   try {this.pending=JSON.parse(this.storage.getItem("desk.pending-tasks")||"[]");if(!Array.isArray(this.pending))this.pending=[];this.pending=this.pending.filter(p=>p&&typeof p.requestID==="string"&&typeof p.serverID==="string"&&typeof p.write==="boolean");}catch(_){this.pending=[];}
  }
  configureOperations(operations) {
   if(!Array.isArray(operations)||operations.some(o=>!o||typeof o.id!=="string"||typeof o.path!=="string"||typeof o.write!=="boolean"||!['GET','POST'].includes(o.method)||o.retry_policy!=="never")||new Set(operations.map(o=>o.path)).size!==operations.length)throw Error("操作清单无效；请更新 Desk 并刷新页面");
   this.operations=new Map(operations.map(o=>[o.path,o]));
  }
  operation(path){return this.operations.get(path);}
  remember() {try{this.storage.setItem("desk.pending-tasks",JSON.stringify(this.pending));}catch(_){this.storageFailure=true;}}
  async refresh() {
   const epoch=this.refreshEpoch=(this.refreshEpoch||0)+1,value=await this.api("/api/tasks");
   if(epoch!==this.refreshEpoch)return this.visibleTasks?[...this.visibleTasks.values()]:[];
   if(!value||typeof value.server_id!=="string"||!Array.isArray(value.tasks))throw new Error("任务列表无效");
   if(this.serverID && this.serverID!==value.server_id){this.results.clear();this.resultRevision++;}
   this.serverID=value.server_id;this.journalError=value.journal_error||"";this.render(value.tasks);return value.tasks;
  }
  async start(){try{await this.refresh();}catch(error){this.container.textContent="无法读取任务："+error.message;}finally{if(!this.stopped)this.timer=setTimeout(()=>this.start(),this.interval);}}
  stop(){this.stopped=true;clearTimeout(this.timer);}
  render(tasks) {
   this.visibleTasks=new Map(tasks.map(task=>[task.id,task]));
   const node=(tag,text)=>{const n=document.createElement(tag);if(text!=null)n.textContent=text;return n;};
   const renderKey=JSON.stringify([this.serverID,tasks,this.pending,this.resultRevision,this.storageFailure,this.journalError]);
   if(renderKey===this.renderKey)return;this.renderKey=renderKey;
   for(const id of this.results.keys())if(!tasks.some(t=>t.id===id))this.results.delete(id);
   this.container.replaceChildren();
   if(this.journalError)this.container.append(node("p","任务记录保存失败，已阻止新任务。请保留记录并核对设备状态。"));
   if(this.storageFailure)this.container.append(node("p","无法保存本页任务收据；刷新后仍可查询服务端任务列表。"));
   for(const receipt of this.pending) {
    const found=tasks.find(t=>t.request_id===receipt.requestID);
    if(!found){
     const warning=node("p",`提交 ${receipt.requestID}：服务已重启、回执丢失或结果未保留；${receipt.write?"写入结果无法确认":"执行结果无法确认"}，不会自动重新提交。`);
     const dismiss=node("button","我已记录，移除此页提醒");dismiss.onclick=()=>{this.pending=this.pending.filter(p=>p.requestID!==receipt.requestID);this.remember();this.render(tasks);};warning.append(dismiss);this.container.append(warning);
    }
   }
   const labels={queued:"排队中",running:"执行中",succeeded:"已完成",failed:"失败",canceled:"执行前取消",interrupted:"执行中断"};
   for(const task of tasks) {
    const row=node("article");row.dataset.taskId=task.id;row.dataset.state=task.state;
    row.append(node("strong",`${labels[task.state]||task.state} · ${task.path}`));
    row.append(node("p",`任务 ${task.id} · 会话 ${task.session_id} · 请求尝试 ${task.commands_attempted} / 收到响应 ${task.responses_received}`));
    if(task.recovered)row.append(node("p","此任务从持久记录恢复，仅恢复状态；没有恢复执行或重新发送命令。"));
    if(task.result_retained===false&&terminal(task))row.append(node("p","结果正文未保留：持久记录不保存密钥和卡片内容，请查看此前导出的结果；不要因此重复写入。"));
    if(task.cancel_requested)row.append(node("p","已请求取消；等待执行结束，取消不代表撤销已执行命令。"));
    if(task.outcome==="write_not_started")row.append(node("p","写入尚未开始；验证或预检未通过，没有调用修改接口。"));
    if(task.outcome==="write_outcome_unknown")row.append(node("p","写入结果未知：部分命令可能已经生效。请读取设备状态核对，不要直接重复写入。"));
    if(task.outcome==="read_interrupted")row.append(node("p",this.operation(task.path)?.session_required===false?"离线分析已中断；请保留原文件，任务不会自动重放。":"读取已中断；设备模式或临时状态可能需要核对。"));
    if(!terminal(task)){const cancel=node("button","取消任务");cancel.onclick=async()=>{cancel.disabled=true;try{await this.api(`/api/tasks/${task.id}/cancel`,{method:"POST",body:"{}"});await this.refresh();}catch(e){row.append(node("p",e.message));cancel.disabled=false;}};row.append(cancel);}
    const result=task.result||this.results.get(task.id);
    if(result){const details=node("details"),pre=node("pre",JSON.stringify(result,null,2));details.open=this.expanded.has(task.id);details.ontoggle=()=>{if(details.open)this.expanded.add(task.id);else this.expanded.delete(task.id);};details.append(node("summary","查看结果与已确认阶段"),pre);row.append(details);}
    const readValue=result?.schema===1?result:result?.result;
    if(this.onReaderResult&&terminal(task)&&!task.write&&(task.path.startsWith('/api/reader/')||task.path==='/api/slot/read-current')&&readValue?.schema===1&&['classic','type2','lf','slot'].includes(readValue.kind)&&readValue.units_read>0&&!readValue.slot_snapshot?.source_changed){
     const button=node('button','导入读取结果到卡库'),server=this.serverID,source=result,key=server+':'+task.id;button.disabled=this.importing.has(key);
     button.onclick=async()=>{if(this.importing.has(key))return;this.importing.add(key);button.disabled=true;try{await this.onReaderResult({value:readValue,error:result?.error||''},()=>this.serverID===server&&this.visibleTasks.get(task.id)?.session_id===task.session_id&&terminal(this.visibleTasks.get(task.id))&&(this.results.get(task.id)===source||this.visibleTasks.get(task.id)?.result===source));}catch(error){row.append(node('p',error.message));}finally{this.importing.delete(key);button.disabled=false;}};
     row.append(button);
    }

    if(!result&&terminal(task)&&task.result_retained!==false){const query=node("button","查询任务结果");query.onclick=async()=>{query.disabled=true;try{const server=this.serverID,value=await this.api(`/api/tasks/${task.id}`);if(this.serverID!==server||this.visibleTasks.get(task.id)?.session_id!==task.session_id)throw Error("任务来源已变化；旧结果未导入");this.results.set(task.id,value.result);this.expanded.add(task.id);this.resultRevision++;await this.refresh();}catch(error){row.append(node("p",error.message));query.disabled=false;}};row.append(query);}
    this.container.append(row);
    if(terminal(task)&&task.outcome!=="write_outcome_unknown")this.pending=this.pending.filter(p=>p.requestID!==task.request_id);
   }
   this.remember();
  }
  async execute(path,opts={}) {
   const operation=this.operation(path);
   if(!operation)throw new Error("此操作未登记，任务未提交");
   // Parse before keeping a receipt: invalid local input was never submitted.
   const body=opts.body!=null?JSON.parse(opts.body):undefined;
   if(!this.serverID)await this.refresh();
   const sessionID=operation.session_required===false?"offline":opts.headers?.["X-Chameleon-Session"];
   if(!sessionID)throw new Error("请先刷新设备会话");
   const receipt={serverID:this.serverID,requestID:window.crypto.randomUUID(),write:operation.write};
   this.pending.push(receipt);this.remember();
   const input={request_id:receipt.requestID,session_id:sessionID,operation_id:operation.id,path};if(body!==undefined)input.body=body;
   // Exactly one submission. Network ambiguity is resolved by querying, never
   // by resending a mutation, including after a page or server restart.
   let task;
   try{task=await this.api("/api/tasks",{method:"POST",body:JSON.stringify(input)});}catch(error){
    if([400,403,404,405,409,410,413,429].includes(error.status)){this.pending=this.pending.filter(p=>p.requestID!==receipt.requestID);this.remember();error.message+="；任务未提交，不会自动重试。";}
    else error.message+="；请在后台任务中查询，不会自动重新提交。";
    throw error;
   }
   while(!terminal(task)) {await new Promise(resolve=>setTimeout(resolve,this.interval));task=await this.api(`/api/tasks/${task.id}`);}
   await this.refresh();
   if(task.state!=="succeeded") {const error=new Error((task.result?.error||"任务未完成")+(task.outcome==="write_outcome_unknown"?"；写入结果未知，请核对设备状态":""));error.result=task.result?.result;error.task=task;throw error;}
   return task.result;
  }
 }
 window.DeskBackgroundTasks=DeskBackgroundTasks;
})();
