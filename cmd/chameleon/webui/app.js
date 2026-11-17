"use strict";

const $ = (s) => document.querySelector(s);
const el = (tag, cls, text) => {
  const n = document.createElement(tag);
  if (cls) n.className = cls;
  if (text != null) n.textContent = text;
  return n;
};
let csrfToken = "", writesEnabled = false, generatedCard = null, capabilitiesPending = false;
let rfFileTools = null, environmentTools = null, emulatorTools = null, backgroundTasks = null, readerController = null, workflowController = null, slotReader = null;
let deviceSession = null, sessionEpoch = 0, sessionsEnabled = false, nativeMaintenanceEnabled = false;
let bridgeEnabled = false, bridgeStatus = null, bridgeMedium = "ble";
let libraryManager = null, libraryEditor = null, libraryPersistence = null, libraryState = null, pendingLibraryMutation = false;
const busy = new Set();
let featureAvailability = null;
function clearFeatureDevice() { featureAvailability?.clear(); }
function featureInfo(info) { featureAvailability?.observe(info); slotReader?.observe(info);emulatorTools?.observe(info); }
async function refreshFeatures() {
 try {
  const value = await api("/api/features/device");
  featureAvailability.setStates(value.schema_version, value.states, value.device);
 } catch(error) { if (error.staleSession) return; featureAvailability.clear(error.message); window.desktopToast("无法读取功能状态："+error.message); }
}

const dictionaryLabels = {
  "proxmark3-classic": "Classic 默认密钥",
  "proxmark3-t55xx": "T55xx 默认密码",
  "proxmark3-ultralight-c": "Ultralight C 默认密钥",
  "proxmark3-mifare-plus": "MIFARE Plus 默认密钥",
  "flipper-unleashed-classic": "Classic 扩展密钥"
};
const dictionaryLabel = (name) => dictionaryLabels[name] || "密钥字典";
const groups = { hardware: ["device", "slots", "read"], database: ["cards"], ndef: ["ndef"], acl: ["acl"], vanity: ["vanity"], dictionary: ["dictionary"] };

async function api(path, opts = {}) {
  const epoch = sessionEpoch;
  let offlineFeatureCheck=false;
  if(path==="/api/features/check"&&typeof opts.body==="string"){try{offlineFeatureCheck=/^(library|files)\./.test(JSON.parse(opts.body).id||"");}catch(_){}}
  const taskOperation=backgroundTasks?.operation(path);
  const deviceRequest = (!!taskOperation && taskOperation.session_required!==false) || path.startsWith("/api/device/") || path.startsWith("/api/physical/") || path.startsWith("/api/reader/") || path.startsWith("/api/settings/") || path === "/api/info" || path.startsWith("/api/scan/") || path.startsWith("/api/slot/") || path === "/api/features/device" || (path === "/api/features/check" && !offlineFeatureCheck) || path === "/api/firmware/check";
  const headers = { Accept: "application/json", ...opts.headers };
  if (opts.body != null) headers["Content-Type"] = "application/json";
  if (taskOperation || path.startsWith("/api/device/") || path.startsWith("/api/physical/") || path.startsWith("/api/reader/") || path.startsWith("/api/settings/") || path.startsWith("/api/slot/") || path.startsWith("/api/bridge/") || path.startsWith("/api/session/") || path.startsWith("/api/tasks")) headers["X-Chameleon-Token"] = csrfToken;
  if ((deviceRequest || path.startsWith("/api/bridge/")) && deviceSession) headers["X-Chameleon-Session"] = deviceSession.session_id;
  const controller = new AbortController();
  // Firmware checks allow 30s of device access followed by 60s of HTTP work.
  // Leave time for the server's error response before the browser aborts.
  const budget = path === "/api/firmware/check" ? 95000 : path === "/api/dictionary/download" ? 65000 : 60000;
  const timeout = setTimeout(() => controller.abort(), budget);
  try {
    if(backgroundTasks && deviceRequest && !taskOperation && !["/api/features/check","/api/firmware/check"].includes(path))throw new Error("此操作未在服务端登记；请更新 Desk 并刷新页面");
    if (backgroundTasks && taskOperation) {
     const value=await backgroundTasks.execute(path,{...opts,headers});
     if(deviceRequest && epoch!==sessionEpoch){const error=new Error("旧会话任务结果仅保留在任务列表");error.staleSession=true;throw error;}
     return value;
    }
    const r = await fetch(path, { ...opts, headers, signal: controller.signal });
    if (deviceRequest && (epoch !== sessionEpoch || (deviceSession && r.headers.get("X-Chameleon-Session") !== null && r.headers.get("X-Chameleon-Session") !== deviceSession.session_id))) { const error = new Error("旧设备会话响应已忽略"); error.staleSession = true; throw error; }
    if (r.status === 403 && (path.startsWith("/api/settings/") || path.startsWith("/api/slot/") || path.startsWith("/api/tasks"))) {
      writesEnabled = false; csrfToken = ""; $("#retry-capabilities").hidden = false;
      $("#write-note").textContent = "写入权限或会话已失效，请重试读取权限后再决定是否重新操作";
      $("#menu-write-mode").textContent = "权限未就绪"; updateWriteButtons();
    }
    const ct = r.headers.get("content-type") || "";
    let body;
    try { body = ct.includes("json") ? await r.json() : await r.text(); }
    catch (error) { if (error.staleSession) return;
      if (error.name === "SyntaxError") throw new Error("服务返回了无效 JSON，请重试或重新启动本地服务");
      throw error;
    }
    if (deviceRequest && (epoch !== sessionEpoch || (deviceSession && r.headers.get("X-Chameleon-Session") !== null && r.headers.get("X-Chameleon-Session") !== deviceSession.session_id))) { const error = new Error("旧设备会话响应已忽略"); error.staleSession = true; throw error; }
    if (!r.ok) {
      const error = new Error((body && body.error) || ("请求失败（HTTP " + r.status + "），请检查服务是否仍在运行"));
      error.status=r.status;
      if ((taskOperation || deviceRequest) && body?.result) error.result = body.result;
      throw error;
    }
    if (!ct.includes("json")) throw new Error("服务返回了非 JSON 内容，请重启服务并刷新页面");
    return body;
  } catch (error) {
    if (deviceRequest && epoch !== sessionEpoch) { error.staleSession = true; throw error; }
    if (controller.signal.aborted) {
      const message = "本地服务响应超时。请确认服务仍在运行，再重试。";
      throw new Error(message + (path.startsWith("/api/slot/") ? "写入操作请先读取状态确认结果。" : ""));
    }
    throw error;
  } finally { clearTimeout(timeout); }
}
const postJSON = (path, value) => api(path, { method: "POST", body: JSON.stringify(value) });
function parseObject(id, name) {
  let value;
  try { value = JSON.parse($(id).value); } catch (_) { throw new Error(name + "不是有效 JSON，请导入文件或检查输入"); }
  if (!value || typeof value !== "object" || Array.isArray(value)) throw new Error(name + "必须是 JSON 对象");
  return value;
}
function updateWriteButtons() {
  $("#retry-capabilities").disabled = capabilitiesPending || busy.has("hardware");
  $("#load-card-go").disabled = !writesEnabled || busy.has("hardware");
  document.querySelectorAll("[data-slot-active]").forEach((button) => { button.disabled = !writesEnabled || busy.has("hardware") || button.dataset.slotActive === "true"; });
  updateBridgeButtons();
  updateSessionButtons();
  updateLibraryButtons();
  featureAvailability?.apply();
  libraryEditor?.sync();libraryManager?.sync();
  readerController?.sync();workflowController?.sync();slotReader?.sync();emulatorTools?.sync();environmentTools?.sync();rfFileTools?.sync();
}

