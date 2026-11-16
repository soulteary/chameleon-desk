"use strict";
(() => {
 class DeskMemoryEditor {
  constructor({post,changed}){
   this.post=post;this.changed=changed;this.get=id=>document.getElementById(id);this.epoch=0;
   this.get('card-edit-address').oninput=()=>this.renderUnit();
   this.get('card-edit-unit-apply').onclick=()=>this.applyUnit();
   this.get('card-edit-acl-read').onclick=()=>this.acl(false);
   this.get('card-edit-acl-apply').onclick=()=>this.acl(true);
   this.get('card-edit-rows').addEventListener('input',()=>this.render());
  }
  open(card){this.card=card;this.epoch++;this.original=this.rows();this.get('card-edit-address').value='0';this.get('card-edit-unit-status').textContent='';this.render();}
  rows(){if(!this.card)return [];const text=this.get('card-edit-rows').value;return text===''&&this.card&&!this.card.desk_read&&this.card.data?.length===0&&this.card.tag>=1000?[]:text.split('\n').map(r=>r.replace(/\s/g,'').toUpperCase());}
  address(){const text=this.get('card-edit-address').value;if(!/^\d+$/.test(text))throw Error('单元地址必须为非负整数');const value=Number(text);if(!Number.isSafeInteger(value)||value>=this.rows().length)throw Error('单元地址超出当前数据范围');return value;}
  sync(blocked){this.blocked=!!blocked;this.updateControls();}
  updateControls(){for(const id of ['card-edit-address','card-edit-unit-hex','card-edit-unit-apply','card-edit-sector','card-edit-acl-read','card-edit-acl-values','card-edit-acl-apply'])this.get(id).disabled=this.blocked||this.pending||!this.card;}
  renderUnit(){try{this.get('card-edit-unit-hex').value=this.rows()[this.address()].match(/.{2}/g)?.join(' ')||'';}catch(error){this.get('card-edit-unit-hex').value='';}}
  render(){
   const rows=this.rows(),out=this.get('card-edit-diff');out.replaceChildren();let count=0;
   rows.forEach((row,index)=>{if(row!==(this.original?.[index]||'')){count++;if(count<=64){const line=document.createElement('li');line.textContent=`单元 ${index}: ${this.original?.[index]||'空'} → ${row||'空'}`;out.append(line);}}});
   this.get('card-edit-diff-summary').textContent=`${rows.length} 个单元 · ${count} 行有修改${count>64?'（仅显示前 64 行，导出与保存包含全部修改）':''}`;
   this.get('card-edit-acl-tools').hidden=!(this.card?.desk_read?.read_result?.kind==='classic'||this.card?.tag>=1000&&this.card.tag<=1003);
   this.renderUnit();this.updateControls();
  }
  commitRow(index,value){
   const rows=this.rows(),old=rows[index];if(old==null||old.length!==value.length||! /^(?:[0-9A-F]{2}|\?\?)*$/.test(value))throw Error('保持单元宽度并使用十六进制字节');
   for(let j=0;j<old.length;j+=2)if(old.slice(j,j+2)==='??'&&value.slice(j,j+2)!=='??')throw Error('未知字节需通过补读或验证补齐，不能在编辑器中填值');
   rows[index]=value;this.get('card-edit-rows').value=rows.map(r=>r.match(/.{2}/g)?.join(' ')||'').join('\n');this.get('card-edit-rows').dispatchEvent(new Event('input',{bubbles:true}));
  }
  applyUnit(){if(this.blocked||this.pending||!this.card)return;try{this.commitRow(this.address(),this.get('card-edit-unit-hex').value.replace(/\s/g,'').toUpperCase());this.get('card-edit-unit-status').textContent='已更新编辑草稿，请保存到卡库';}catch(error){this.get('card-edit-unit-status').textContent=error.message;}}
  async acl(write){
   if(this.blocked||this.pending||!this.card)return;
   const status=this.get('card-edit-unit-status'),epoch=this.epoch,snapshot=this.get('card-edit-rows').value,sectorText=this.get('card-edit-sector').value,conditionsText=this.get('card-edit-acl-values').value;
   try{
    const sector=Number(sectorText);if(!/^\d+$/.test(sectorText)||!Number.isInteger(sector)||sector<0||sector>39)throw Error('扇区须为 0–39');const trailer=sector<32?sector*4+3:128+(sector-32)*16+15;
    const row=this.rows()[trailer];if(!row||row.length!==32||row.slice(12,18).includes('?'))throw Error('尾块访问位尚未读取，或扇区超出数据范围');
    const parts=conditionsText.split(',').map(v=>v.trim());if(write&&(parts.length!==4||parts.some(v=>! /^[0-7]$/.test(v))))throw Error('输入四个 0–7 的访问条件，以逗号分隔');
    this.pending=true;this.updateControls();
    let value;if(write){const conditions=parts.map(Number);if(conditions.length!==4||conditions.some(v=>!Number.isInteger(v)||v<0||v>7))throw Error('输入四个 0–7 的访问条件，以逗号分隔');value=await this.post('/api/acl/encode',{conditions});}
    else value=await this.post('/api/acl/decode',{access:row.slice(12,18)});
    if(epoch!==this.epoch||snapshot!==this.get('card-edit-rows').value||sectorText!==this.get('card-edit-sector').value||(write&&conditionsText!==this.get('card-edit-acl-values').value))throw Error('卡片或编辑数据已变化，请重新操作');
    if(write){if(typeof value.access_hex!=='string'||!/^[0-9a-fA-F]{6}$/.test(value.access_hex))throw Error('访问位编码结果无效');this.commitRow(trailer,row.slice(0,12)+value.access_hex.toUpperCase()+row.slice(18));status.textContent='已修改访问位，密钥与 GPB 保留；请保存并核对权限';}
    else{if(!Array.isArray(value.conditions)||value.conditions.length!==4||value.conditions.some(v=>!Number.isInteger(v)||v<0||v>7))throw Error('访问位解码结果无效');this.get('card-edit-acl-values').value=value.conditions.join(',');status.textContent=`扇区 ${sector} 访问条件：${value.conditions.join(',')}`;}
   }catch(error){status.textContent=error.message;}finally{this.pending=false;this.updateControls();}
  }
 }
 window.DeskMemoryEditor=DeskMemoryEditor;
})();
