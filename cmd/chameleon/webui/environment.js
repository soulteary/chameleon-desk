/* Environment checks inspect the Desk host and management status, never RF. */
(() => {
  "use strict";
  class DeskEnvironment {
    constructor({post, api, features, run, busy=()=>false}) {
      Object.assign(this,{post,api,features,run,busy});
      this.button=document.querySelector('#environment-check');
      this.status=document.querySelector('#environment-status');
      this.list=document.querySelector('#environment-results');
      this.raw=document.querySelector('#environment-json');
      this.exportButton=document.querySelector('#environment-export');
      this.epoch=0;this.pending=false;this.result=null;
      this.button.onclick=()=>run('hardware',()=>this.check());
      this.exportButton.onclick=()=>this.export();
      document.addEventListener('desk-feature-device',()=>{
        this.epoch++;this.result=null;this.list.replaceChildren();this.raw.textContent='';
        this.status.textContent='连接事实已变化，请重新诊断或从任务中心查看既有结果';this.sync();
      });
      this.sync();
    }
    sync() {
      const state=this.features?.state('environment.diagnostics');
      this.button.disabled=this.pending||this.busy()||state?.state!=='available';
      this.button.title=state?.state==='available'?'':(state?.reasons||[]).map(r=>r.message).join('；')||'请先读取功能清单';
      this.exportButton.disabled=this.pending||!this.result;
    }
    async check() {
      const epoch=++this.epoch;this.pending=true;this.result=null;this.list.replaceChildren();this.raw.textContent='';
      this.status.textContent='正在检查 Desk 主机；不会执行恢复程序、扫描或连接设备';this.sync();
      try {
        const value=await this.api('/api/environment');
        if(epoch!==this.epoch)return;
        if(value?.schema_version!==1||!Array.isArray(value.recovery?.workers)||!Array.isArray(value.recovery?.checks)||!Array.isArray(value.checks))throw Error('环境诊断结果格式不受支持');
        this.result=value;this.raw.textContent=JSON.stringify(value,null,2);
        const rows=[...value.checks,...value.recovery.checks,...(value.bridge?.checks||[])];
        const labels={pass:'通过',fail:'失败',unknown:'未验证'};
        let failed=false;
        const add=(state,title,message)=>{const li=document.createElement('li');li.textContent=(labels[state]||'未验证')+' · '+title+(message?'：'+message:'');this.list.append(li);if(state==='fail')failed=true;};
        for(const row of rows)add(row.status,row.code,row.message);
        for(const worker of value.recovery.workers)add(worker.state==='available'?(worker.code==='worker_integrity_match'?'pass':'unknown'):'fail',worker.name,worker.code);
        this.status.textContent=(failed?'发现环境缺项；请按诊断代码处理后重查。':'本次文件与管理接口检查完成，未验证项仍需单独检查。')+' 主机：'+value.os+'/'+value.arch+'。诊断不代表恢复成功或硬件可用。';
      } catch(error) {
        if(epoch===this.epoch){this.status.textContent=error.message;throw error;}
      } finally {this.pending=false;this.sync();}
    }
    export() {
      if(!this.result)return;
      const url=URL.createObjectURL(new Blob([JSON.stringify(this.result,null,2)],{type:'application/json'}));
      const link=document.createElement('a');link.href=url;link.download='desk-environment.json';link.click();
      setTimeout(()=>URL.revokeObjectURL(url),0);
    }
  }
  window.DeskEnvironment=DeskEnvironment;
})();