function updateLibraryButtons() {
  const blocked = busy.has("database") || !libraryState || libraryState.loading;
  $("#db-save-now").disabled = blocked || !libraryState.dirty || libraryState.blocked || libraryState.saving;
  $("#db-reload").disabled = blocked || libraryState.saving;
  $("#db-restore-previous").disabled = blocked || libraryState.saving || !libraryState.previous;
  $("#db-export-draft").disabled = busy.has("database");
  libraryEditor?.sync();libraryManager?.sync(); readerController?.sync();workflowController?.sync();slotReader?.sync();emulatorTools?.sync();environmentTools?.sync();rfFileTools?.sync();
}
function resetLibraryView() {
  $("#library-results").replaceChildren(); $("#db-id").value = ""; invalidate("#db-out");
}
function startLibraryPersistence() {
  libraryPersistence = new window.DeskLibraryPersistence({
    getText: () => $("#db-json").value,
    setText: (text) => { $("#db-json").value = text; resetLibraryView(); libraryEditor?.clear(); },
    validate: async (database) => {
      const result = await postJSON("/api/database", { op: "validate", database });
      if (result.valid !== true) throw new Error("数据库校验未完成，未更新本地保存版本");
    },
    render: (state) => {
      libraryState = state;
      const status = $("#db-save-state"); status.textContent = state.message;
      status.classList.toggle("has-changes", state.dirty || state.error);
      status.setAttribute("role", state.error ? "alert" : "status");
      updateLibraryButtons();
    }
  });
  $("#db-save-now").addEventListener("click", () => libraryPersistence.save());
  $("#db-reload").addEventListener("click", () => {
    if ((libraryPersistence.dirty || libraryEditor?.dirty || libraryManager?.dirty) && !window.confirm("重新读取将替换本页未保存内容。请先导出本页草稿，确认继续？")) return;
    runTask("database", () => libraryPersistence.load());
  });
  $("#db-restore-previous").addEventListener("click", () => {
    if (!window.confirm("上一保存版本将替换当前卡库。请先导出备份或本页草稿，确认恢复？")) return;
    runTask("database", () => libraryPersistence.restorePrevious());
  });
  $("#db-export-draft").addEventListener("click", () => {
    const url = URL.createObjectURL(new Blob([$("#db-json").value], { type: "text/plain;charset=utf-8" }));
    const a = document.createElement("a"); a.href = url; a.download = "chameleon-desk-library-draft.txt"; a.click();
    setTimeout(() => URL.revokeObjectURL(url), 1000);
  });
  $("#db-json").addEventListener("desk:before-library-replace", (event) => {
    if ((libraryPersistence.dirty || libraryEditor?.dirty || libraryManager?.dirty) && !window.confirm("导入将替换本页未保存的卡库。请先导出草稿，确认继续？")) event.preventDefault();
  });
  window.addEventListener("beforeunload", (event) => {
    if (libraryPersistence.dirty || pendingLibraryMutation || libraryEditor?.dirty || libraryEditor?.pending || libraryManager?.dirty || libraryManager?.pending) { event.preventDefault(); event.returnValue = ""; }
  });
  document.addEventListener("visibilitychange", () => {
    if (document.visibilityState === "hidden" && libraryPersistence.dirty) libraryPersistence.save();
  });
  // Keep imports and database actions disabled until the initial read settles.
  window.libraryReady = runTask("database", () => libraryPersistence.load());
}

