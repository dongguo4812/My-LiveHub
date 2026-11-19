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
	bootstrap := lxHostBootstrap + "\n;globalThis.lx.currentScriptInfo = " + string(metadata) + ";"
	if _, err := r.Eval(ctx, bootstrap, "lx-host-bootstrap.js"); err != nil {
		return fmt.Errorf("install LX host contract: %w", err)
	}
	return nil
}

const lxHostBootstrap = `
(() => {
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
    request: unavailable('request'),
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
  Object.defineProperty(globalThis, '__lxHostState', { value: state, configurable: true });
  Object.defineProperty(globalThis, '__lxHostHandlers', { value: handlers, configurable: true });
  globalThis.lx = lx;
})();
`
