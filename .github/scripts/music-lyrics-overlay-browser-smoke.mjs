import assert from 'node:assert/strict'
import { createRequire } from 'node:module'
import { fileURLToPath } from 'node:url'
import path from 'node:path'

const frontendRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../../app/frontend')
// Resolve frontend dependencies from the frontend package, not from this script's
// .github/scripts directory (Node resolves bare imports relative to the importer).
const requireFrontend = createRequire(path.join(frontendRoot, 'package.json'))
const { createServer } = requireFrontend('vite')
const vue = requireFrontend('@vitejs/plugin-vue').default
const { chromium } = requireFrontend('playwright')
const mocks = {
  music: '\0my-livehub-lyrics-music-service',
  window: '\0my-livehub-lyrics-window-service',
}
const mockBindings = {
  name: 'my-livehub-desktop-lyrics-mocks',
  enforce: 'pre',
  resolveId(source) {
    if (source.includes('/bindings/') && source.endsWith('/musicservice.js')) return mocks.music
    if (source.includes('/bindings/') && source.endsWith('/musiclyricswindowservice.js')) return mocks.window
    return null
  },
  load(id) {
    if (id === mocks.music) return `
      export async function GetCachedPlaybackStatus() {
        return { state: 'paused', position_ms: globalThis.__lyricsPosition ?? 1000, duration_ms: 10000,
          track: globalThis.__lyricsTrack ?? { id: 'tx_fixture', source_id: 'tx', title: '桌面歌词验收曲', artist: '回归测试' } }
      }
      export async function GetLyricsForRequest(track, requestID) {
        globalThis.__lyricsRequests ??= []
        globalThis.__lyricsRequests.push(requestID)
        if (track.id === 'tx_fixture_pending') {
          return await new Promise(resolve => { globalThis.__pendingLyricsResolve = resolve })
        }
        if (track.id === 'tx_fixture_error') {
          globalThis.__lyricsErrorAttempts ??= 0
          globalThis.__lyricsErrorAttempts++
          if (globalThis.__lyricsErrorAttempts === 1) throw new Error('模拟歌词请求超时')
        }
        return ['[00:01.00]第一句', '[00:01.00]First translation', '[00:02.50]第二句', '[00:02.50]Second translation'].join(String.fromCharCode(10))
      }
      export function CancelLyrics(requestID) {
        globalThis.__cancelledLyricsRequests ??= []
        globalThis.__cancelledLyricsRequests.push(requestID)
        globalThis.__pendingLyricsResolve?.('')
      }
    `
    if (id === mocks.window) return 'export async function CloseWindow() {}'
    return null
  },
}

const server = await createServer({
  configFile: false,
  root: frontendRoot,
  plugins: [vue(), mockBindings],
  server: { host: '127.0.0.1', port: 0, strictPort: false },
  appType: 'mpa',
})
let browser
try {
  await server.listen()
  const address = server.httpServer.address()
  assert.ok(address && typeof address === 'object', 'Vite test server did not bind a TCP port')
  browser = await chromium.launch({ headless: true })
  const page = await browser.newPage()
  const errors = []
  page.on('pageerror', error => errors.push(error.message))
  await page.goto(`http://127.0.0.1:${address.port}/lyrics-overlay-smoke.html`)
  const original = page.locator('.original-lyric')
  await original.waitFor({ state: 'visible' })
  await page.waitForFunction(() => document.querySelector('.original-lyric')?.textContent?.includes('第一句'))
  assert.match(await page.locator('.lyrics-track strong').textContent(), /桌面歌词验收曲/)
  assert.equal((await page.locator('.translated-lyric').textContent()).trim(), 'First translation')

  // The fixture starts paused. Repeated playback snapshots must neither cancel
  // nor duplicate the current track's lyric request while playback is paused.
  await page.waitForTimeout(350)
  assert.equal(await page.evaluate(() => globalThis.__lyricsRequests?.length ?? 0), 1,
    'paused playback duplicated the active track lyric request')
  assert.equal(await page.evaluate(() => globalThis.__cancelledLyricsRequests?.length ?? 0), 0,
    'paused playback cancelled the active track lyric request')

  await page.evaluate(() => { globalThis.__lyricsPosition = 2600 })
  await page.waitForFunction(() => document.querySelector('.original-lyric')?.textContent?.includes('第二句'), null, { timeout: 5000 })
  assert.equal((await page.locator('.translated-lyric').textContent()).trim(), 'Second translation')

  await page.evaluate(() => {
    globalThis.__lyricsPosition = 1000
    globalThis.__lyricsTrack = { id: 'tx_fixture_pending', source_id: 'tx', title: '待取消歌词曲', artist: '回归测试' }
  })
  await page.waitForFunction(() => document.querySelector('.lyrics-track strong')?.textContent?.includes('待取消歌词曲'))
  await page.waitForFunction(() => (globalThis.__lyricsRequests?.length ?? 0) >= 2)
  const pendingRequestID = await page.evaluate(() => globalThis.__lyricsRequests?.at(-1))
  assert.ok(pendingRequestID, 'desktop lyrics did not issue a cancellable request ID')
  await page.evaluate(() => {
    globalThis.__lyricsPosition = 1000
    globalThis.__lyricsTrack = { id: 'tx_fixture_next', source_id: 'tx', title: '切歌取消验收曲', artist: '回归测试' }
  })
  await page.waitForFunction(() => document.querySelector('.lyrics-track strong')?.textContent?.includes('切歌取消验收曲'))
  await page.waitForFunction(id => globalThis.__cancelledLyricsRequests?.includes(id), pendingRequestID)
  await page.waitForFunction(() => document.querySelector('.original-lyric')?.textContent?.includes('第一句'))

  await page.evaluate(() => {
    globalThis.__lyricsPosition = 1000
    globalThis.__lyricsTrack = { id: 'tx_fixture_error', source_id: 'tx', title: '歌词重试验收曲', artist: '回归测试' }
  })
  await page.waitForFunction(() => document.querySelector('.lyrics-track strong')?.textContent?.includes('歌词重试验收曲'))
  await page.locator('.retry-lyrics').waitFor({ state: 'visible' })
  assert.match(await page.locator('.lyrics-error, .translated-lyric').first().textContent(), /模拟歌词请求超时/)
  await page.locator('.retry-lyrics').click()
  await page.waitForFunction(() => document.querySelector('.original-lyric')?.textContent?.includes('第一句'))
  await page.locator('.retry-lyrics').waitFor({ state: 'hidden' })
  assert.deepEqual(errors, [], `desktop lyrics overlay browser errors: ${errors.join('; ')}`)
  console.log('Desktop lyrics overlay browser smoke passed: synchronized and translated lyrics render, track changes cancel in-flight requests, and transient failures can be retried.')
} finally {
  if (browser) await browser.close()
  await server.close()
}
