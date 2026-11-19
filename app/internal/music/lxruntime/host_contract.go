//go:build lxquickjs && cgo

package lxruntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

// ScriptInfo mirrors the metadata exposed by the LX desktop preload bridge.
type ScriptInfo struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Version     string `json:"version"`
	Author      string `json:"author"`
	Homepage    string `json:"homepage"`
	RawScript   string `json:"rawScript"`
}

// InstallLXHost installs the verified, synchronous portion of the LX desktop
// host contract into this persistent QuickJS context. Network and binary
// utility operations deliberately fail closed until their native host
// implementations are installed; they are never replaced with fake results.
func (r *Runtime) InstallLXHost(ctx context.Context, info ScriptInfo) error {
	if r == nil {
		return errors.New("QuickJS runtime is nil")
	}
	metadata, err := json.Marshal(info)
	if err != nil {
		return fmt.Errorf("encode LX script metadata: %w", err)
	}
	bootstrap := lxHostBootstrap + "\n;globalThis.lx.currentScriptInfo = " + string(metadata) + ";\n" + lxBrowserBootstrap
	if _, err := r.Eval(ctx, bootstrap, "lx-host-bootstrap.js"); err != nil {
		return fmt.Errorf("install LX host contract: %w", err)
	}
	if err := r.installLXCryptoNative(ctx); err != nil {
		return fmt.Errorf("install LX native crypto: %w", err)
	}
	return nil
}

