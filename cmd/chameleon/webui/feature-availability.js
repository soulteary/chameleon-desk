/* Availability is a preview from the Go catalog, not device authorization. */
(() => {
  "use strict";
  const labels = { supported: "已支持", partial: "部分支持", raw: "原始接口", missing: "无专用入口", removed: "已移除" };
  const reasonText = (reason) => {
    const messages = {
      device_unknown: "请先读取设备信息或刷新功能状态", capabilities_unknown: "设备命令列表未知，请重新读取",
      model_unknown: "设备型号未知，请重新读取", unsupported_model: "当前设备型号不支持此功能", writes_disabled: "只读模式：启动服务时需启用 --allow-writes",
      transport_unknown: "底层传输未知，请重新读取设备状态", transport_restricted: "此操作要求直连串口，BLE / TCP 不允许",
      usb_bridge_required: "常规 USB 操作请使用 Python 桥；当前原生连接仅用于维护",
      bridge_required: "请配置对应的受管设备桥", no_entry: "此端没有已支持的专用入口",
      parameters_required: "需选择具体参数或外部运行环境后判断", invalid_parameters: "请检查输入参数"
    };
    if (reason.code === "missing_commands") return "固件未公布所需命令：" + (reason.commands || []).join("、");
    return (messages[reason.code] || reason.message || "可用性未知") + (reason.code === "invalid_parameters" ? "：" + reason.message : "");
  };
  class FeatureAvailability {
    constructor({ api, changed, baseDisabled, writeAllowed }) {
      Object.assign(this, { api, changed, baseDisabled, writeAllowed });
      this.catalog = []; this.states = new Map(); this.device = null; this.epoch = 0; this.hints = new WeakMap(); this.hintID = 0;
    }
    setStates(version, states, device) {
      if (!this.catalog.length) { this.clear("请先重试读取权限，获取有效功能清单"); return false; }
      if (version !== 1 || !Array.isArray(states) || states.some(s => !s || typeof s.id !== "string" || !["available", "unavailable", "unknown"].includes(s.state) || !Array.isArray(s.reasons) || s.reasons.some(r => !r || typeof r.message !== "string"))) {
        this.clear("功能信息格式不受支持，请重试读取权限与设备状态"); return false;
      }
      this.epoch++; this.message = ""; this.states = new Map(states.map(s => [s.id, s])); this.device = device;
      this.render(); this.changed(); this.checkCard(); document.dispatchEvent(new Event("desk-feature-device")); return true;
    }
    configure(caps) {
      const catalog = caps.feature_catalog;
      const strings = value => Array.isArray(value) && value.every(v => typeof v === "string");
      const commands = value => Array.isArray(value) && value.every(v => Number.isInteger(v) && v >= 0 && v <= 65535);
      const valid = f => f && typeof f.id === "string" && typeof f.title === "string" && strings(f.models) && strings(f.notes) && commands(f.commands) && Array.isArray(f.conditional) && f.conditional.every(c => c && typeof c.when === "string" && commands(c.commands)) && [f.web,f.cli,f.sdk].every(e => e && Object.hasOwn(labels,e.status) && strings(e.entries)) && (f.web.controls == null || strings(f.web.controls));
      if (caps.feature_schema_version !== 1 || !Array.isArray(catalog) || !catalog.length || catalog.some(f => !valid(f)) || new Set(catalog.map(f=>f.id)).size !== catalog.length) {
        this.catalog = []; this.states = new Map(); this.clear("无法读取功能清单；请重试读取权限"); return false;
      }
      try { for(const f of catalog) for(const selector of f.web.controls || []) document.querySelectorAll(selector); }
      catch (_) { this.catalog = []; this.states = new Map(); this.clear("功能控件定义无效，请重试读取权限"); return false; }
      this.catalog = catalog;
      this.bridgePolicy = caps.usb_bridge_enabled === true ? "managed-usb" : "managed-ble";
      this.nativeMaintenance = caps.native_usb_maintenance_enabled === true;
      return this.setStates(caps.feature_schema_version, caps.feature_states, caps.feature_device || null);
    }
    observe(info) {
      const device = Number.isInteger(info.model_id) && Array.isArray(info.capabilities) && info.transport ? { model_id: info.model_id, capabilities: info.capabilities, transport: info.transport } : null;
      if (!device) { this.clear("设备能力信息不完整，请刷新功能状态"); return; }
      this.setStates(info.feature_schema_version, info.feature_states, device);
    }
    clear(message = "连接状态已变化，请重新读取设备能力") {
      this.epoch++; this.device = null;
      for (const f of this.catalog) if (f.device || f.requirement_mode === "card-plan") {
        // Connection identity changes invalidate firmware facts, not the
        // configured native-maintenance boundary supplied by the server.
        const policy = this.nativeMaintenance ? this.states.get(f.id)?.reasons.filter(r => r.code === "usb_bridge_required") || [] : [];
        this.states.set(f.id, { id: f.id, state: policy.length ? "unavailable" : "unknown", reasons: policy.length ? policy : [{ code: "device_unknown", message }], required_commands: [] });
      }
      this.message = message; this.render(); this.changed(); document.dispatchEvent(new Event("desk-feature-device"));
    }
    state(id) {
      return this.states.get(id) || { id, state: "unknown", reasons: [{ code: "device_unknown", message: this.message || "请先读取功能清单和设备状态" }] };
    }
    apply() {
      const defaults = [
        {id:"reader.hf-scan",web:{controls:["#scan-hf"]}}, {id:"reader.lf-scan",web:{controls:["#scan-lf"]}},
        {id:"slot.load-card",write:true,web:{controls:["#load-card-go"]}},
        {id:"device.settings",web:{controls:["#settings-read"]}}, {id:"slot.read-details",web:{controls:["#slot-edit-read"]}},
        {id:"device.configure",write:true,web:{controls:["#settings-apply"]}}, {id:"slot.configure",write:true,web:{controls:["#slot-edit-apply"]}}
      ];
      const fallback = this.catalog.map(f=>({...f,web:{...f.web,controls:[...new Set([...(f.web.controls||[]),...(defaults.find(d=>d.id===f.id)?.web.controls||[])])]}}));
      fallback.push(...defaults.filter(d=>!this.catalog.some(f=>f.id===d.id)));

      for (const f of fallback) {
        // Both bridge workflows use the same lifecycle panel. Only the configured
        // medium controls its buttons; the other medium stays in the inventory.
        if (f.requirement_mode === "bridge" && f.transport !== this.bridgePolicy) continue;
        let state = this.state(f.id);
        if (f.write && !this.writeAllowed()) state = { ...state, state:"unavailable", reasons:[{ code:"writes_disabled" }, ...state.reasons.filter(r=>r.code!=="writes_disabled")] };
        const bootstrap = ["device.info", "device.features"].includes(f.id);
        const denied = state.state !== "available" && !(f.id === "device.features" || (bootstrap && state.state === "unknown"));
        for (const selector of f.web.controls || []) for (const button of document.querySelectorAll(selector)) {
          const base = this.baseDisabled(button, f);
          button.disabled = base || denied;
          let hint = this.hints.get(button);
          if (!hint) {
            hint = document.createElement("span"); hint.className = "feature-disabled-reason"; hint.id = "feature-reason-" + ++this.hintID;
            button.after(hint); this.hints.set(button, hint);
            button.setAttribute("aria-describedby", [button.getAttribute("aria-describedby"), hint.id].filter(Boolean).join(" "));
          }
          hint.hidden = !denied;
          hint.textContent = denied ? state.reasons.map(reasonText).join("；") || "可用性未知" : "";
          if (denied) button.title = hint.textContent; else button.removeAttribute("title");
        }
      }
    }
    render() {
      const list = document.querySelector("#feature-list"); list.replaceChildren();
      document.querySelector("#feature-summary").textContent = this.device ? "设备能力快照 · 重新连接后请刷新；实际操作仍会再次校验" : this.message || "设备能力尚未读取；离线工具不依赖固件命令";
      for (const f of this.catalog) {
        const state = this.state(f.id), item = document.createElement("details"), title = document.createElement("summary");
        title.textContent = f.title + " · " + ({ available: "可用", unavailable: "不可用", unknown: "待确认" }[state.state]); item.append(title);
        for (const [surface, entry] of [["Web UI", f.web], ["CLI", f.cli], ["SDK", f.sdk]]) {
          const p = document.createElement("p"); p.textContent = surface + "：" + (labels[entry.status] || entry.status) + " · " + entry.entries.join(" / "); item.append(p);
        }
        const p = document.createElement("p"); p.textContent = "型号：" + f.models.join(" / ") + "；传输：" + f.transport + "；固定命令：" + f.commands.join(", "); item.append(p);
        for (const c of f.conditional) { const p = document.createElement("p"); p.textContent = c.when + "：" + c.commands.join(", "); item.append(p); }
        for (const text of [...state.reasons.map(reasonText), ...f.notes]) { const p = document.createElement("p"); p.textContent = text; item.append(p); }
        list.append(item);
      }
    }
    checkCard() {
      const epoch = ++this.epoch, read = id => document.querySelector(id), prior = this.state("slot.load-card");
      const publish = (state) => { if (epoch !== this.epoch) return; this.states.set("slot.load-card", state); this.render(); this.changed(); };
      const invalid = (message) => publish({ id: "slot.load-card", state: "unavailable", reasons: [{ code: "invalid_parameters", message }], required_commands: [] });
      if (!this.device || !this.writeAllowed() || prior.reasons.some(r => r.code === "writes_disabled")) return;
      let card; try { card = JSON.parse(read("#load-card-json").value); } catch (_) { invalid("卡片不是有效 JSON，请导入卡片文件"); return; }
      const slot = Number(read("#load-slot").value);
      if (!Number.isInteger(slot) || slot < 1 || slot > 8) { invalid("槽位必须是 1 至 8 的整数"); return; }
      publish({ id: "slot.load-card", state: "unknown", reasons: [{ message: "正在检查所选卡片的命令要求" }], required_commands: [] });
      window.featureCheckReady = this.api("/api/features/check", { id: "slot.load-card", device: this.device, card, slot, persist: read("#load-persist").checked, activate: read("#load-activate").checked })
        .then(response => { if (response.schema_version !== 1 || response.state?.id !== "slot.load-card" || !["available","unavailable","unknown"].includes(response.state.state) || !Array.isArray(response.state.reasons)) throw new Error("功能检查结果格式无效"); publish(response.state); })
        .catch(error => publish({ id: "slot.load-card", state: "unknown", reasons: [{ message: "无法检查卡片能力：" + error.message }], required_commands: [] }));
    }
  }
  window.DeskFeatureAvailability = FeatureAvailability;
})();