function updateBridgeButtons() {
  const blocked = busy.has("hardware") || !bridgeStatus;
  const disconnected = bridgeStatus?.state === "disconnected" && !bridgeStatus?.session_active;
  $("#bridge-scan").disabled = blocked || !disconnected;
  $("#bridge-select").disabled = blocked || !disconnected || !$("#bridge-devices").value;
  $("#bridge-devices").disabled = blocked || !disconnected;
  $("#bridge-connect").disabled = blocked || !disconnected || !bridgeStatus?.selected_device;
  $("#bridge-disconnect").disabled = blocked || bridgeStatus?.state === "scanning" || bridgeStatus?.state === "blocked";
  $("#bridge-refresh").disabled = busy.has("hardware");
}
function showBridgeStatus(status) {
  const transport = status?.transport;
  if (!status || status.api_version !== 1 || transport?.medium !== bridgeMedium || transport.connection !== "tcp" || transport.managed !== true || transport.allows_pairing_changes !== false || transport.allows_device_restore !== false || transport.supports_dfu !== false || !["disconnected","scanning","connecting","connected","disconnecting","blocked"].includes(status.state)) throw new Error("设备桥返回了不支持的连接信息");
  if (bridgeStatus?.state === "connected" && status.state !== "connected") clearBridgeDeviceView();
  bridgeStatus = status;
  const states = { disconnected:"未连接", scanning:"正在扫描", connecting:"正在连接", connected:"已连接", disconnecting:"正在断开", blocked:"连接清理失败，请重启设备桥" };
  $("#bridge-state").textContent = (states[status.state] || "未知状态") + (status.session_active ? " · 设备会话使用中" : "");
  $("#bridge-selected").textContent = status.selected_device ? "已选择：" + (status.selected_device.name || status.selected_device.id) + " · " + status.selected_device.id : "尚未选择设备";
  $("#bridge-error").hidden = !status.error;
  $("#bridge-error").textContent = status.error || "";
  updateBridgeButtons();
}
async function refreshBridgeStatus() {
  if (!bridgeEnabled) return;
  try { showBridgeStatus(await api("/api/bridge/status")); }
  catch (error) { if (error.staleSession) return;
    bridgeStatus = null;
    clearBridgeDeviceView();
    $("#bridge-state").textContent = "无法读取设备桥状态";
    $("#bridge-error").hidden = false; $("#bridge-error").textContent = error.message;
    updateBridgeButtons();
  }
}
function clearBridgeDeviceView() {
  clearFeatureDevice();
  $("#scan-out").replaceChildren(); $("#scan-out").dataset.ready="false"; delete $("#scan-out").dataset.exportJson;
  for (const output of ["#load-card-out", "#fw-out"]) { invalidate(output); $(output).replaceChildren(); }
  setStatus(false, "设备会话已断开");
  $("#info-cards").replaceChildren(); $("#info-raw").replaceChildren();
  $("#device").classList.remove("has-device-data"); $("#device .device-hero h1").textContent = "Chameleon Desk";
  const slots = $("#slot-list"); slots.replaceChildren(); slots.dataset.ready = "false"; delete slots.dataset.exportJson;
}
async function bridgeAction(action) {
  try {
    const input = action === "select" ? { device_id: $("#bridge-devices").value } : {};
    if (action === "connect" || action === "disconnect") { sessionEpoch++; clearBridgeDeviceView(); }
    const result = await postJSON("/api/bridge/" + action, input);
    if (sessionsEnabled) await refreshDeviceSession();
    if (action === "scan") {
      if (!Array.isArray(result.devices)) throw new Error("扫描结果无效");
      const select = $("#bridge-devices"); select.replaceChildren(el("option", null, result.devices.length ? "选择扫描到的设备" : (bridgeMedium === "usb-serial" ? "未发现串口，请检查 USB 连接和权限" : "未发现设备，请确认设备正在广播"))); select.firstChild.value = "";
      for (const device of result.devices) { const option = el("option", null, (device.name || "未命名设备") + " · " + device.id); option.value = device.id; select.append(option); }
      await refreshBridgeStatus();
    } else {
      showBridgeStatus(result);
      if (action === "select" || action === "disconnect") clearBridgeDeviceView();
      if (action === "connect") { await loadInfo(); await refreshBridgeStatus(); }
    }
  } catch (error) { if (error.staleSession) return;
    if (sessionsEnabled) { try { await refreshDeviceSession(); } catch (_) { deviceSession=null; } }
    await refreshBridgeStatus();
    $("#bridge-error").hidden = false; $("#bridge-error").textContent = error.message;
    throw error;
  }
}
async function runTask(group, action) {
  if (busy.has(group)) return;
  busy.add(group);
  const saved = new Map();
  for (const id of groups[group] || []) {
    const win = document.getElementById(id); win.setAttribute("aria-busy", "true");
    win.querySelectorAll(".window-content button, .window-content input, .window-content textarea, .window-content select").forEach((control) => { saved.set(control, control.disabled); control.disabled = true; });
  }
  try { await action(); }
  catch (e) { window.desktopToast("操作未完成：" + e.message); }
  finally {
    busy.delete(group);
    saved.forEach((disabled, control) => { if (control.isConnected) control.disabled = disabled; });
    for (const id of groups[group] || []) document.getElementById(id).removeAttribute("aria-busy");
    updateWriteButtons();
  }
}
function setStatus(ok, text) {
  const s = $("#status"); s.className = "pill " + (ok ? "pill-ok" : "pill-err"); s.textContent = text;
  const menu = $("#menu-connection"); menu.classList.toggle("connected", ok); menu.lastChild.textContent = ok ? "已连接" : "未连接";
  $("#connection-hint").hidden = ok;
}
// Hardware results share the same panel, field and state components.
function appSymbol(id, cls = "panel-symbol") {
  const symbol = el("span", cls);
  const svg = document.querySelector(`.dock [data-open="${id}"] svg`);
  if (svg) symbol.append(svg.cloneNode(true));
  return symbol;
}
function rawDetails(value) {
  const details = el("details", "raw-result hardware-raw"), pre = el("pre");
  pre.textContent = JSON.stringify(value, null, 2);
  details.append(el("summary", null, "查看原始数据 JSON"), pre); return details;
}
function resultStamp() {
  return "本次读取 " + new Intl.DateTimeFormat("zh-CN", { hour:"2-digit", minute:"2-digit", second:"2-digit", hour12:false }).format(new Date());
}
function resultHeader(title, subtitle, badge) {
  const header = el("div", "result-heading"), text = el("div");
  text.append(el("h3", null, title), el("p", null, subtitle)); header.append(text);
  if (badge) header.append(el("span", "result-badge", badge));
  return header;
}
function fieldGrid(fields) {
  const grid = el("dl", "inspector-fields");
  for (const [label, value, mono] of fields) {
    const field = el("div", "inspector-field");
    field.append(el("dt", null, label), el("dd", mono ? "hex-value" : "", value == null || value === "" ? "未返回" : String(value))); grid.append(field);
  }
  return grid;
}
function readableError(error) {
  if (/Failed to fetch|NetworkError|Load failed|Network request failed/i.test(error.message)) return "无法访问本地服务。请确认服务仍在运行，再重试。";
  if (/busy|in use|resource temporarily unavailable/i.test(error.message)) return "设备正被其他程序占用。关闭其他连接后重试。";
  if (/^open (?:\/dev\/|COM\d)/i.test(error.message) && /no such file|cannot find|not configured/i.test(error.message)) return "未找到设备。请检查 USB 连接，重新连接后重试。";
  return error.message;
}
function hardwareState(container, kind, title, message, retry, group = "hardware") {
  const panel = el("div", "hardware-state state-" + kind); panel.setAttribute("role", kind === "error" ? "alert" : "status");
  panel.append(el("span", "state-symbol", kind === "loading" ? "" : kind === "error" ? "!" : "⌁"));
  const content = el("div"); content.append(el("h3", null, title), el("p", null, message));
  if (retry) { const button = el("button", null, "重试"); button.addEventListener("click", () => runTask(group, retry)); content.append(button); }
  panel.append(content); container.replaceChildren(panel);
}
function hardwareFailure(container,title,error,retry) {
  hardwareState(container,"error",title,readableError(error),retry);
  container.querySelector(".hardware-state>div").append(rawDetails({error:error.message}));
}
async function loadInfo() {
  const cards = $("#info-cards"); $("#info-raw").replaceChildren();
  hardwareState(cards, "loading", "正在读取设备", "读取型号、工作模式、电量与槽位概览…");
  try {
    const info = await api("/api/info"); cards.replaceChildren();
    featureInfo(info);
    if (!info || typeof info.model !== "string" || !Array.isArray(info.slots)) throw new Error("设备信息不完整，请重试读取");
    const fw = info.firmware ? info.firmware.major + "." + info.firmware.minor : "";
    setStatus(true, info.model + " · fw " + fw); $("#device").classList.add("has-device-data");
    $("#device .device-hero h1").textContent = info.model;
    const active = info.slots.find((slot) => slot.active), mode = {emulator:"卡片模拟",reader:"读卡器"}[info.mode] || info.mode || "未返回";
    const metrics = [["设备型号",info.model],["固件版本",info.git_version || fw || "未返回"],["工作模式",mode],["电量", info.battery ? info.battery.percent + "%" : "未返回"],["可用命令",info.capabilities?.length ?? "未返回"],["槽位",info.slots.length + " 个 · 当前 " + (active ? active.number : "未返回")]];
    metrics.forEach(([label,value],i) => {
      const card = el("div", "card metric-tile"); card.append(el("div", "k", label),el("div", "v",String(value)));
      if (i === 3 && info.battery) {
        const meter = el("meter", "battery-meter"); meter.min=0; meter.max=100; meter.value=info.battery.percent; meter.setAttribute("aria-label","设备电量"); card.append(meter,el("small",null,info.battery.millivolts + " mV"));
      }
      cards.append(card);
    });
    $("#info-raw").append(el("p","read-stamp",resultStamp()),rawDetails(info));
  } catch (error) { if (error.staleSession) return;
    clearFeatureDevice(); setStatus(false,"设备连接失败"); $("#device").classList.remove("has-device-data");
    hardwareFailure(cards,"未能连接设备",error,loadInfo);
  }
}
async function loadSlots() {
  const list = $("#slot-list"); list.dataset.ready="false"; delete list.dataset.exportJson; hardwareState(list,"loading","正在读取槽位","读取当前槽位与 HF / LF 配置…");
  try {
    const info=await api("/api/info");
    featureInfo(info);
    if (!info || !Array.isArray(info.slots)) throw new Error("槽位信息不完整，请重试读取");
    setStatus(true,info.model); list.replaceChildren();
    const active=info.slots.find((slot)=>slot.active), occupied=info.slots.filter((slot)=>slot.hf_enabled || slot.lf_enabled).length;
    list.append(resultHeader(info.slots.length + " 个槽位",occupied + " 个已启用 · 当前槽位 " + (active ? active.number : "未返回"),writesEnabled ? "写入已启用" : "只读查看"));
    const grid=el("div","slot-grid");
    for (const slot of info.slots) {
      const row=el("article","slot slot-card"+(slot.active ? " active" : ""));
      const heading=el("div","slot-card-heading"); heading.append(el("span","slot-number",String(slot.number)),el("h3",null,"槽位 " + slot.number));
      if (slot.active) heading.append(el("span","result-badge","当前"));
      row.append(heading);
      for (const [name,enabled,type] of [["HF",slot.hf_enabled,slot.hf_name],["LF",slot.lf_enabled,slot.lf_name]]) {
        const channel=el("div","slot-channel"+(enabled ? " enabled" : ""));
        channel.append(el("span","channel-label",name),el("span",null,enabled ? type || "已启用" : "未启用"),el("i","channel-dot")); row.append(channel);
      }
      if (writesEnabled) {
        const button=el("button","slot-activate",slot.active ? "已激活" : "激活此槽位"); button.dataset.slotActive=String(!!slot.active); button.disabled=slot.active || busy.has("hardware");
        button.addEventListener("click",()=>runTask("hardware",async()=>{
          if (!writesEnabled) return;
          try { await postJSON("/api/slot/activate",{slot:slot.number}); await loadSlots(); }
          catch (error) { if (error.staleSession) return; slotMutationFailure(error); }
        })); row.append(button);
      }
      grid.append(row);
    }
    if (!info.slots.length) hardwareState(grid,"empty","没有返回槽位","可重新读取设备信息确认。",loadSlots);
    list.append(grid,el("p","read-stamp",resultStamp()),rawDetails({slots:info.slots}));
    list.dataset.exportJson=JSON.stringify({slots:info.slots}); list.dataset.ready="true";
  } catch (error) { if (error.staleSession) return; clearFeatureDevice(); setStatus(false,"设备连接失败"); hardwareFailure(list,"未能读取槽位",error,loadSlots); }
}
function slotMutationFailure(error) {
  clearFeatureDevice();
  const list = $("#slot-list"); list.dataset.ready = "false"; delete list.dataset.exportJson;
  hardwareFailure(list, "槽位状态需要重新读取", error, loadSlots);
}
const hexNumber = (value, digits) => Number.isInteger(value) ? "0x" + value.toString(16).toUpperCase().padStart(digits,"0") : "未返回";
function renderScanCard(card, kind, index) {
  const article=el("article","scan-card"), heading=el("div","scan-card-heading"), title=el("div");
  const hf=kind==="hf", identifier=hf ? card.uid : card.id ?? card.uid, protocol=hf ? "ISO 14443-A" : (card.protocol || "LF").toUpperCase();
  const type=hf ? (card.type || "高频卡片").replace(" (exact model unknown)","") : protocol;
  title.append(el("span","panel-eyebrow",hf ? "HF · 13.56 MHz" : "LF · 125 kHz"),el("h3",null,type));
  heading.append(appSymbol("read","panel-symbol card-symbol"),title,el("span","card-index",String(index+1).padStart(2,"0"))); article.append(heading);
  const id=el("div","identifier-strip"), value=el("code","identifier-value",identifier || "未返回");
  id.append(el("span","panel-eyebrow",hf ? "UID" : "卡片 ID"),value);
  if (identifier) {
    const copy=el("button","copy-identifier",hf ? "复制 UID" : "复制 ID");
    copy.addEventListener("click",async()=>{try{if (!navigator.clipboard?.writeText) throw new Error("unavailable"); await navigator.clipboard.writeText(identifier); window.desktopToast("标识已复制");}catch(_){window.desktopToast("浏览器未允许复制，可选中标识手动复制");}}); id.append(copy);
  }
  article.append(id);
  const fields=hf ? [["协议",protocol],["UID 长度",identifier ? Math.floor(identifier.replace(/\s/g,"").length/2) + " 字节" : null],["ATQA",hexNumber(card.atqa,4),true],["空口字节",card.atqa_wire,true],["SAK",hexNumber(card.sak,2),true],["ATS",card.ats || "未返回",true]] : [["协议",protocol],["标签类型编号",card.tag_type ?? "未返回"],["ID 长度",identifier ? Math.floor(identifier.replace(/\s/g,"").length/2) + " 字节" : null]];
  article.append(fieldGrid(fields));
  if (hf && card.type_basis) article.append(el("p","inference-note","类型依据："+(card.type_basis === "SAK-based inference; exact chip model not verified" ? "按 SAK 推断，具体芯片型号尚未核验" : card.type_basis)));
  article.append(rawDetails(card)); return article;
}
async function scan(kind) {
  const id = kind === "hf" ? "reader.hf-scan" : "reader.lf-scan";
  if (featureAvailability.state(id).state === "unknown") await refreshFeatures();
  if (featureAvailability.state(id).state !== "available") {
    window.desktopToast("扫描不可用：请在设备管理中查看功能限制并刷新状态"); return;
  }
  const out=$("#scan-out"); $("#read").classList.add("has-scan-data");
  $("#scan-hf").classList.toggle("primary",kind==="hf"); $("#scan-lf").classList.toggle("primary",kind==="lf");
  hardwareState(out,"loading","正在扫描 " + kind.toUpperCase(),"请将卡片保持在对应天线附近…");
  try {
    const result=await api("/api/scan/"+kind);
    if (!result || typeof result.present!=="boolean") throw new Error("扫描结果不完整，请重试");
    const cards=kind==="hf" ? result.cards : result.card ? [result.card] : [];
    if (result.present && (!Array.isArray(cards) || !cards.length || cards.some((card)=>!card || typeof card!=="object"))) throw new Error("扫描结果不完整，请重试");
    setStatus(true,"扫描完成 · 设备可用"); out.replaceChildren();
    if (!result.present) { hardwareState(out,"empty","未检测到 " + kind.toUpperCase() + " 卡片","设备已响应。请调整卡片位置，或确认所选频段后重试。",()=>scan(kind)); return; }
    out.append(resultHeader("已发现 " + cards.length + " 张卡片",resultStamp(),kind.toUpperCase())); cards.forEach((card,index)=>out.append(renderScanCard(card,kind,index)));
  } catch (error) { if (error.staleSession) return; clearFeatureDevice(); setStatus(false,"设备操作失败"); hardwareFailure(out,"扫描未完成",error,()=>scan(kind)); }
}
const {renderToolResult,panelButton}=window.createToolPanels({el,$,appSymbol,fieldGrid,hardwareState,hexNumber,resultStamp});
const outputGroups={"#db-out":"database","#ndef-out":"ndef","#acl-out":"acl","#vanity-out":"vanity","#dict-out":"dictionary","#fw-out":"hardware","#load-card-out":"hardware"};
const outputRevisions=new WeakMap();
function resultParts(sel) {
  const pre=$(sel),panel=pre.closest("details"); return {pre,panel,summary:panel?.previousElementSibling};
}
function revealResult(pre,summary) {
  const win=pre.closest(".app-window"); if(win&&!win.hidden&&win.classList.contains("front")) summary.scrollIntoView?.({block:"start"});
}
function resultError(sel,error,retry,group=outputGroups[sel]) {
  const {pre,panel,summary}=resultParts(sel); pre.dataset.ready="false"; pre.textContent="错误："+error.message;
  if(panel) {panel.hidden=false;panel.open=false;}
  if(summary){summary.hidden=false;summary.classList.add("result-error");hardwareState(summary,"error","操作未完成",readableError(error),retry,group);revealResult(pre,summary);}
  if(sel==="#load-card-out"&&error.result){
    pre.textContent=JSON.stringify({error:error.message,result:error.result},null,2);
    try { summary.append(renderToolResult(sel,error.result,{partial:true})); } catch (_) {}
  }
}
async function showPre(sel,action,options={}) {
  const {pre,panel,summary}=resultParts(sel),group=options.group||outputGroups[sel];
  const revision=(outputRevisions.get(pre)||0)+1;outputRevisions.set(pre,revision);
  pre.dataset.ready="false";pre.textContent="";panel.hidden=true;panel.open=false;
  summary.hidden=false;summary.classList.remove("result-error");
  hardwareState(summary,"loading","正在处理","请稍候，结果将在这里显示。",null,group);
  if(sel==="#ndef-out"){generatedCard=null;$("#ndef-use-result").hidden=true;}
  try {
    const value=await action(()=>outputRevisions.get(pre)===revision);
    if(outputRevisions.get(pre)!==revision) return null;
    if(value==null||typeof value!=="object") throw new Error("返回的数据格式不完整，请重试");
    const rendered=renderToolResult(sel,value,options);
    summary.replaceChildren(rendered);pre.textContent=JSON.stringify(value,null,2);pre.dataset.ready="true";panel.hidden=false;
    if(sel==="#ndef-out"&&options.generated){generatedCard=value;$("#ndef-use-result").hidden=false;}
    if(sel==="#dict-out"&&!Array.isArray(value)) $("#dictionary-source-options").open=false;
    const target=sel==="#db-out"&&options.op==="list"&&value.length ? $("#library-results") : sel==="#dict-out"&&Array.isArray(value) ? $("#dictionary-source-options") : summary;
    revealResult(pre,target);
    return value;
  } catch(error) { if (error.staleSession) return;
    if(outputRevisions.get(pre)!==revision) return null;
    resultError(sel,error,options.retry?()=>showPre(sel,action,options):null,group);return null;
  }
}
function bindOutput(button,sel,group,action,options={}) {
  $(button).addEventListener("click",()=>runTask(group,()=>showPre(sel,action,{group,retry:sel!=="#load-card-out",...options})));
}
function invalidate(sel) {
  const {pre,panel,summary}=resultParts(sel);outputRevisions.set(pre,(outputRevisions.get(pre)||0)+1);
  pre.dataset.ready="false";pre.textContent="";panel.hidden=true;panel.open=false;summary.hidden=true;
}
function updateCardSource(input,container) {
  const source=$(container); source.replaceChildren();
  try {
    const card=JSON.parse($(input).value);
    if(!card||!Array.isArray(card.data)) return;
    const label=el("div","source-file");label.append(appSymbol("ndef"),el("b",null,card.name||"当前卡片文件"),el("span",null,card.data.length+" 个数据单元 · "+(card.uid||"未提供 UID")));source.append(label);
  } catch(_) {}
}
function updateNdefSource(){updateCardSource("#ndef-input","#ndef-source");}
function wire() {
  featureAvailability = new window.DeskFeatureAvailability({ api: postJSON, changed: updateWriteButtons, writeAllowed: () => writesEnabled,
    baseDisabled: (button, f) => {
      if (f.requirement_mode === "bridge") return button.disabled;
      const group = Object.entries(groups).find(([, ids]) => ids.includes(button.closest(".app-window")?.id))?.[0];
      return busy.has(group) || (!!f.write && !writesEnabled) || button.dataset.slotActive === "true";
    }
  });
  workflowController = new window.DeskReaderWorkflows({post:postJSON,features:featureAvailability,run:runTask,busy:()=>busy.has("hardware"),libraryBusy:()=>busy.has("database")||!libraryState||libraryState.loading,importRead:(result,current)=>libraryEditor.importRead(result,current),onSelectedRepair:async(result,current)=>{if(libraryEditor.dirty)throw Error("先保存或放弃当前编辑");const base=$("#db-json").value,source=JSON.parse(result.input.raw).source,card=await postJSON("/api/database",{op:"show",database:JSON.parse(base),id:source.id});if(!current()||base!==$("#db-json").value||libraryEditor.dirty)throw Error("来源或卡库已变化，未应用补读");libraryEditor.open(card);$("#card-selected-repair").value=JSON.stringify({schema:1,result:result.value,error:result.error||null});$("#card-selected-repair").dispatchEvent(new Event("input",{bubbles:true}));document.querySelector('.dock [data-open="cards"]').click();$("#card-selected-repair-tools").open=true;},notice:window.desktopToast});
  new window.DeskWorkflowForms({controller:workflowController,getText:()=>$("#db-json").value});
  readerController = new window.DeskReader({post:postJSON,features:featureAvailability,run:runTask,busy:()=>busy.has("hardware"),libraryBusy:()=>busy.has("database")||!libraryState||libraryState.loading,importRead:(result,current)=>libraryEditor.importRead(result,current)});
  libraryEditor = new window.DeskLibraryEditor({post:postJSON,run:runTask,getText:()=>$("#db-json").value,setText:text=>{$("#db-json").value=text;resetLibraryView();},changed:()=>libraryPersistence.changed(),busy:()=>busy.has("database")||!libraryState||libraryState.loading,features:featureAvailability,notice:window.desktopToast,display:card=>{$("#db-id").value=card.id;return showPre("#db-out",async()=>card,{op:"show",group:"database"});}});
  libraryManager = new window.DeskLibraryManager({post:postJSON,run:runTask,getText:()=>$("#db-json").value,setText:text=>{$("#db-json").value=text;resetLibraryView();},changed:()=>libraryPersistence.changed(),busy:()=>busy.has("database")||!libraryState||libraryState.loading,canMutate:()=>!libraryEditor?.dirty,features:featureAvailability,selectCard:id=>dbCall({op:"show",id})});
  new window.DeskConflictForms({getText:()=>$("#db-json").value});
  slotReader = new window.DeskSlotReader({post:postJSON,features:featureAvailability,run:runTask,busy:()=>busy.has("hardware"),libraryBusy:()=>busy.has("database")||!libraryState||libraryState.loading,importRead:(result,current)=>libraryEditor.importRead(result,current)});
  emulatorTools = new window.DeskEmulatorTools({post:postJSON,features:featureAvailability,run:runTask,busy:()=>busy.has("hardware"),writeAllowed:()=>writesEnabled});
  rfFileTools = new window.DeskRFFiles({post:postJSON,api,features:featureAvailability,run:runTask,busy:()=>busy.has("analysis")});
  environmentTools = new window.DeskEnvironment({api,features:featureAvailability,run:runTask,busy:()=>busy.has("hardware")});
  new window.DeskConfiguration({api,post:postJSON,features:featureAvailability,run:runTask,writeAllowed:()=>writesEnabled});
  $("#refresh-features").addEventListener("click", () => runTask("hardware", refreshFeatures));
  for(const id of ["#load-card-json", "#load-slot", "#load-persist", "#load-activate"]) {
    $(id).addEventListener("input",()=>featureAvailability.checkCard());
    $(id).addEventListener("change",()=>featureAvailability.checkCard());
  }

  for (const action of ["scan", "select", "connect", "disconnect"]) $("#bridge-" + action).addEventListener("click", () => runTask("hardware", () => bridgeAction(action)));
  $("#session-refresh").addEventListener("click", () => runTask("hardware", refreshDeviceSession));
  for (const action of ["connect", "disconnect"]) $("#session-"+action).addEventListener("click", () => runTask("hardware", () => sessionAction(action)));
  $("#bridge-refresh").addEventListener("click", () => runTask("hardware", refreshBridgeStatus));
  $("#bridge-devices").addEventListener("change", updateBridgeButtons);
  document.querySelectorAll("pre").forEach((pre) => {
    if(!Object.hasOwn(outputGroups,"#"+pre.id))return;
    const summary = el("div", "result-summary tool-result"); summary.hidden = true; summary.setAttribute("role", "status");
    const details = el("details", "raw-result"); details.hidden = true; details.append(el("summary", null, "查看完整结果 JSON")); pre.before(summary, details); details.append(pre);
  });
  document.querySelectorAll("textarea").forEach((area) => {
    if (area.closest(".reader-workflows, .card-editor, .read-result-import, #library-manager")) return;
    const details = el("details", "json-input"); details.append(el("summary", null, "或粘贴 / 查看 JSON")); area.before(details); details.append(area);
  });
  $("#refresh-info").addEventListener("click", () => runTask("hardware", loadInfo));
  $("#refresh-slots").addEventListener("click", () => runTask("hardware", loadSlots));
  $("#scan-hf").addEventListener("click", () => runTask("hardware", () => scan("hf")));
  $("#scan-lf").addEventListener("click", () => runTask("hardware", () => scan("lf")));
  bindOutput("#fw-check", "#fw-out", "hardware", () => postJSON("/api/firmware/check", {}));
  bindOutput("#ndef-go", "#ndef-out", "ndef", () => postJSON("/api/ndef/read", parseObject("#ndef-input", "卡片")));
  bindOutput("#acl-decode-go", "#acl-out", "acl", () => postJSON("/api/acl/decode", { access: $("#acl-hex").value.trim() }));
  bindOutput("#acl-encode-go", "#acl-out", "acl", () => {
    const raw = $("#acl-cond").value.split(",").map((x) => x.trim());
    if (raw.length !== 4 || raw.some((x) => !/^[0-7]$/.test(x))) throw new Error("请输入四个 0 至 7 的整数，以逗号分隔");
    return postJSON("/api/acl/encode", { conditions: raw.map(Number) });
  });
  bindOutput("#vanity-go", "#vanity-out", "vanity", () => {
    const uid = $("#vanity-uid").value.replace(/\s/g, "");
    if (!/^(?:[0-9a-f]{8}|[0-9a-f]{14})$/i.test(uid)) throw new Error("UID 必须是 4 或 7 字节的十六进制内容");
    return postJSON("/api/vanity", { uid });
  });
  bindOutput("#dict-go", "#dict-out", "dictionary", async (isCurrent) => {
    const sources = await api("/api/dictionary/sources"), list = $("#dictionary-sources");
    if (!isCurrent()) return null;
    list.replaceChildren();
    if(!Array.isArray(sources)||sources.some((source)=>!source||typeof source.name!=="string")) throw new Error("字典来源格式不完整，请重试");
    const selected = $("#dict-name").value;
    $("#dict-name").replaceChildren(...sources.map((source) => {
      const option = el("option", null, dictionaryLabel(source.name)); option.value = source.name; return option;
    }));
    if (sources.some((source) => source.name === selected)) $("#dict-name").value = selected;
    sources.forEach((source)=>{
      const button=el("button","library-item source-card"),text=el("span","library-card-text");
      let host="内置来源";try{host=new URL(source.url).hostname;}catch(_){}
      text.append(el("b",null,dictionaryLabel(source.name)),el("small",null,host));button.append(appSymbol("dictionary"),text);
      button.setAttribute("aria-pressed",String(source.name===$("#dict-name").value));
      button.addEventListener("click",()=>{
        $("#dict-name").value=source.name;$("#dict-name").dispatchEvent(new Event("input",{bubbles:true}));
        list.querySelectorAll("button").forEach((item)=>item.setAttribute("aria-pressed",String(item===button)));
        $("#dictionary-source-options").open=false;
      });list.append(button);
    });
    $("#dictionary-source-options").open=true;
    return sources.map((source) => ({ name: dictionaryLabel(source.name) }));
  });
  bindOutput("#dict-download", "#dict-out", "dictionary", async () => {
    const name = $("#dict-name").value;
    if (!name) throw new Error("请选择字典类型");
    const dictionary = await postJSON("/api/dictionary/download", { name });
    return { ...dictionary, name: dictionaryLabel(name) };
  });
  bindOutput("#load-card-go", "#load-card-out", "hardware", async () => {
    if (!writesEnabled) throw new Error("只读模式：需在启动服务时启用 --allow-writes");
    const card = parseObject("#load-card-json", "卡片"), slot = Number($("#load-slot").value);
    if (!Number.isInteger(slot) || slot < 1 || slot > 8) throw new Error("槽位必须是 1 至 8 的整数");
    if (!window.confirm("加载将替换槽位 " + slot + " 的内容。确认已备份并继续？")) throw new Error("已取消，槽位内容未更改");
    let result;
    try { result = await postJSON("/api/slot/load-card", { slot, card, persist: $("#load-persist").checked, activate: $("#load-activate").checked }); }
    catch (error) { if (error.staleSession) return; slotMutationFailure(error); throw error; }
    await loadSlots(); return result;
  });
  bindOutput("#ndef-write-go", "#ndef-out", "ndef", async () => {
    const inputs=["#ndef-input","#ndef-text","#ndef-uri","#ndef-language"];
    const snapshot=inputs.map((id)=>$(id).value);
    const body = { card: parseObject("#ndef-input", "卡片"), text: $("#ndef-text").value, uri: $("#ndef-uri").value, language: $("#ndef-language").value };
    if (!body.text && !body.uri) throw new Error("请填写文本或网址，再生成卡片文件");
    if (body.text && (!/^[\x00-\x7F]*$/.test(body.language) || body.language.length>63)) throw new Error("语言代码最多为 63 个 ASCII 字符");
    const value=await postJSON("/api/ndef/write",body);
    if(snapshot.some((value,i)=>value!==$(inputs[i]).value)) throw new Error("输入已变化，请重新生成");
    return value;
  }, {generated:true});
  $("#ndef-use-result").addEventListener("click", () => {
    if (!generatedCard) return;
    $("#ndef-input").value = JSON.stringify(generatedCard, null, 2); updateNdefSource();
    window.desktopToast("生成的卡片已设为当前输入，可读取记录或继续编辑");
  });
  const dbCall=(payload)=>runTask("database",()=>showPre("#db-out",async(isCurrent)=>{
    if(payload.op==="list"){ $("#library-results").replaceChildren();$("#db-id").value=""; }
    payload.database=parseObject("#db-json","数据库");
    if(payload.op==="add") payload.card=parseObject("#db-card","卡片");
    if((payload.op==="show"||payload.op==="delete")&&!payload.id) throw new Error("请先选择卡片或填写卡片 ID / 完整名称");
    const original=$("#db-json").value;
    const mutation = payload.op === "add" || payload.op === "delete";
    let value;
    if (mutation) pendingLibraryMutation = true;
    try { value = await postJSON("/api/database", payload); }
    finally { if (mutation) pendingLibraryMutation = false; }
    if (!isCurrent()) return null;
    if(payload.op==="add"||payload.op==="delete"){
      if($("#db-json").value!==original) throw new Error("数据库输入已变化，请重新操作");
      const next=payload.op==="add"?value:value.database;
      if(!next||typeof next!=="object"||Array.isArray(next)) throw new Error("数据库结果不完整，请重新读取");
      $("#db-json").value=JSON.stringify(next,null,2);$("#library-results").replaceChildren();$("#db-id").value="";
      libraryPersistence.changed();
      window.desktopToast("数据库已更新，将自动保存到当前浏览器");
    }
    if(payload.op==="show") libraryEditor.open(value);
    if(payload.op==="list"){
      if(!Array.isArray(value)||value.some((row)=>!row||typeof row.id!=="string"||!row.id)) throw new Error("卡片列表格式不完整，请重试搜索");
      const list=$("#library-results");let displayed=0;
      const appendPage=()=>{
        list.querySelector('.library-more')?.remove();
        value.slice(displayed,displayed+100).forEach((card)=>{
          const row=el("button","library-item library-card"),text=el("span","library-card-text");
          row.dataset.cardId=card.id;row.setAttribute("aria-pressed","false");
          text.append(el("b",null,card.name||card.id),el("small",null,card.tag_name||card.uid||"卡片文件"));
          row.append(appSymbol("cards"),text,el("span","result-badge",(card.frequency|| (card.tag>=1000?"hf":"lf")).toUpperCase()));
          row.addEventListener("click",()=>{
            list.querySelectorAll('[data-card-id]').forEach((item)=>item.setAttribute("aria-pressed",String(item===row)));
            $("#db-id").value=card.id;$("#db-id").dispatchEvent(new Event("input",{bubbles:true}));
            dbCall({op:"show",id:card.id});
          });list.append(row);
        });
        displayed=Math.min(displayed+100,value.length);
        if(displayed<value.length) list.append(panelButton("显示更多（"+displayed+" / "+value.length+"）",appendPage,"library-more"));
      };appendPage();
    }
    return value;
  },{op:payload.op,group:"database",retry:payload.op==="list"||payload.op==="show"}));
  $("#db-list").addEventListener("click", () => dbCall({ op: "list", query: $("#db-query").value.trim() }));
  $("#db-show").addEventListener("click", () => dbCall({ op: "show", id: $("#db-id").value.trim() }));
  $("#db-add").addEventListener("click", () => dbCall({ op: "add" }));
  $("#db-delete").addEventListener("click", () => { if (!busy.has("database") && window.confirm("从当前卡库中移除这张卡片？此更改会自动保存到浏览器。")) dbCall({ op: "delete", id: $("#db-id").value.trim() }); });
  $("#db-json").addEventListener("input", () => { resetLibraryView(); libraryPersistence?.changed(); libraryEditor?.sync();libraryManager?.sync(); });
  for (const id of ["#ndef-input", "#ndef-text", "#ndef-uri", "#ndef-language"]) $(id).addEventListener("input", () => { generatedCard = null; $("#ndef-use-result").hidden = true; invalidate("#ndef-out"); updateNdefSource(); });
  $("#db-card").addEventListener("input",()=>updateCardSource("#db-card","#db-card-source"));
  $("#db-id").addEventListener("input",()=>invalidate("#db-out"));
  for(const [input,output] of [["#acl-hex","#acl-out"],["#acl-cond","#acl-out"],["#vanity-uid","#vanity-out"],["#dict-name","#dict-out"]]) $(input).addEventListener("input",()=>invalidate(output));
  $("#db-query").addEventListener("input",()=>{$("#library-results").replaceChildren();$("#db-id").value="";invalidate("#db-out");});
  $("#db-query").addEventListener("keydown",(event)=>{if(event.key==="Enter"&&!event.isComposing){event.preventDefault();$("#db-list").click();}});
  $("#retry-capabilities").addEventListener("click", loadCapabilities);
}
async function loadCapabilities() {
  const button = $("#retry-capabilities"); if (capabilitiesPending || busy.has("hardware")) return;
  capabilitiesPending = true; updateWriteButtons();
  try {
    const caps = await api("/api/capabilities");
    writesEnabled = caps.writes_enabled === true && !!caps.csrf_token; csrfToken = caps.csrf_token || "";
    if (!featureAvailability.configure(caps)) throw new Error("功能清单版本或内容不受支持，请重试或更新服务后刷新");
    sessionsEnabled = caps.device_sessions_enabled === true;
    nativeMaintenanceEnabled = caps.native_usb_maintenance_enabled === true;
    $("#session-tcp-label").hidden = nativeMaintenanceEnabled;
    $("#session-connect").textContent = nativeMaintenanceEnabled ? "连接维护端口" : "连接";
    $("#session-note").textContent = nativeMaintenanceEnabled ? "原生 USB 维护模式：仅用于配对设置和维护。请刷新功能状态；常规 USB 操作使用 Python 桥。" : "USB 应用操作请连接 Python 桥的 TCP 地址。连接由此 Desk 服务的所有页面共享。";
    if (caps.ble_bridge_enabled === true && caps.usb_bridge_enabled === true) throw new Error("服务同时声明了两种设备桥，请检查配置");
    bridgeMedium = caps.usb_bridge_enabled === true ? "usb-serial" : "ble";
    bridgeEnabled = caps.ble_bridge_enabled === true || caps.usb_bridge_enabled === true;
    $("#bridge-title").textContent = bridgeMedium === "usb-serial" ? "USB 串口桥连接" : "蓝牙连接";
    $("#bridge-panel").setAttribute("aria-label", bridgeMedium === "usb-serial" ? "USB 串口桥连接管理" : "BLE 桥连接管理");
    $("#bridge-note").textContent = bridgeMedium === "usb-serial" ? "选择桥所在主机的 Chameleon 应用串口。此连接不支持修改配对设置、整机恢复、进入 bootloader 或固件上传。" : "底层连接为 BLE。此连接不支持修改配对设置、整机恢复或固件上传。";
    $("#session-panel").hidden = !sessionsEnabled || bridgeEnabled;
    if (sessionsEnabled) await refreshDeviceSession();
    if (caps.background_tasks_enabled === true && !backgroundTasks) {
      $("#tasks-panel").hidden=false;
      backgroundTasks=new window.DeskBackgroundTasks({operations:caps.operation_schema_version===1?caps.operations:[],api,container:$("#task-list"),onReaderResult:(result,current)=>{if(busy.has("database"))throw Error("卡库正在处理，请稍后重试");return runTask("database",()=>libraryEditor.importRead(result,current));}});
      backgroundTasks.start();
      window.addEventListener("pagehide",()=>backgroundTasks.stop(),{once:true});
    }
    if(backgroundTasks)backgroundTasks.configureOperations(caps.operation_schema_version===1?caps.operations:[]);
    $("#bridge-panel").hidden = !bridgeEnabled;
    if (bridgeEnabled) await refreshBridgeStatus();
    $("#write-note").textContent = writesEnabled ? "设备写入已启用" : "只读模式 · 仅查看设备配置";
    $("#menu-write-mode").textContent = writesEnabled ? "写入已启用" : "只读模式"; button.hidden = true;
  } catch (e) {
    featureAvailability.clear("无法读取功能清单，请重试读取权限");
    writesEnabled = false; csrfToken = ""; button.hidden = false;
    $("#write-note").textContent = "无法读取服务权限：" + e.message;
    $("#menu-write-mode").textContent = "权限未就绪";
  } finally { capabilitiesPending = false; updateWriteButtons(); }
}
wire();
startLibraryPersistence();
loadCapabilities();