const lxHostBootstrap = `
(() => {
  if (!globalThis.console) {
    const noop = () => {};
    globalThis.console = { log: noop, info: noop, warn: noop, error: noop, debug: noop };
  }
  const EVENT_NAMES = Object.freeze({
    request: 'request',
    inited: 'inited',
    updateAlert: 'updateAlert'
  });
  const eventNames = Object.values(EVENT_NAMES);
  const allSources = ['kw', 'kg', 'tx', 'wy', 'mg', 'local'];
  const supportQualitys = {
    kw: ['128k', '320k', 'flac', 'flac24bit'],
    kg: ['128k', '320k', 'flac', 'flac24bit'],
    tx: ['128k', '320k', 'flac', 'flac24bit'],
    wy: ['128k', '320k', 'flac', 'flac24bit'],
    mg: ['128k', '320k', 'flac', 'flac24bit'],
    local: []
  };
  const supportActions = {
    kw: ['musicUrl'],
    kg: ['musicUrl'],
    tx: ['musicUrl'],
    wy: ['musicUrl'],
    mg: ['musicUrl'],
    local: ['musicUrl', 'lyric', 'pic']
  };
  const handlers = Object.create(null);
  const pendingRequests = new Map();
  const requestQueue = [];
  let nextRequestID = 1;
  const state = { initialized: false, initInfo: null, updateAlertShown: false };
  const unavailable = name => function() {
    throw new Error('LX host capability is not implemented: ' + name);
  };
  const lx = {
    EVENT_NAMES,
    version: '2.0.0',
    env: 'desktop',
    currentScriptInfo: {},
    on(eventName, handler) {
      if (!eventNames.includes(eventName)) {
        return Promise.reject(new Error('The event is not supported: ' + eventName));
      }
      if (eventName !== EVENT_NAMES.request || typeof handler !== 'function') {
        return Promise.reject(new Error('The event is not supported or handler is invalid'));
      }
      handlers.request = handler;
      return Promise.resolve();
    },
    send(eventName, data) {
      return new Promise((resolve, reject) => {
        if (!eventNames.includes(eventName)) {
          reject(new Error('The event is not supported: ' + eventName));
          return;
        }
        if (eventName === EVENT_NAMES.inited) {
          if (state.initialized) {
            reject(new Error('Script is inited'));
            return;
          }
          if (!data || typeof data !== 'object' || !data.sources || typeof data.sources !== 'object') {
            reject(new Error('Missing required parameter init info'));
            return;
          }
          const sources = {};
          try {
            for (const source of allSources) {
              const userSource = data.sources[source];
              if (!userSource || userSource.type !== 'music') continue;
              if (!Array.isArray(userSource.actions) || !Array.isArray(userSource.qualitys)) {
                throw new Error('Invalid source capabilities: ' + source);
              }
              sources[source] = {
                type: 'music',
                actions: supportActions[source].filter(action => userSource.actions.includes(action)),
                qualitys: supportQualitys[source].filter(quality => userSource.qualitys.includes(quality))
              };
            }
          } catch (error) {
            reject(error);
            return;
          }
          state.initialized = true;
          state.initInfo = { sources };
          resolve();
          return;
        }
        if (eventName === EVENT_NAMES.updateAlert) {
          if (state.updateAlertShown) {
            reject(new Error('The update alert can only be called once.'));
            return;
          }
          if (!data || typeof data !== 'object' || typeof data.log !== 'string' || !data.log) {
            reject(new Error('log is required.'));
            return;
          }
          if (data.log.length > 1024) data.log = data.log.substring(0, 1024) + '...';
          if (data.updateUrl && (typeof data.updateUrl !== 'string' ||
              !/^https?:\/\/[^\s$.?#].[^\s]*$/.test(data.updateUrl))) {
            delete data.updateUrl;
          }
          state.updateAlertShown = true;
          resolve();
          return;
        }
        reject(new Error('Unknown event name: ' + eventName));
      });
    },
    request(url, { method = 'get', timeout, headers, body, form, formData }, callback) {
      if (typeof callback !== 'function') throw new TypeError('callback must be a function');
      const id = nextRequestID++;
      pendingRequests.set(id, callback);
      requestQueue.push({ id, url: String(url), method, timeout, headers: headers || {}, body, form, formData });
      return () => {
        const fn = pendingRequests.get(id);
        if (!fn) return;
        pendingRequests.delete(id);
        const queuedIndex = requestQueue.findIndex(request => request.id === id);
        if (queuedIndex !== -1) requestQueue.splice(queuedIndex, 1);
        fn.call(lx, new Error('Cancel request'), null, null);
      };
    },
    utils: {
      crypto: {
        aesEncrypt: unavailable('crypto.aesEncrypt'),
        rsaEncrypt: unavailable('crypto.rsaEncrypt'),
        randomBytes: unavailable('crypto.randomBytes'),
        md5: unavailable('crypto.md5')
      },
      buffer: {
        from: unavailable('buffer.from'),
        bufToString: unavailable('buffer.bufToString')
      },
      zlib: {
        inflate: unavailable('zlib.inflate'),
        deflate: unavailable('zlib.deflate')
      }
    }
  };
  // Minimal event-loop timers. DispatchLXRequest pumps due callbacks between
  // host I/O operations; timers are never executed during a synchronous eval.
  const timers = new Map();
  let nextTimerID = 1;
  globalThis.setTimeout = (callback, delay = 0, ...args) => {
    if (typeof callback !== 'function') throw new TypeError('callback must be a function');
    const id = nextTimerID++;
    const milliseconds = Math.max(0, Math.min(Number(delay) || 0, 2147483647));
    timers.set(id, { callback, args, due: Date.now() + milliseconds });
    return id;
  };
  globalThis.clearTimeout = id => { timers.delete(Number(id)); };
  globalThis.setInterval = (callback, delay = 0, ...args) => {
    if (typeof callback !== 'function') throw new TypeError('callback must be a function');
    const id = nextTimerID++;
    const milliseconds = Math.max(1, Math.min(Number(delay) || 0, 2147483647));
    timers.set(id, { callback, args, due: Date.now() + milliseconds, interval: milliseconds });
    return id;
  };
  globalThis.clearInterval = id => { timers.delete(Number(id)); };
  if (!globalThis.queueMicrotask) globalThis.queueMicrotask = callback => {
    if (typeof callback !== 'function') throw new TypeError('callback must be a function');
    Promise.resolve().then(callback);
  };
  Object.defineProperty(globalThis, '__lxRunDueTimers', {
    value: () => {
      let executed = 0;
      while (executed < 1000) {
        const now = Date.now();
        let selectedID = 0;
        let selected = null;
        for (const [id, timer] of timers) {
          if (timer.due <= now && (!selected || timer.due < selected.due)) {
            selectedID = id;
            selected = timer;
          }
        }
        if (!selected) break;
        if (selected.interval > 0) selected.due = now + selected.interval;
        else timers.delete(selectedID);
        try { selected.callback(...selected.args); }
        catch (error) { globalThis.__lxTimerErrors.push(String(error && error.message || error)); }
        executed++;
      }
      return executed;
    }, configurable: true
  });
  Object.defineProperty(globalThis, '__lxNextTimerDelay', {
    value: () => {
      if (timers.size === 0) return -1;
      let due = Infinity;
      for (const timer of timers.values()) if (timer.due < due) due = timer.due;
      return Math.max(0, due - Date.now());
    }, configurable: true
  });
  Object.defineProperty(globalThis, '__lxTimerErrors', { value: [], configurable: true });
  Object.defineProperty(globalThis, '__lxTakeRequests', {
    value: () => requestQueue.splice(0, requestQueue.length), configurable: true
  });
  Object.defineProperty(globalThis, '__lxCompleteRequest', {
    value: (id, error, response) => {
      const callback = pendingRequests.get(id);
      if (!callback) return false;
      pendingRequests.delete(id);
      if (error) callback.call(lx, new Error(String(error)), null, null);
      else callback.call(lx, null, response, response.body);
      return true;
    }, configurable: true
  });
  Object.defineProperty(globalThis, '__lxHostState', { value: state, configurable: true });
  Object.defineProperty(globalThis, '__lxHostHandlers', { value: handlers, configurable: true });
  globalThis.lx = lx;
})();
`

