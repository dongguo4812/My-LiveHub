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
          track: { id: 'tx_fixture', source_id: 'tx', title: '桌面歌词验收曲', artist: '回归测试' } }
      }
      export async function GetLyrics() {
        return '[00:01.00]第一句\\n[00:02.50]第二句\\n[00:02.50]Second translation'
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
  assert.equal((await page.locator('.translated-lyric').textContent()).trim(), 'Second translation')

  await page.evaluate(() => { globalThis.__lyricsPosition = 2600 })
  await page.waitForFunction(() => document.querySelector('.original-lyric')?.textContent?.includes('第二句'), null, { timeout: 5000 })
  assert.equal((await page.locator('.translated-lyric').textContent()).trim(), 'Second translation')
  assert.deepEqual(errors, [], `desktop lyrics overlay browser errors: ${errors.join('; ')}`)
  console.log('Desktop lyrics overlay browser smoke passed: provider lyrics rendered, translation displayed, active line advanced with playback position.')
} finally {
  if (browser) await browser.close()
  await server.close()
}
