'use strict'

const { spawn } = require('node:child_process')
const { createServer } = require('node:net')
const { randomBytes } = require('node:crypto')
const readline = require('node:readline')
const path = require('node:path')

const executable = process.argv[2]
if (!executable) throw new Error('Pass the packaged LX runtime executable path')

const token = randomBytes(32).toString('hex')
const server = createServer()
let child
let socket
let stderr = ''
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

function waitFor(predicate, timeoutMs = 30000, label = 'LX runtime IPC response') {
  const existing = messages.find(predicate)
  if (existing) return Promise.resolve(existing)
  return new Promise((resolve, reject) => {
    const waiter = {
      predicate,
      resolve,
      timer: setTimeout(() => {
        const index = waiters.indexOf(waiter)
        if (index >= 0) waiters.splice(index, 1)
        reject(new Error('Timed out waiting for ' + label + '; stderr: ' + stderr))
      }, timeoutMs),
    }
    waiters.push(waiter)
  })
}

function send(message) {
  if (!socket || socket.destroyed) throw new Error('LX Electron IPC socket is closed')
  socket.write(JSON.stringify(message) + '\n')
}

async function run() {
  await new Promise((resolve, reject) => {
    server.once('error', reject)
    server.listen(0, '127.0.0.1', resolve)
  })
  const address = server.address()
  child = spawn(path.resolve(executable), [
    '--lx-runtime-ipc', '127.0.0.1:' + address.port,
    '--lx-runtime-token', token,
  ], { stdio: ['ignore', 'ignore', 'pipe'] })
  child.stderr.setEncoding('utf8')
  child.stderr.on('data', chunk => { stderr += chunk })

  const connection = await new Promise((resolve, reject) => {
    server.once('connection', resolve)
    child.once('error', reject)
    child.once('exit', code => reject(new Error('LX runtime exited before IPC connection: ' + code + '; stderr: ' + stderr)))
  })
  socket = connection
  const input = readline.createInterface({ input: socket, crlfDelay: Infinity })
  let handshakeReceived = false
  const hello = await new Promise((resolve, reject) => {
    const timer = setTimeout(() => reject(new Error('Timed out waiting for LX IPC handshake; stderr: ' + stderr)), 15000)
    input.on('line', line => {
      let message
      try { message = JSON.parse(line) } catch (error) { reject(error); return }
      if (!handshakeReceived) {
        handshakeReceived = true
        clearTimeout(timer)
        resolve(message)
        return
      }
      dispatch(message)
    })
  })
  if (hello.type !== 'hello' || hello.token !== token) throw new Error('Invalid LX runtime IPC handshake')
  socket.on('close', () => {
    for (const waiter of waiters.splice(0)) {
      clearTimeout(waiter.timer)
      waiter.reject(new Error('LX runtime IPC socket closed; stderr: ' + stderr))
    }
  })

  const script = [
    'lx.on(lx.EVENT_NAMES.request, function (request) {',
    '  return Promise.resolve("https://cdn.example.test/" + request.info.musicInfo.songmid + ".mp3");',
    '});',
    'lx.send(lx.EVENT_NAMES.inited, {',
    '  sources: { wy: { type: "music", actions: ["musicUrl"], qualitys: ["320k"] } }',
    '});',
  ].join('\n')

  send({
    operation: 'init',
    script,
    id: 'smoke',
    name: 'LX runtime smoke test',
    description: '',
    version: '1.0.0',
    author: '',
    homepage: '',
    proxy: { host: '', port: '' },
  })

  const initialized = await waitFor(message =>
    message.type === 'event' &&
    message.name === 'userApi_init' &&
    message.params &&
    message.params.status === true,
    30000,
    'original LX preload userApi_init event'
  )
  if (!initialized.params.data.sources.wy) throw new Error('LX preload did not register the test source')

  send({
    operation: 'request',
    requestKey: 'smoke-request-1',
    data: {
      source: 'wy',
      action: 'musicUrl',
      info: { type: '320k', musicInfo: { songmid: 'smoke-track' } },
    },
  })

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

  send({ operation: 'close' })
  socket.end()
  await new Promise(resolve => child.once('exit', resolve))
  process.stdout.write('LX Electron preload initialization and musicUrl IPC smoke test passed\n')
}

run().catch(error => {
  if (socket && !socket.destroyed) socket.destroy()
  if (child && !child.killed) child.kill()
  process.stderr.write((error && error.stack) || String(error))
  process.exitCode = 1
}).finally(() => {
  server.close()
})