const lxBrowserBootstrap = `(() => {
  globalThis.window = globalThis;
  globalThis.self = globalThis;
  if (!globalThis.document) globalThis.document = {};
  if (!globalThis.document.currentScript) globalThis.document.currentScript = { textContent: globalThis.lx.currentScriptInfo.rawScript || "" };
  if (!globalThis.navigator) globalThis.navigator = { userAgent: "Mozilla/5.0 My-LiveHub QuickJS desktop renderer" };
  if (!globalThis.location) globalThis.location = { href: "file:///" };
  if (!globalThis.performance) globalThis.performance = { now: () => Date.now() };
  if (typeof globalThis.URLSearchParams !== "function") globalThis.URLSearchParams = class URLSearchParams {
    constructor(input = "") { this.entriesList = []; if (typeof input === "string") { const text = input.replace(/^\?/, ""); for (const part of text.split("&")) { if (!part) continue; const i = part.indexOf("="); const key = i < 0 ? part : part.slice(0, i); const value = i < 0 ? "" : part.slice(i + 1); this.append(decodeURIComponent(key.replace(/\+/g, " ")), decodeURIComponent(value.replace(/\+/g, " "))); } } else if (input && typeof input === "object") { for (const key of Object.keys(input)) this.append(key, input[key]); } }
    append(key, value) { this.entriesList.push([String(key), String(value)]); }
    delete(key) { this.entriesList = this.entriesList.filter(entry => entry[0] !== String(key)); }
    get(key) { const entry = this.entriesList.find(item => item[0] === String(key)); return entry ? entry[1] : null; }
    getAll(key) { return this.entriesList.filter(item => item[0] === String(key)).map(item => item[1]); }
    has(key) { return this.entriesList.some(item => item[0] === String(key)); }
    set(key, value) { this.delete(key); this.append(key, value); }
    toString() { return this.entriesList.map(([k, v]) => encodeURIComponent(k).replace(/%20/g, "+") + "=" + encodeURIComponent(v).replace(/%20/g, "+")).join("&"); }
    *entries() { yield* this.entriesList; } *keys() { for (const entry of this.entriesList) yield entry[0]; } *values() { for (const entry of this.entriesList) yield entry[1]; }
    forEach(callback, thisArg) { for (const [key, value] of this.entriesList) callback.call(thisArg, value, key, this); }
    [Symbol.iterator]() { return this.entries(); }
  };
  if (typeof globalThis.URL !== "function") globalThis.URL = class URL {
    constructor(input, base) { let value = String(input); if (base && !/^[a-z][a-z0-9+.-]*:/i.test(value)) { const b = new URL(String(base)); if (value.startsWith("//")) value = b.protocol + value; else if (value.startsWith("/")) value = b.origin + value; else value = b.origin + b.pathname.replace(/[^/]*$/, "") + value; } const match = /^([a-z][a-z0-9+.-]*:)\/\/([^/?#]*)([^?#]*)(\?[^#]*)?(#.*)?$/i.exec(value); if (!match) throw new TypeError("Invalid URL"); this.protocol = match[1].toLowerCase(); this.host = match[2]; this.hostname = this.host.replace(/:\d+$/, ""); this.port = /:(\d+)$/.exec(this.host)?.[1] || ""; this.pathname = match[3] || "/"; this.search = match[4] || ""; this.hash = match[5] || ""; this.origin = this.protocol + "//" + this.host; this.searchParams = new URLSearchParams(this.search); this.href = this.origin + this.pathname + this.search + this.hash; }
    toString() { return this.href; } toJSON() { return this.href; }
  };
  if (typeof globalThis.TextEncoder !== "function") globalThis.TextEncoder = class TextEncoder { encode(value = "") { const binary = unescape(encodeURIComponent(String(value))); return Uint8Array.from(binary, c => c.charCodeAt(0)); } };
  if (typeof globalThis.TextDecoder !== "function") globalThis.TextDecoder = class TextDecoder { decode(value = new Uint8Array()) { const bytes = value instanceof Uint8Array ? value : new Uint8Array(value); let binary = ""; for (const byte of bytes) binary += String.fromCharCode(byte); try { return decodeURIComponent(escape(binary)); } catch (_) { return binary; } } };
  const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/";
  if (typeof globalThis.btoa !== "function") globalThis.btoa = value => { let out = ""; for (let i = 0; i < value.length; i += 3) { const a = value.charCodeAt(i), hasB = i + 1 < value.length, hasC = i + 2 < value.length; const b = hasB ? value.charCodeAt(i + 1) : 0, c = hasC ? value.charCodeAt(i + 2) : 0; if (a > 255 || b > 255 || c > 255) throw new TypeError("Invalid character"); out += alphabet[a >> 2] + alphabet[((a & 3) << 4) | (b >> 4)] + (hasB ? alphabet[((b & 15) << 2) | (c >> 6)] : "=") + (hasC ? alphabet[c & 63] : "="); } return out; };
  if (typeof globalThis.atob !== "function") globalThis.atob = value => { const input = String(value).replace(/[\t\n\f\r ]/g, ""); if (input.length % 4 === 1 || /[^A-Za-z0-9+/=]/.test(input) || /=/.test(input.slice(0, -2))) throw new TypeError("Invalid base64"); const clean = input.replace(/=+$/, ""); let out = ""; let bits = 0, buffer = 0; for (const ch of clean) { buffer = (buffer << 6) | alphabet.indexOf(ch); bits += 6; if (bits >= 8) { bits -= 8; out += String.fromCharCode((buffer >> bits) & 255); } } return out; };
  if (!globalThis.crypto) globalThis.crypto = {};
  if (typeof globalThis.crypto.getRandomValues !== "function") globalThis.crypto.getRandomValues = value => { if (!ArrayBuffer.isView(value) || value instanceof DataView) throw new TypeError("Expected an integer TypedArray"); if (value.byteLength > 65536) throw new RangeError("Array too large"); value.set(globalThis.lx.utils.crypto.randomBytes(value.length)); return value; };
})();`;
