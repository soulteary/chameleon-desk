/* Browser-local library snapshots. No device access or external service. */
(() => {
  "use strict";
  const databaseName = "chameleon-desk-library", storeName = "snapshots";
  const conflict = () => Object.assign(new Error("另一个页面已更新卡库；请先导出本页草稿，再重新读取已保存版本"), { name: "LibraryConflictError" });
  function sameRecord(a, b) {
    if (a === b) return true;
    // Normal snapshots are strings. This also permits deliberate recovery
    // when a damaged record was stored with another structured-clone type.
    if (!a || !b || typeof a !== "object" || typeof b !== "object") return false;
    try { return JSON.stringify(a) === JSON.stringify(b); } catch (_) { return false; }
  }
  function decode(raw) {
    const record = JSON.parse(raw);
    if (!record || record.format !== 1 || typeof record.text !== "string" || typeof record.revision !== "string" || !record.revision || typeof record.savedAt !== "string" || !Number.isFinite(Date.parse(record.savedAt))) {
      throw new Error("保存记录版本不受支持或已损坏；原记录未被覆盖");
    }
    return record;
  }
  function storageError(error) {
    if (error?.name === "QuotaExceededError") return "浏览器存储空间不足；本页内容与原保存版本保留，请导出备份后重试";
    return error?.message || "浏览器保存失败；本页内容与原保存版本保留";
  }
  class LibraryStore {
    async open() {
      if (!window.indexedDB) throw new Error("浏览器本地存储不可用；请导出文件保存");
      return new Promise((resolve, reject) => {
        const request = window.indexedDB.open(databaseName, 1);
        let refused = false;
        request.onupgradeneeded = () => request.result.createObjectStore(storeName);
        request.onblocked = () => { refused = true; reject(new Error("本地存储升级被其他页面阻止；请关闭其他 Desk 页面后重新读取")); };
        request.onerror = () => reject(request.error);
        request.onsuccess = () => {
          if (refused) { request.result.close(); return; }
          request.result.onversionchange = () => request.result.close();
          resolve(request.result);
        };
      });
    }
    async read() {
      const db = await this.open();
      try {
        return await new Promise((resolve, reject) => {
          const tx = db.transaction(storeName, "readonly"), store = tx.objectStore(storeName), result = {};
          for (const key of ["current", "previous"]) store.get(key).onsuccess = (event) => { result[key] = event.target.result; };
          tx.oncomplete = () => resolve(result);
          tx.onabort = () => reject(tx.error || new Error("读取本地卡库失败"));
        });
      } finally { db.close(); }
    }
    async write(expected, text, keepPrevious = false) {
      const db = await this.open();
      try {
        const next = JSON.stringify({ format: 1, revision: window.crypto.randomUUID(), savedAt: new Date().toISOString(), text });
        return await new Promise((resolve, reject) => {
          const tx = db.transaction(storeName, "readwrite", { durability: "strict" }), store = tx.objectStore(storeName);
          let failure;
          store.get("current").onsuccess = (event) => {
            if (!sameRecord(event.target.result, expected)) { failure = conflict(); tx.abort(); return; }
            if (expected !== undefined && !keepPrevious) store.put(expected, "previous");
            store.put(next, "current");
          };
          // Request success alone is not a durability acknowledgement.
          tx.oncomplete = () => resolve(next);
          tx.onabort = () => reject(failure || tx.error || new Error("本地保存事务未完成"));
        });
      } finally { db.close(); }
    }
  }

  class LibraryPersistence {
    constructor({ getText, setText, validate, render, store = new LibraryStore(), delay = 600 }) {
      Object.assign(this, { getText, setText, validate, render, store, delay });
      this.dirty = false; this.blocked = true; this.loaded = false; this.loading = false;
      this.previous = false; this.generation = 0; this.base = undefined;
      this.timer = null; this.saving = null; this.again = false;
    }
    status(message, error = false) {
      this.render({ message, error, dirty: this.dirty, blocked: this.blocked, loading: this.loading, saving: !!this.saving, previous: this.previous });
    }
    cancelTimer() { clearTimeout(this.timer); this.timer = null; }
    async checked(text) {
      let value;
      try { value = JSON.parse(text); } catch (_) { throw new Error("数据库 JSON 尚未完整；未覆盖已保存版本，请修正或导出本页草稿"); }
      if (!value || typeof value !== "object" || Array.isArray(value)) throw new Error("数据库必须是 JSON 对象；已保存版本未改变");
      await this.validate(value);
    }
    async load() {
      this.cancelTimer();
      if (this.saving) await this.saving;
      const generation = this.generation;
      this.loading = true; this.status("正在读取浏览器保存的卡库…");
      try {
        const records = await this.store.read();
        this.base = records.current; this.loaded = true;
        this.previous = records.previous !== undefined;
        let text = "{}", savedAt;
        if (records.current !== undefined) {
          const record = decode(records.current); text = record.text; savedAt = record.savedAt;
          await this.checked(text);
        }
        if (this.generation !== generation) throw new Error("读取期间本页输入已变化；请先导出草稿，再重新读取");
        this.setText(text); this.dirty = false; this.blocked = false;
        this.message = savedAt ? "已恢复本地卡库 · 保存于 " + new Date(savedAt).toLocaleString() : "尚无本地卡库 · 修改后自动保存到当前浏览器和地址";
        this.failed = false;
      } catch (error) {
        this.blocked = true; this.message = "无法恢复本地卡库：" + storageError(error); this.failed = true;
      } finally { this.loading = false; this.status(this.message, this.failed); }
    }
    changed() {
      this.generation++; this.dirty = true;
      this.cancelTimer();
      if (this.blocked || this.loading) { this.status("本页有未保存更改；请先解决本地存储读取问题，或导出本页草稿", true); return; }
      this.status("有未保存更改 · 即将自动保存");
      this.timer = setTimeout(() => { this.timer = null; this.save(); }, this.delay);
    }
    save() {
      this.cancelTimer();
      if (this.saving) { this.again = true; return this.saving; }
      if (!this.dirty) return Promise.resolve();
      if (this.blocked || !this.loaded || this.loading) { this.status("尚未读取可靠的保存版本；请导出本页草稿后重新读取", true); return Promise.resolve(); }
      const generation = this.generation, text = this.getText();
      this.saving = Promise.resolve().then(async () => {
        this.message = "正在校验并保存本地卡库…"; this.failed = false; this.status(this.message);
        try {
          await this.checked(text);
          if (generation !== this.generation) { this.again = true; this.message = "输入已变化 · 等待保存最新内容"; return; }
          const old = this.base;
          this.base = await this.store.write(old, text);
          this.previous = old !== undefined || this.previous;
          if (generation === this.generation && text === this.getText()) {
            this.dirty = false; this.message = "已自动保存到当前浏览器 · " + new Date(decode(this.base).savedAt).toLocaleString(); this.failed = false;
          } else {
            this.again = true; this.message = "保存期间输入已变化 · 仍有未保存更改";
          }
        } catch (error) {
          if (error.name === "LibraryConflictError") this.blocked = true;
          this.message = "未保存：" + storageError(error); this.failed = true;
        }
      }).finally(() => {
        this.saving = null; this.status(this.message || "有未保存更改", this.failed);
        if (this.again && this.dirty && !this.blocked) { this.again = false; this.cancelTimer(); this.timer = setTimeout(() => { this.timer = null; this.save(); }, this.delay); }
        else this.again = false;
      });
      return this.saving;
    }
    async restorePrevious() {
      this.cancelTimer();
      if (this.saving) await this.saving;
      const generation = this.generation;
      this.loading = true; this.status("正在校验上一保存版本…");
      try {
        const records = await this.store.read();
        if (!sameRecord(records.current, this.base)) throw conflict();
        if (records.previous === undefined) throw new Error("没有可恢复的上一保存版本");
        const record = decode(records.previous);
        await this.checked(record.text);
        if (generation !== this.generation) throw new Error("恢复期间本页输入已变化；未替换任何内容");
        this.base = await this.store.write(records.current, record.text, true);
        // Preserve edits made while the final IndexedDB transaction completed.
        if (generation === this.generation) { this.setText(record.text); this.dirty = false; }
        this.blocked = false; this.loaded = true; this.previous = true;
        this.message = this.dirty ? "上一版本已恢复到本地存储；本页仍有未保存更改" : "已恢复上一保存版本"; this.failed = false;
      } catch (error) { if (error.name === "LibraryConflictError") this.blocked = true; this.message = "恢复未完成：" + storageError(error); this.failed = true; }
      finally { this.loading = false; this.status(this.message, this.failed); }
    }
  }
  window.DeskLibraryStore = LibraryStore;
  window.DeskLibraryPersistence = LibraryPersistence;
})();
