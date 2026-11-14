"use strict";
// A window fits entirely between the menu bar and Dock, even after cascading,
// dragging, resizing or restoring on a smaller viewport.
(function (root) {
  function fitWindow(bounds, rect) {
    const mx = Math.min(12, bounds.width / 2), my = Math.min(12, bounds.height / 2);
    const width = Math.max(0, Math.min(Math.max(560, rect.width), bounds.width - mx * 2));
    const height = Math.max(0, Math.min(Math.max(180, rect.height), bounds.height - my * 2));
    return { width, height,
      left: Math.max(mx, Math.min(rect.left, bounds.width - width - mx)),
      top: Math.max(my, Math.min(rect.top, bounds.height - height - my)) };
  }
  if (typeof module !== "undefined" && module.exports) module.exports = fitWindow;
  else root.fitChameleonWindow = fitWindow;
})(typeof window !== "undefined" ? window : globalThis);
