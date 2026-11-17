"use strict";
(() => {
 const el=(tag,text)=>{const n=document.createElement(tag);if(text!==undefined)n.textContent=text;return n};
 class DeskConflictForms {
  constructor(){this.root=document.getElementById('classic-conflict-form');this.confirm=el('input');this.confirm.type='checkbox';const label=el('label','已独立核对为同一卡片（UID 相同并不足以证明）');label.append(this.confirm);this.list=el('div');this.root.append(label,this.list);this.choices=[];this.confirm.onchange=()=>this.publish();document.addEventListener('desk-classic-conflicts',event=>this.show(event.detail));document.addEventListener('desk-library-card',()=>this.clear());}
  clear(){this.confirm.checked=false;this.list.replaceChildren();this.choices=[];this.publish();}
  publish(){const value={confirm_same_card:this.confirm.checked,resolutions:this.choices.filter(x=>x.select.value).map(x=>({block:x.block,byte:x.byte,source:x.select.value}))},input=document.getElementById('card-merge-options');input.value=JSON.stringify(value);input.dispatchEvent(new Event('input',{bubbles:true}));}
  show({value}){const conflicts=value?.conflicts||value?.preview?.conflicts;if(!Array.isArray(conflicts))return;this.list.replaceChildren();this.choices=[];let prior;try{prior=JSON.parse(document.getElementById('card-merge-options').value)}catch(_){prior={}}this.confirm.checked=prior.confirm_same_card===true;for(const conflict of conflicts){const label=el('label',`块 ${conflict.block} 字节 ${conflict.byte}：左 ${conflict.left} / 右 ${conflict.right}`),select=el('select');for(const [v,title]of [['','请选择已有来源'],['left','左侧已知字节'],['right','右侧已知字节']]){const option=el('option',title);option.value=v;select.append(option)}select.value=(prior.resolutions||[]).find(r=>r.block===conflict.block&&r.byte===conflict.byte)?.source||'';select.onchange=()=>this.publish();label.append(select);this.list.append(label);this.choices.push({...conflict,select})}if(!conflicts.length)this.list.append(el('p','没有已知字节冲突；预览仍需确认身份、范围与完整性。'));}
 }
 window.DeskConflictForms=DeskConflictForms;
})();
