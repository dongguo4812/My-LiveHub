'use strict'

const { spawn } = require('node:child_process')
const path = require('node:path')

const executable = process.argv[2]
if (!executable) throw new Error('Pass the packaged LX runtime executable path')

const child = spawn(path.resolve(executable), [], { stdio: ['pipe', 'pipe', 'ignore'] })
let pending = ''
const messages = []
const waiters = []

function dispatch(message) {
  messages.push(message)
  for (let index = waiters.length - 1; index >= 0; index--) {
    const waiter = waiters[index]
    if (!waiter.predicate(message)) continue
    waiters.splice(index, 1)
    clearTimeout(waiter.timer)
    waiter.resolve(message)
  }
}

function waitFor(predicate, timeoutMs = 15000) {
  const existing = messages.find(predicate)
  if (existing) return Promise.resolve(existing)
  return new Promise((resolve, reject) => {
    const waiter = {
      predicate,
      resolve,
      timer: setTimeout(() => {
        const index = waiters.indexOf(waiter)
        if (index >= 0) waiters.splice(index, 1)
        reject(new Error('Timed out waiting for LX runtime IPC response'))
      }, timeoutMs),
    }
    waiters.push(waiter)
  })
}

child.stdout.setEncoding('utf8')
child.stdout.on('data', chunk => {
  pending += chunk
  for (;;) {
    const index = pending.indexOf('\n')
    if (index < 0) break
    const line = pending.slice(0, index).trim()
    pending = pending.slice(index + 1)
    if (!line) continue
    try { dispatch(JSON.parse(line)) } catch (_) {}
  }
})

child.on('error', error => {
  for (const waiter of waiters.splice(0)) {
    clearTimeout(waiter.timer)
    waiter.reject(error)
  }
})

async function run() {
  const script = [
    'lx.on(lx.EVENT_NAMES.request, function (request) {',
    '  return Promise.resolve("https://cdn.example.test/" + request.info.songmid + ".mp3");',
    '});',
    'lx.send(lx.EVENT_NAMES.inited, {',
    '  sources: { wy: { type: "music", actions: ["musicUrl"], qualitys: ["320k"] } }',
    '});',
  ].join('\n')

  child.stdin.write(JSON.stringify({
    operation: 'init',
    script,
    id: 'smoke',
    name: 'LX runtime smoke test',
    description: '',
    version: '1.0.0',
    author: '',
    homepage: '',
    proxy: { host: '', port: '' },
  }) + '\n')

  const initialized = await waitFor(message =>
    message.type === 'event' &&
    message.name === 'userApi_init' &&
    message.params &&
    message.params.status === true
  )
  if (!initialized.params.data.sources.wy) throw new Error('LX preload did not register the test source')

  child.stdin.write(JSON.stringify({
    operation: 'request',
    requestKey: 'smoke-request-1',
    data: {
      source: 'wy',
      action: 'musicUrl',
      info: { type: '320k', songmid: 'smoke-track' },
    },
  }) + '\n')

  const response = await waitFor(message =>
    message.type === 'event' &&
    message.name === 'userApi_response' &&
    message.params &&
    message.params.status === true &&
    message.params.data &&
    message.params.data.requestKey === 'smoke-request-1'
  )
  const url = response.params.data.result.data.url
  if (url !== 'https://cdn.example.test/smoke-track.mp3') {
    throw new Error('Unexpected musicUrl result: ' + String(url))
  }

  child.stdin.write(JSON.stringify({ operation: 'close' }) + '\n')
  child.stdin.end()
  process.stdout.write('LX Electron preload initialization and musicUrl IPC smoke test passed\n')
}

run().catch(error => {
  child.kill()
  process.stderr.write((error && error.stack) || String(error))
  process.exitCode = 1
})
