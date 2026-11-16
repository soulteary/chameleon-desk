"use strict";
// Only window geometry is kept in memory; card data is never persisted here.
(() => {
  let z = 10;
  const restoreRects = new WeakMap();
  const viewportRects = new WeakMap();
  const rectangle = (win) => ({ left: win.style.left, top: win.style.top, width: win.style.width, height: win.style.height });
  const lastFocus = new WeakMap();
  const windows = [...document.querySelectorAll(".app-window")];
  const desktopArea = document.querySelector("#desktop");
  function desktopBounds() {
    const area = desktopArea.getBoundingClientRect();
    return { width: area.width, height: Math.max(0, document.querySelector(".dock").getBoundingClientRect().top - area.top - 10) };
  }
  function clampWindow(win) {
    if (win.hidden) return;
    const bounds = desktopBounds();
    const maximized = win.classList.contains("maximized");
    const fitted = window.fitChameleonWindow(bounds, { width: maximized || innerWidth <= 600 ? bounds.width : win.offsetWidth, height: maximized ? bounds.height : win.offsetHeight, left: maximized ? 12 : win.offsetLeft, top: maximized ? 12 : win.offsetTop });
    win.style.width = fitted.width + "px";
    win.style.height = fitted.height + "px";
    win.style.left = fitted.left + "px";
    win.style.top = fitted.top + "px";
  }
  function restoreViewport(win) {
    if (win.hidden) return;
    const saved = viewportRects.get(win);
    if (saved && !win.hidden && !win.classList.contains("maximized")) Object.assign(win.style, saved);
    clampWindow(win);
    if (saved && Object.entries(saved).every(([key, value]) => win.style[key] === value)) viewportRects.delete(win);
  }
  const windowObserver = new ResizeObserver((entries) => requestAnimationFrame(() => entries.forEach(({target}) => clampWindow(target))));
  windows.forEach((win) => windowObserver.observe(win));
  const dock = (id) => document.querySelector(`.dock [data-open="${id}"]`);
  const focus = (win) => {
    windows.forEach((w) => w.classList.toggle("front", w === win));
    // Keep windows below the menu and Dock, even after many focus changes.
    if (++z > 100) {
      windows.slice().sort((a, b) => Number(a.style.zIndex || 0) - Number(b.style.zIndex || 0)).forEach((w, i) => { w.style.zIndex = 10 + i; });
      z = 10 + windows.length;
    }
    win.style.zIndex = z;
    document.querySelector("#active-app").textContent = win.dataset.title;
  };
  function focusControl(win) {
    const previous = lastFocus.get(win);
    const available = (control) => {
      if (!control?.isConnected || control.disabled || control.closest("[hidden]")) return false;
      for (let parent=control.parentElement; parent && parent!==win; parent=parent.parentElement) {
        if (parent.tagName==="DETAILS" && !parent.open && !parent.querySelector(":scope > summary")?.contains(control)) return false;
      }
      return true;
    };
    const target = available(previous) ? previous : [...win.querySelectorAll(".window-content button, .window-content input, .window-content textarea, .window-content select")].find(available) || win.querySelector(".traffic button");
    target?.focus({ preventScroll: true });
  }
  function maximize(win) {
    if (win.classList.contains("maximized")) {
      win.classList.remove("maximized");
      const saved = restoreRects.get(win);
      if (saved) viewportRects.set(win, saved);
      restoreViewport(win);
    } else {
      restoreRects.set(win, viewportRects.get(win) || rectangle(win));
      viewportRects.delete(win);
      win.classList.add("maximized");
      clampWindow(win);
    }
  }
  function open(id) {
    const win = windows.find((w) => w.id === id);
    if (!win) return;
    win.hidden = false; win.dataset.minimized = "false";
    dock(id)?.classList.add("running"); restoreViewport(win); focus(win);
    focusControl(win);
    document.querySelector("#applications-menu").hidden = true;
    document.querySelector("#applications-button").setAttribute("aria-expanded", "false");
  }
  function hide(win, close) {
    win.hidden = true;
    win.dataset.minimized = String(!close);
    if (close) dock(win.id)?.classList.remove("running");
    const next = windows.filter((w) => !w.hidden).sort((a, b) => Number(b.style.zIndex || 0) - Number(a.style.zIndex || 0))[0];
    if (next) { focus(next); focusControl(next); }
    else { document.querySelector("#active-app").textContent = "Chameleon Desk"; dock(win.id)?.focus({ preventScroll: true }); }
  }
  document.querySelectorAll("[data-open]").forEach((b) => b.addEventListener("click", () => open(b.dataset.open)));
  windows.forEach((win) => {
    win.addEventListener("pointerdown", (e) => { if (e.target === win) viewportRects.delete(win); focus(win); });
    win.addEventListener("focusin", (e) => { if (!e.target.closest(".traffic")) lastFocus.set(win, e.target); focus(win); });
    win.querySelectorAll("[data-window]").forEach((b) => b.addEventListener("click", () => {
      if (b.dataset.window === "maximize") maximize(win);
      else hide(win, b.dataset.window === "close");
    }));
    const bar = win.querySelector(".titlebar");
    bar.addEventListener("dblclick", (e) => { if (!e.target.closest("button")) maximize(win); });
    bar.addEventListener("pointerdown", (e) => {
      if (e.target.closest("button") || win.classList.contains("maximized") || innerWidth < 950 || e.button !== 0) return;
      viewportRects.delete(win);
      const x = e.clientX, y = e.clientY, left = win.offsetLeft, top = win.offsetTop;
      bar.setPointerCapture(e.pointerId);
      const move = (m) => {
        win.style.left = (left + m.clientX - x) + "px";
        win.style.top = (top + m.clientY - y) + "px";
        clampWindow(win);
      };
      const done = () => { bar.removeEventListener("pointermove", move); bar.removeEventListener("pointerup", done); bar.removeEventListener("pointercancel", done); bar.removeEventListener("lostpointercapture", done); if (bar.hasPointerCapture(e.pointerId)) bar.releasePointerCapture(e.pointerId); };
      bar.addEventListener("pointermove", move); bar.addEventListener("pointerup", done); bar.addEventListener("pointercancel", done); bar.addEventListener("lostpointercapture", done);
    });
  });
  const desktop = () => { windows.forEach((w) => { if (!w.hidden) { w.dataset.minimized = "true"; w.hidden = true; } }); document.querySelector("#active-app").textContent = "Chameleon Desk"; document.querySelector("#applications-button").focus({ preventScroll: true }); };
  document.querySelector("#desktop-button").addEventListener("click", desktop);
  document.querySelector("#dock-desktop").addEventListener("click", desktop);
  document.querySelector("#arrange-windows").addEventListener("click", () => {
    const visible = windows.filter((w) => !w.hidden).sort((a, b) => Number(a.style.zIndex || 0) - Number(b.style.zIndex || 0));
    const bounds = desktopBounds(), count = Math.max(1, visible.length - 1);
    const dx = Math.min(25, Math.max(0, (bounds.width - 332) / count)), dy = Math.min(24, Math.max(0, (bounds.height - 220) / count));
    visible.forEach((w, i) => {
      viewportRects.delete(w);
      w.classList.remove("maximized"); w.style.width = ""; w.style.height = "";
      // Leave room for the whole cascade, rather than clamping several titles
      // onto the same row where later windows cover their traffic controls.
      w.style.width = Math.max(280, Math.min(w.offsetWidth, bounds.width - 52 - dx * (visible.length - 1))) + "px";
      w.style.height = Math.max(180, Math.min(w.offsetHeight, bounds.height - 40 - dy * (visible.length - 1))) + "px";
      w.style.left = (40 + i * dx) + "px"; w.style.top = (28 + i * dy) + "px"; clampWindow(w);
    });
  });
  const menuButton = document.querySelector("#applications-button"), menu = document.querySelector("#applications-menu");
  menuButton.addEventListener("click", () => { menu.hidden = !menu.hidden; menuButton.setAttribute("aria-expanded", String(!menu.hidden)); });
  document.addEventListener("click", (e) => { if (!e.target.closest(".menu")) { menu.hidden = true; menuButton.setAttribute("aria-expanded", "false"); } });
  document.addEventListener("keydown", (e) => { if (e.key === "Escape") { menu.hidden = true; menuButton.setAttribute("aria-expanded", "false"); } });
  document.querySelector("#about-button").addEventListener("click", () => document.querySelector("#about-dialog").showModal());
  const tick = () => { document.querySelector("#clock").textContent = new Intl.DateTimeFormat("zh-CN", { month: "numeric", day: "numeric", hour: "2-digit", minute: "2-digit", hour12: false }).format(new Date()); };
  tick(); setInterval(tick, 30000);
  const importRequests = new WeakMap(), inputRevisions = new WeakMap();
  document.querySelectorAll("[data-import]").forEach((b) => {
    const target = document.getElementById(b.dataset.import);
    if (!inputRevisions.has(target)) {
      inputRevisions.set(target, 0);
      target.addEventListener("input", () => inputRevisions.set(target, inputRevisions.get(target) + 1));
    }
    b.addEventListener("click", () => {
      const original = target.value;
      const request = {}, revision = inputRevisions.get(target);
      importRequests.set(target, request);
      const input = document.createElement("input"); input.type = "file"; input.accept = ".json,application/json";
      input.addEventListener("change", async () => {
        const f = input.files[0]; if (!f) return;
        try {
          if (f.size > (b.dataset.import === "db-json" ? 32 : 4) * 1024 * 1024) throw new Error("文件超过大小限制");
          const text = await f.text();
          if (importRequests.get(target) !== request) return;
          const value = JSON.parse(text);
          if (!value || typeof value !== "object" || Array.isArray(value)) throw new Error("请选择 JSON 对象文件");
          if (b.disabled || target.disabled || inputRevisions.get(target) !== revision || target.value !== original) throw new Error("工作区正在处理或输入已变化，请重新导入");
          if (target.id === "db-json") {
            const replace = new Event("desk:before-library-replace", { cancelable: true });
            target.dispatchEvent(replace); if (replace.defaultPrevented) return;
          }
          target.value = text; target.dispatchEvent(new Event("input", { bubbles: true }));
          const details = target.closest("details"); if (details && target.id === "db-json") details.open = true;
          window.desktopToast("已导入 " + f.name);
        } catch (e) { if (importRequests.get(target) === request) window.desktopToast("导入失败：" + e.message); }
      }); input.click();
    });
  });
  document.querySelectorAll("[data-export]").forEach((b) => b.addEventListener("click", () => {
    try {
      const result = document.getElementById(b.dataset.export);
      if (b.dataset.export !== "db-out" && result.dataset.ready !== "true") throw new Error("请先完成操作");
      const database = b.dataset.export === "db-out" && !b.hasAttribute("data-export-card");
      if (!database && result.dataset.ready !== "true") throw new Error("请先完成操作");
      const content = database ? document.querySelector("#db-json").value : result.dataset.exportJson || result.textContent;
      const parsed = JSON.parse(content);
      if (!parsed || typeof parsed !== "object") throw new Error("请先完成操作");
      const metadata = database || (Number.isInteger(parsed.tag) && Array.isArray(parsed.data)) || (typeof parsed.id === "string" && Array.isArray(parsed.keys));
      if (!metadata && parsed.error) throw new Error("请先完成操作");
      if (database && Array.isArray(parsed)) throw new Error("数据库必须是 JSON 对象");
      if (b.hasAttribute("data-export-card") && (!Number.isInteger(parsed.tag) || !Array.isArray(parsed.data))) throw new Error("请先查看一张卡片");
      const url = URL.createObjectURL(new Blob([JSON.stringify(parsed, null, 2)], { type: "application/json" }));
      const a = document.createElement("a"); a.href = url; a.download = "chameleon-desk-" + b.dataset.export + ".json"; a.click(); setTimeout(() => URL.revokeObjectURL(url), 1000);
    } catch (_) { window.desktopToast("还没有可导出的成功结果"); }
  }));
  let toastTimer;
  window.desktopToast = (message) => { const t = document.querySelector("#toast"); t.textContent = message; t.hidden = false; clearTimeout(toastTimer); toastTimer = setTimeout(() => { t.hidden = true; }, 4500); };
  window.addEventListener("resize", () => windows.forEach((win) => {
    if (!win.classList.contains("maximized") && !viewportRects.has(win) && win.style.width && win.style.height) viewportRects.set(win, rectangle(win));
    restoreViewport(win);
  }));
  windows.forEach(clampWindow);
  focus(document.querySelector("#device"));
})();
