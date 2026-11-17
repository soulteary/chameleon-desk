/* File-only RF workbench. No acquisition or card-library handoff is exposed. */
(() => {
  'use strict';
  const $=id=>document.getElementById(id);
  const node=(tag,text)=>{const n=document.createElement(tag);if(text!=null)n.textContent=text;return n;};
  const download=(value,type,name)=>{const url=URL.createObjectURL(new Blob([value],{type})),a=node('a');a.href=url;a.download=name;a.click();setTimeout(()=>URL.revokeObjectURL(url),1000);};
  class DeskRFFiles {
    constructor({post,api,features,run,busy=()=>false}) {
      Object.assign(this,{post,api,features,run,busy});this.root=$('rf-files');this.rev=0;this.pending=false;this.result=null;this.bytes=null;this.hex=null;this.page=0;this.dirty=false;
      this.fields=['rf-format','rf-layout','rf-short-markers','rf-rate','rf-start','rf-count','rf-decode','rf-clock','rf-phase','rf-invert'];
      for(const id of this.fields)$(id).addEventListener('input',()=>{if(id==='rf-format')$('rf-short-markers').checked=false;this.invalidate();this.sync();});
      $('rf-file').addEventListener('change',()=>this.loadFile().catch(e=>{this.status(e.message);this.sync();}));
      $('rf-analyze').onclick=()=>run('analysis',()=>this.analyze());
      $('rf-csv').onclick=()=>run('analysis',()=>this.analyze('csv'));
      $('rf-svg').onclick=()=>run('analysis',()=>this.analyze('svg'));
      $('rf-report').onclick=()=>{if(this.result&&!this.dirty)download(JSON.stringify(this.result,null,2),'application/json','desk-rf-analysis.json');};
      $('rf-original').onclick=()=>{if(this.bytes)download(this.bytes,'application/octet-stream',this.fileName||'rf-source.bin');};
      $('rf-query').onclick=()=>run('analysis',()=>this.query());
      for(const id of ['rf-direction','rf-search'])$(id).oninput=()=>{this.page=0;this.table();};
      $('rf-prev').onclick=()=>{this.page=Math.max(0,this.page-1);this.table();};$('rf-next').onclick=()=>{this.page++;this.table();};
      document.addEventListener('desk-feature-device',()=>this.sync());this.sync();
    }
    status(text){$('rf-status').textContent=text;}
    invalidate(clear=false){this.rev++;this.dirty=true;if(clear){this.result=null;this.page=0;$('rf-view').hidden=true;}if(this.result)this.status('参数已变化；当前结果仅供查看，请重新分析后导出报告。');this.sync();}
    sync(){
      const format=$('rf-format').value,lf=format.startsWith('lf-'),state=this.features?.state('files.rf-analysis'),blocked=this.pending||this.busy();
      $('rf-lf-options').hidden=!lf;$('rf-hf-options').hidden=lf;$('rf-layout').disabled=blocked||format==='pm3-trace';$('rf-short-markers').disabled=blocked||format!=='pm3-trace';
      $('rf-analyze').disabled=blocked||!this.hex||state?.state!=='available';
      $('rf-reason').textContent=state?.state==='available'?(!this.hex?'请先选择并读取原文件。':''):(state?.reasons||[]).map(r=>r.message).join('；')||'请先读取功能清单。';
      const available=this.result&&!this.dirty;
      $('rf-report').disabled=blocked||!available;$('rf-original').disabled=!this.bytes;
      const current=available&&this.resultRev===this.rev&&!!this.hex&&this.result?.lf;
      $('rf-csv').disabled=blocked||!current||!this.result.lf.rate_hz||this.result.lf.data.length/2>65536;$('rf-svg').disabled=blocked||!current;
      $('rf-export-limit').textContent=current?'CSV 仅支持不超过 65536 个样本且有声明/指定采样率的选区；索引和推算时间从选区零点开始。':'查询结果可导出报告；CSV/SVG 需要当前原文件和已分析选区。';
      $('rf-query').disabled=blocked;
      for(const id of this.fields)if(!['rf-layout','rf-short-markers'].includes(id))$(id).disabled=blocked;
    }
    async loadFile(){
      this.invalidate(true);const epoch=this.fileEpoch=(this.fileEpoch||0)+1;$('rf-short-markers').checked=false;this.bytes=null;this.hex=null;this.fileName=null;this.sync();
      const file=$('rf-file').files?.[0];if(!file){this.status('尚未选择文件。');return;}
      if(file.size<1||file.size>1048576)throw Error('文件必须为 1..1048576 字节；不会自动截断。');
      this.status('正在读取原文件…');const buffer=await file.arrayBuffer();if(epoch!==this.fileEpoch)return;
      const bytes=new Uint8Array(buffer);let parts=[];
      for(let start=0;start<bytes.length;start+=65536){parts.push(Array.from(bytes.subarray(start,start+65536),b=>b.toString(16).padStart(2,'0')).join(''));if(start+65536<bytes.length)await new Promise(r=>setTimeout(r,0));if(epoch!==this.fileEpoch)return;}
      this.bytes=bytes;this.hex=parts.join('');this.fileName=file.name;this.status('原文件已读取：'+file.name+' · '+bytes.length+' 字节。请选择明确格式与解析参数。');this.sync();
    }
    input(output=''){
      const format=$('rf-format').value,lf=format.startsWith('lf-');
      const integer=(id)=>{const v=Number($(id).value);if(!Number.isSafeInteger(v)||v<0)throw Error('范围、时钟和采样率必须为非负整数。');return v;};
      return {format,name:this.fileName||'',data:this.hex,layout:lf||format==='pm3-trace'?'':$('rf-layout').value,short_markers:format==='pm3-trace'&&$('rf-short-markers').checked,rate_hz:lf?integer('rf-rate'):0,start:integer('rf-start'),count:integer('rf-count'),decode:lf&&$('rf-decode').checked,clock:integer('rf-clock'),phase:integer('rf-phase'),invert:lf&&$('rf-invert').checked,export:output};
    }
    async analyze(output=''){
      if(!this.hex)throw Error('请先选择原文件。');const rev=this.rev,input=this.input(output);this.pending=true;this.dirty=true;this.status('离线任务正在分析；可在任务中心查询或请求取消。');this.sync();
      try{const value=await this.post('/api/files/rf/analyze',input);if(rev!==this.rev)return;if(value?.source?.name!==input.name||value?.source?.format!==input.format||value?.source?.bytes!==this.bytes?.length)throw Error('分析来源与当前文件不一致。');this.render(value);this.resultRev=rev;this.dirty=false;if(output&&value.export)download(value.export.text,value.export.media_type,'desk-lf-selection.'+output);}
      catch(error){if(rev===this.rev){this.dirty=true;this.status(error.message);throw error;}}
      finally{this.pending=false;this.sync();}
    }
    async query(){
      const id=$('rf-task-id').value.trim();if(!/^[0-9a-f]{32}$/.test(id))throw Error('请输入完整的 32 位任务 ID。');const rev=++this.rev;this.fileEpoch=(this.fileEpoch||0)+1;this.pending=true;this.dirty=true;this.status("正在查询既有离线任务；不会重新提交原文件。");this.sync();
      try{const task=await this.api('/api/tasks/'+id);if(rev!==this.rev)return;if(task.operation_id!=='files.rf-analyze'||task.state!=='succeeded'||task.result_retained===false||!task.result)throw Error('没有可用的离线分析正文；请保留原文件，不会自动重新提交。');this.render(task.result);this.bytes=null;this.hex=null;this.fileName=null;$('rf-file').value='';this.resultRev=null;this.dirty=false;this.status('已查询既有任务。原文件只保留在选择它的页面内存中；此处可查看和导出报告，不会重跑分析。');}
      catch(error){if(rev===this.rev){this.status(error.message);throw error;}}
      finally{this.pending=false;this.sync();}
    }
    render(value){
      if(value?.schema_version!==1||value?.kind!=='offline_rf'||!value.source||!Array.isArray(value.notes)||!Array.isArray(value.issues)||(!value.lf&&!value.hf))throw Error('离线分析结果格式不受支持。');
      if(value.lf&&value.hf)throw Error('分析结果类型不明确。');
      const hex=v=>typeof v==='string'&&v.length%2===0&&/^[0-9a-f]*$/i.test(v);
      if(value.lf&&(!hex(value.lf.data)||!value.lf.data.length||value.lf.data.length>524288||!Array.isArray(value.lf.buckets)||value.lf.buckets.length>1024))throw Error('样本结果超限或无效。');
      if(value.hf&&(!Array.isArray(value.hf.frames)||value.hf.frames.length>4096||!Array.isArray(value.hf.auth_candidates)||value.hf.frames.some(f=>!hex(f.data)||f.data.length>8192||typeof f.from_card!=='boolean')))throw Error('帧结果超限或无效。');
      this.result=value;this.page=0;$('rf-view').hidden=false;
      $('rf-source').textContent=value.source.name+' · '+value.source.format+' · '+value.source.bytes+' 字节 · SHA-256 '+value.source.sha256;
      $('rf-parameters').textContent=JSON.stringify(value.parameters||{},null,2);
      const notes=$('rf-notes');notes.replaceChildren();for(const note of value.notes)notes.append(node('li',note));for(const issue of value.issues)notes.append(node('li',issue.code+' · '+issue.unit+' '+issue.offset+'：'+issue.message));
      this.status(value.complete?'已解析所选文件；这不代表完整 RF 交换或真实硬件验收。':'仅解析了确认的前缀；后续数据未知，请查看失败位置。');
      $('rf-summary').textContent=value.lf?('LF · 总样本 '+value.lf.total+' · 选区起点 '+value.lf.start+' · 选中 '+value.lf.data.length/2+' · 采样率 '+(value.lf.rate_hz||'未知')+' ('+value.lf.rate_source+') · '+value.lf.analysis.heuristic_hint+(value.lf.manchester?' · Manchester 位数 '+value.lf.manchester.bits.length+'，时序错误 '+value.lf.manchester.timing_errors+'，无效对 '+value.lf.manchester.invalid_pairs:'')):('HF · 已解析 '+value.hf.parsed+' 帧 · 当前选区读卡器 '+value.hf.reader_frames+' / 卡片 '+value.hf.card_frames+' · 时间：'+value.hf.timing);
      $('rf-stats').textContent=value.lf?('最小 / 最大 '+value.lf.analysis.min+' / '+value.lf.analysis.max+' · 均值 '+value.lf.analysis.mean+' · 阈值 '+value.lf.analysis.threshold+' · 转换 '+value.lf.analysis.transitions+(value.lf.rf_heuristic_available?' · 半周期样本估计 '+value.lf.analysis.half_period_samples+' · RF divider 估计 '+value.lf.analysis.estimated_rf_divider:' · RF divider 估计未启用')):'';
      $('rf-bits-view').hidden=!value.lf?.manchester;$('rf-bits').textContent='';$('rf-bits-view').open=false;$('rf-bits-view').ontoggle=()=>{if($('rf-bits-view').open&&value===this.result)$('rf-bits').textContent=value.lf?.manchester?.bits||'';};
      $('rf-hf-filter').hidden=!value.hf;$('rf-auth').replaceChildren();if(value.hf){$('rf-auth').append(node('p','候选认证片段只按相邻方向、长度和请求后缀匹配；不验证 UID、密钥、明文/加密状态，也不生成恢复输入。'));for(const c of value.hf.auth_candidates)$('rf-auth').append(node('p','请求帧 '+c.request_frame+' → nonce 候选帧 '+c.nonce_frame+' → 读卡器回答 '+(c.reader_answer_frame??'未知')+' → 卡片回答 '+(c.card_answer_frame??'未知')+'；块 '+c.block+'，Key '+c.key_type));}
      this.plot();this.table();
    }
    plot(){
      const target=$('rf-waveform');target.replaceChildren();const lf=this.result?.lf;if(!lf)return;
      const svg=document.createElementNS('http://www.w3.org/2000/svg','svg');svg.setAttribute('viewBox','0 0 1040 300');svg.setAttribute('role','img');svg.setAttribute('aria-label','LF 选区的无符号 8 位样本最小/最大包络；横轴为样本索引');
      const buckets=lf.buckets||[];if(buckets.length>1024)throw Error('波形桶数量超限。');const points=[];for(let i=0;i<buckets.length;i++){const b=buckets[i];if(![b.min,b.max].every(v=>Number.isInteger(v)&&v>=0&&v<=255))throw Error('波形值无效。');const x=40+i*960/Math.max(1,buckets.length-1);points.push(x+','+(270-b.min),x+','+(270-b.max));}
      const line=document.createElementNS(svg.namespaceURI,'polyline');line.setAttribute('points',points.join(' '));line.setAttribute('fill','none');line.setAttribute('stroke','#176caa');line.setAttribute('stroke-width','1');svg.append(line);
      for(const [x,text]of [[40,String(lf.start)],[900,String(lf.start+lf.data.length/2-1)]]){const label=document.createElementNS(svg.namespaceURI,'text');label.setAttribute('x',String(x));label.setAttribute('y','292');label.textContent=text;svg.append(label);}target.append(svg);target.append(node('p','包络最多 1024 桶，保留每桶最小/最大值；这不是逐点波形或校准电压。改变起点/数量后可重新分析选区。'));
    }
    table(){
      const value=this.result;if(!value)return;const head=$('rf-table-head'),body=$('rf-table-body');head.replaceChildren();body.replaceChildren();let rows=[],total=0;
      if(value.lf){const l=value.lf;total=l.data.length/2;head.append(...['样本索引','推算时间 µs（文件起点）','ADC 值'].map(t=>node('th',t)));}
      else{const direction=$('rf-direction').value,search=$('rf-search').value.replace(/\s/g,'').toUpperCase();rows=value.hf.frames.filter(f=>(direction==='all'||(direction==='card')===f.from_card)&&f.data.includes(search));head.append(...['帧 / 来源偏移','方向 / 位数','载荷 / 候选解释','奇偶 / CRC-A 候选','文件时间字段'].map(t=>node('th',t)));}
      if(value.hf)total=rows.length;const pages=Math.max(1,Math.ceil(total/50));this.page=Math.max(0,Math.min(this.page,pages-1));
      const visible=value.lf?Array.from({length:Math.min(50,total-this.page*50)},(_,i)=>this.page*50+i):rows.slice(this.page*50,this.page*50+50);
      for(const row of visible){
        const tr=node('tr');if(value.lf){const l=value.lf,index=l.start+row;for(const text of [index,l.rate_hz?(index*1e6/l.rate_hz).toFixed(3):'未知',parseInt(l.data.slice(row*2,row*2+2),16)])tr.append(node('td',text));}
        else{const f=row;tr.append(node('td',f.index+' / '+(f.offset??'JSON 记录')),node('td',(f.from_card?'卡片 → 读卡器':'读卡器 → 卡片')+' · '+f.bits+' 数据位（'+(f.bit_length_source||'声明值')+'；原始位数 '+(f.raw_bits||'未知')+'）'));const data=node('td');data.append(node('code',f.data.slice(0,128)+(f.data.length>128?'…':'')),node('p',f.label));const details=node('details');details.append(node('summary','查看完整帧字段'));details.ontoggle=()=>{if(details.open&&!details.querySelector('pre'))details.append(node('pre',JSON.stringify(f,null,2)));};data.append(details);tr.append(data,node('td',f.parity_check+' ('+f.parity_errors+') / '+f.crc_a_candidate),node('td',f.file_timestamp==null?'未采集':f.file_timestamp+' + '+f.file_duration+' ticks（未验证）'));}
        body.append(tr);
      }
      $('rf-page').textContent=(this.page+1)+' / '+pages+' · '+total+' 项 · 每页 50 项';$('rf-prev').disabled=this.page===0;$('rf-next').disabled=this.page+1>=pages;
    }
  }
  window.DeskRFFiles=DeskRFFiles;
})();
