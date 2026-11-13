'use strict'

const { app, BrowserWindow, ipcMain } = require('electron')
const readline = require('node:readline')
const path = require('node:path')

const EVENT = {
  initEnv: 'userApi_initEnv',
  init: 'userApi_init',
  request: 'userApi_request',
  response: 'userApi_response',
  openDevTools: 'userApi_openDevTools',
  showUpdateAlert: 'userApi_showUpdateAlert',
  getProxy: 'userApi_getProxy',
  proxyUpdate: 'userApi_proxyUpdate',
}

let windowRef = null
let initialized = false
let currentProxy = { host: '', port: '' }
let closing = false

function output(type, name, params) {
  process.stdout.write(JSON.stringify({ type, name, params }) + '\n')
}

function send(name, params) {
  if (!windowRef || windowRef.isDestroyed()) return
  windowRef.webContents.send(name, params)
}

function envelope(name, payload) {
  output('event', name, payload)
}

function createWindow() {
  windowRef = new BrowserWindow({
    resizable: false,
    minimizable: false,
    maximizable: false,
    fullscreenable: false,
    roundedCorners: false,
    hasShadow: false,
    show: false,
    webPreferences: {
      contextIsolation: true,
      nodeIntegration: false,
      nodeIntegrationInWorker: false,
      sandbox: false,
      spellcheck: false,
      autoplayPolicy: 'document-user-activation-required',
      enableWebSQL: false,
      disableDialogs: true,
      webgl: false,
      images: false,
      preload: path.join(__dirname, 'dist', 'user-api-preload.cjs'),
    },
  })

  for (const eventName of ['will-navigate', 'will-redirect', 'will-attach-webview', 'will-prevent-unload', 'media-started-playing']) {
    windowRef.webContents.on(eventName, event => event.preventDefault())
  }
  windowRef.webContents.session.setPermissionRequestHandler((contents, permission, callback) => callback(contents !== windowRef.webContents))
  windowRef.webContents.setWindowOpenHandler(() => ({ action: 'deny' }))
  windowRef.on('closed', () => { windowRef = null })
  const html = '<!DOCTYPE html><html lang="en"><head><meta charset="UTF-8"><meta http-equiv="Content-Security-Policy" content="default-src \'none\'"><meta name="viewport" content="width=device-width,initial-scale=1"></head><body></body></html>'
  return new Promise((resolve, reject) => {
    windowRef.once('ready-to-show', resolve)
    windowRef.loadURL('data:text/html;charset=UTF-8,' + encodeURIComponent(html)).catch(reject)
  })
}

function onRendererEvent(name, handler) {
  ipcMain.on(name, (_event, params) => handler(params || {}))
}

async function initialize(message) {
  await app.whenReady()
  if (initialized) throw new Error('LX runtime already initialized')
  currentProxy = message.proxy || currentProxy
  await createWindow()
  initialized = true
  send(EVENT.initEnv, {
    id: message.id || '',
    name: message.name || '',
    description: message.description || '',
    version: message.version || '',
    author: message.author || '',
    homepage: message.homepage || '',
    script: message.script,
    proxy: currentProxy,
  })
}

onRendererEvent(EVENT.init, params => envelope(EVENT.init, params))
onRendererEvent(EVENT.response, params => envelope(EVENT.response, params))
onRendererEvent(EVENT.openDevTools, () => {
  if (windowRef && !windowRef.isDestroyed()) windowRef.webContents.openDevTools({ mode: 'detach' })
  envelope(EVENT.openDevTools, {})
})
onRendererEvent(EVENT.showUpdateAlert, params => envelope(EVENT.showUpdateAlert, params))
onRendererEvent(EVENT.getProxy, () => send(EVENT.proxyUpdate, currentProxy))

const input = readline.createInterface({ input: process.stdin, crlfDelay: Infinity })
input.on('line', async line => {
  let message
  try { message = JSON.parse(line) } catch (_) { return }
  try {
    switch (message.operation) {
      case 'init':
        await initialize(message)
        output('ready', '', { ok: true })
        break
      case 'request':
        send(EVENT.request, { requestKey: message.requestKey, data: message.data })
        break
      case 'proxy':
        currentProxy = message.proxy || { host: '', port: '' }
        send(EVENT.proxyUpdate, currentProxy)
        break
      case 'close':
        closing = true
        if (windowRef && !windowRef.isDestroyed()) windowRef.destroy()
        app.quit()
        break
    }
  } catch (error) {
    output('error', message.operation || '', { message: error && error.message ? error.message : String(error) })
  }
})

app.whenReady().then(() => {
  app.on('window-all-closed', () => { if (!closing) app.quit() })
})
app.on('before-quit', () => { closing = true })
