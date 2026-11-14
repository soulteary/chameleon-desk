"use strict";
window.createToolPanels = function ({el, $, appSymbol, fieldGrid, hardwareState, hexNumber, resultStamp}) {
// Presenters use the same DOM helpers as live hardware inspectors. All content
// is rendered as text; card data, record values and source URLs are untrusted.
function toolPanel(app, title, subtitle, badge) {
  const panel = el("article", "tool-panel");
  const heading = el("div", "tool-panel-heading"), text = el("div");
  text.append(el("span", "panel-eyebrow", subtitle), el("h3", null, title));
  heading.append(appSymbol(app), text);
  if (badge) heading.append(el("span", "result-badge", badge));
  panel.append(heading); return panel;
}
function panelButton(label, action, cls = "") {
  const button = el("button", cls, label); button.addEventListener("click", action); return button;
}
function copyValue(value, label = "内容") {
  return async () => {
    try { if (!navigator.clipboard?.writeText) throw new Error(); await navigator.clipboard.writeText(String(value)); window.desktopToast(label + "已复制"); }
    catch (_) { window.desktopToast("浏览器未允许复制，可选中文本手动复制"); }
  };
}
function valueStrip(label, value, copy = true) {
  const strip = el("div", "identifier-strip");
  strip.append(el("span", "panel-eyebrow", label), el("code", "identifier-value", value));
  if (copy && value != null) strip.append(panelButton("复制", copyValue(value, label), "copy-identifier"));
  return strip;
}
function cardPanel(card, title = "卡片详情", subtitle = "当前卡片文件") {
  if (!card || !Number.isInteger(card.tag) || !Array.isArray(card.data)) throw new Error("卡片结果不完整，请重新读取");
  const panel = toolPanel("cards", card.name || title, subtitle, card.tag >= 1000 ? "HF" : "LF");
  const widths = [...new Set(card.data.map((row) => Array.isArray(row) ? row.length : 0))];
  const unit = widths.length === 1 && widths[0] === 4 ? "页" : widths.length === 1 && widths[0] === 16 ? "块" : "个单元";
  if (card.uid) panel.append(valueStrip("UID / ID", card.uid));
  const names={100:"EM410X",104:"EM410X Electra",1000:"MIFARE Mini",1001:"MIFARE Classic 1K",1002:"MIFARE Classic 2K",1003:"MIFARE Classic 4K",1100:"NTAG213",1101:"NTAG215",1102:"NTAG216",1103:"MIFARE Ultralight",1104:"MIFARE Ultralight C",1105:"MIFARE Ultralight EV1 640",1106:"MIFARE Ultralight EV1 1312",1107:"NTAG210",1108:"NTAG212",3000:"ISO14443-4",3001:"SEOS"};
  panel.append(fieldGrid([["标签类型",names[card.tag]||"标签 "+card.tag],["数据大小",card.data.reduce((sum,row)=>sum+(Array.isArray(row)?row.length:0),0)+" 字节"],["数据单元",card.data.length+" "+unit],["文件 ID",card.id],["SAK",hexNumber(card.sak,2),true],["ATQA 字节",Array.isArray(card.atqa)?card.atqa.map((n)=>Number(n).toString(16).padStart(2,"0").toUpperCase()).join(" "):null,true]]));
  return panel;
}
function permissionTable(caption, headings, rows) {
  const frame = el("div", "permission-frame"), table = el("table", "permission-table");
  table.append(el("caption", null, caption));
  const head = el("thead"), header = el("tr");
  headings.forEach((text) => { const th=el("th",null,text); th.scope="col"; header.append(th); }); head.append(header);
  const body=el("tbody");
  rows.forEach((row)=>{const tr=el("tr"); row.forEach((value,i)=>{const cell=el(i===0?"th":"td"); if(i===0){cell.scope="row";cell.textContent=value;}else cell.append(el("span", "permission-token"+(value==="-"?" denied":""),value==="-"?"禁止":String(value))); tr.append(cell);}); body.append(tr);});
  table.append(head,body); frame.append(table); return frame;
}
function renderNdefRecords(value) {
  const records=value.records??[];
  if (!Array.isArray(records)) throw new Error("NDEF 记录结果不完整，请重新读取");
  const panel=toolPanel("ndef","NDEF 记录", "已解析卡片文件",records.length+" 条记录");
  panel.append(fieldGrid([["存储映射",value.mapping],["可用容量",value.capacity==null?null:value.capacity+" 字节"],["记录数量",records.length]]));
  if (!records.length) { const empty=el("div"); hardwareState(empty,"empty","没有 NDEF 记录","卡片文件可读取，可填写文本或网址生成新文件。");panel.append(empty); }
  const labels={text:"文本记录",uri:"网址记录",mime:"媒体记录",external:"外部类型记录",unknown:"自定义记录"};
  records.forEach((record,index)=>{
    if (!record || typeof record!=="object") throw new Error("NDEF 记录结果不完整，请重新读取");
    const item=el("section","ndef-record"), heading=el("div","record-heading");
    heading.append(el("span","slot-number",String(index+1)),el("h4",null,labels[record.kind]||record.kind||record.type||"记录"),el("span","result-badge",record.language||record.type||"TNF "+record.tnf));
    const content=record.value != null ? String(record.value) : record.kind==="text"||record.kind==="uri" ? "" : record.payload || "";
    item.append(heading,el("div","record-value",content||"（空内容）"));
    if(content) item.append(panelButton("复制记录内容",copyValue(content,"记录内容"),"record-copy"));
    panel.append(item);
  }); return panel;
}
function renderToolResult(sel, value, options = {}) {
  const list=el("div", "tool-result-content"); let panel;
  if (sel==="#acl-out") {
    panel=toolPanel("acl",value.access_hex?"访问位已编码":"访问权限已解码","MIFARE Classic · 仅离线计算","离线");
    if(value.access_hex){
      panel.append(valueStrip("访问位",value.access_hex));
      panel.append(el("p","inference-note","编码结果不会写入卡片。可继续解码，查看各数据块与尾块的权限。"));
      panel.append(panelButton("查看对应权限",()=>{ $("#acl-hex").value=value.access_hex; $("#acl-hex").dispatchEvent(new Event("input",{bubbles:true})); $("#acl-decode-go").click(); }));
    }else{
      if(!Array.isArray(value.conditions)||!Array.isArray(value.blocks)) throw new Error("访问位结果不完整，请重试");
      panel.append(valueStrip("条件",value.conditions.join(", ")));
      const permissions=el("div","permission-layout");
      permissions.append(permissionTable("数据块权限",["数据块","读取","写入","加值","减值 / 恢复"],value.blocks.map((block)=>["块 "+block.block,block.read,block.write,block.increment ?? "-",block.decrement ?? "-"])));
      if(value.trailer) permissions.append(permissionTable("扇区尾块",["字段","读取","写入"],[["Key A",value.trailer.key_a_read,value.trailer.key_a_write],["访问位",value.trailer.access_read,value.trailer.access_write],["Key B",value.trailer.key_b_read,value.trailer.key_b_write]]));
      panel.append(permissions);
      panel.append(el("p","permission-legend","A / B 表示所需密钥类型，禁止表示该操作不允许。此面板不会更改卡片权限。"));
    }
  } else if (sel==="#ndef-out") {
    if("records" in value) panel=renderNdefRecords(value);
    else {panel=cardPanel(value,"新卡片文件","NDEF · 新卡片文件已生成");panel.append(el("p","inference-note","文件已准备，可导出保存。没有写入实体卡片；可使用生成的卡片继续编辑。"));}
  } else if(sel==="#db-out") {
    if(options.op==="list" || Array.isArray(value)) {
      panel=toolPanel("cards",value.length?"已读取 "+value.length+" 条记录":"没有匹配的卡片","当前数据库 · 点击列表卡片查看详情",value.length+" 张");
      panel.append(el("p",null,value.length?"支持按名称、类型、UID 或 ID 搜索。":"可修改搜索条件，或导入 / 添加卡片。"));
    } else if(options.op==="add" || options.op==="delete") {
      panel=toolPanel("cards",options.op==="add"?"卡片已添加":"卡片已移除","数据库已在当前页面中更新","待保存");
      panel.append(el("p",null,"请导出数据库并保留文件，刷新页面不会自动保存更改。"));
      panel.append(panelButton("刷新卡片列表",()=>$("#db-list").click()));
    } else {
      panel=cardPanel(value);
      panel.append(panelButton("在 NDEF 中打开",()=>{
        $("#ndef-input").value=JSON.stringify(value,null,2); $("#ndef-text").value=""; $("#ndef-uri").value="";
        $("#ndef-input").dispatchEvent(new Event("input",{bubbles:true})); $("#ndef-input").closest("details").open=false;
        document.querySelector('.dock [data-open="ndef"]').click();
      }));
    }
  } else if(sel==="#dict-out") {
    if(Array.isArray(value)) {
      panel=toolPanel("dictionary",value.length+" 个内置字典源","选择来源后获取字典；查看来源无需联网","来源");
      panel.append(el("p",null,"来源列表显示发布主机；获取后可以预览密钥并导出完整字典。"));
    } else {
      if(!value || !Array.isArray(value.keys)) throw new Error("字典结果不完整，请重新获取");
      panel=toolPanel("dictionary",value.name||"密钥字典","已获取字典 · 可导出完整文件",value.keys.length+" 个密钥");
      panel.append(fieldGrid([["密钥数量",value.keys.length],["密钥长度",value.keyLength==null?null:value.keyLength/2+" 字节 / "+value.keyLength*4+" 位"],["字典 ID",value.id]]));
      const preview=el("div","key-preview");
      value.keys.slice(0,24).forEach((key)=>preview.append(el("code","key-chip",Array.isArray(key)?key.map((n)=>Number(n).toString(16).padStart(2,"0").toUpperCase()).join(""):String(key))));
      panel.append(preview,el("p","inference-note","预览 "+Math.min(24,value.keys.length)+" / "+value.keys.length+" 个密钥，导出文件包含完整数据。"));
    }
  } else if(sel==="#vanity-out") {
    panel=toolPanel("vanity","UID 默认值推断","根据 UID 长度推断编辑器默认值","离线");
    panel.append(valueStrip("UID",value.uid||$("#vanity-uid").value.replace(/\s/g,"")),fieldGrid([["UID 长度",(value.uid||$("#vanity-uid").value.replace(/\s/g,"")).length/2+" 字节"],["Vanity SAK",value.vanity_sak==null?"无法推断":value.vanity_sak?"默认启用":"默认不启用"]]));
    panel.append(el("p","inference-note","这不是实体卡 SAK 验证结果，也不会修改 UID 或卡片内容。"));
  } else {
    panel=toolPanel("device","处理完成","本次操作结果","完成");
    if(value?.latest) panel.append(el("p",null,value.up_to_date?"当前固件已是最新构建":"发现可用构建："+(value.latest.tag||value.latest.commit||value.latest.asset)));
    value?.notes?.forEach((note)=>panel.append(el("p",null,note)));
  }
  const actions=el("div","result-actions");
  actions.append(panelButton("返回输入区",()=>{const area=list.closest(".window-content");if(area) area.scrollTop=0;}));
  list.append(panel,el("p","read-stamp",resultStamp().replace("读取","处理")),actions); return list;
}

return {renderToolResult,panelButton};
};
