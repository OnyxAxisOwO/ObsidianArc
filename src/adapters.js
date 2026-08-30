// Default environment adapters for running standalone in a plain browser tab.
//
// Everything else in this project (store.js, provider.js, chat.js) talks to
// storage through the same tiny shape chrome.storage.local already has:
// async get(key)/set(obj), plus an onChanged subscription. That shape was
// chosen deliberately — a host embedding this in a browser extension can
// pass chrome.storage.local straight through with no adapter at all. A plain
// web page has no such API, so this file provides the one substitute the
// standalone build needs: a JSON-over-localStorage shim.

(function (root, factory) {
  const api = factory();
  if (typeof module === 'object' && module.exports) module.exports = api;
  if (root) root.ObsidianAdapters = api;
})(typeof globalThis !== 'undefined' ? globalThis : this, function () {
  'use strict';

  function localStorageAdapter(prefix) {
    const ns = typeof prefix === 'string' ? prefix : 'obsidian-arc:';
    const listeners = [];

    function readRaw(key) {
      try {
        const raw = localStorage.getItem(ns + key);
        return raw === null ? undefined : JSON.parse(raw);
      } catch (_) {
        // Corrupt value, or storage disabled in this browsing context (a
        // private window with site data blocked): read back as "not set"
        // rather than throwing, same as an extension's storage would on a
        // key nobody ever wrote.
        return undefined;
      }
    }

    function notify(changes) {
      listeners.forEach((callback) => {
        try {
          callback(changes, 'local');
        } catch (_) {
          // A listener's own bug must not break every other listener, or the
          // write that triggered it.
        }
      });
    }

    async function get(keys) {
      const list = typeof keys === 'string' ? [keys] : (Array.isArray(keys) ? keys : Object.keys(keys || {}));
      const result = {};
      list.forEach((key) => {
        const value = readRaw(key);
        if (value !== undefined) result[key] = value;
      });
      return result;
    }

    async function set(values) {
      const changes = {};
      Object.keys(values || {}).forEach((key) => {
        changes[key] = { oldValue: readRaw(key), newValue: values[key] };
        try {
          localStorage.setItem(ns + key, JSON.stringify(values[key]));
        } catch (_) {
          // Quota exceeded or storage disabled: the in-memory caller still
          // gets its optimistic value back, it just will not survive reload.
        }
      });
      notify(changes);
    }

    function onChanged(callback) {
      listeners.push(callback);
    }

    // Same-tab writes are delivered synchronously above (through set()); the
    // native `storage` event is what makes a second open tab notice a change
    // made in this one — the browser never fires it on the tab that made the
    // write.
    if (typeof window !== 'undefined' && typeof window.addEventListener === 'function') {
      window.addEventListener('storage', (event) => {
        if (!event.key || event.key.indexOf(ns) !== 0) return;
        const key = event.key.slice(ns.length);
        let newValue;
        let oldValue;
        try { newValue = event.newValue === null ? undefined : JSON.parse(event.newValue); } catch (_) { newValue = undefined; }
        try { oldValue = event.oldValue === null ? undefined : JSON.parse(event.oldValue); } catch (_) { oldValue = undefined; }
        notify({ [key]: { oldValue, newValue } });
      });
    }

    return { get, set, onChanged };
  }

  return { localStorageAdapter };
});