function showDeviceSession(value) {
 if (!value || typeof value.session_id !== "string" || typeof value.connected !== "boolean") throw new Error("设备会话信息无效");
 if (!deviceSession || value.session_id !== deviceSession.session_id || value.connected !== deviceSession.connected) { sessionEpoch++; clearBridgeDeviceView(); }
 deviceSession = value;
 $("#session-state").textContent = (value.connected ? "已连接：" + value.target : "未连接") + " · 会话 " + (value.session_id || "尚未建立");
 $("#session-disconnect").disabled = !value.connected;
}
async function refreshDeviceSession() {
 showDeviceSession(await api("/api/session/status"));

}
async function sessionAction(action) {
 sessionEpoch++; clearBridgeDeviceView();
 try {
  const input = { session_id: deviceSession?.session_id || "" };
  if (action === "connect") input.target = nativeMaintenanceEnabled ? "" : $("#session-tcp").value.trim();
  showDeviceSession(await postJSON("/api/session/"+action,input));
 } catch (error) {
  $("#session-state").textContent = error.message;
  try { await refreshDeviceSession(); } catch (_) { deviceSession=null; }
 }
}

function updateSessionButtons() {
 const blocked=busy.has("hardware") || !deviceSession;
 $("#session-connect").disabled=blocked;
 $("#session-disconnect").disabled=blocked || !deviceSession?.connected;
 $("#session-refresh").disabled=busy.has("hardware");
 $("#session-tcp").disabled=busy.has("hardware");
}
