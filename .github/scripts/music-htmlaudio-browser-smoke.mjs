import fs from 'node:fs'
import { createRequire } from 'node:module'
import { resolve } from 'node:path'

// Resolve from app/frontend, where the workflow installs Playwright.
const require = createRequire(resolve(process.cwd(), 'package.json'))
const { chromium } = require('playwright')

const urlFile = process.env.MYLIVEHUB_PLAYBACK_URL_FILE
if (!urlFile || !fs.existsSync(urlFile)) throw new Error('real playback URL handoff file is missing')
const mediaURL = fs.readFileSync(urlFile, 'utf8').trim()
if (!/^https?:\/\//i.test(mediaURL)) throw new Error('handoff does not contain an HTTP(S) media URL')

const browser = await chromium.launch({
  headless: true,
  args: ['--autoplay-policy=no-user-gesture-required', '--ignore-certificate-errors'],
})
try {
  const page = await browser.newPage({ ignoreHTTPSErrors: true })
  page.on('pageerror', error => console.error('browser page error:', error.message))
  await page.setContent(`
    <!doctype html><html><body>
      <button id="start">Start real media playback</button>
      <audio id="audio" preload="auto"></audio>
      <output id="state">idle</output>
      <script>
        const audio = document.querySelector('#audio')
        const state = document.querySelector('#state')
        for (const name of ['loadstart','loadedmetadata','canplay','playing','pause','timeupdate','ended','error','stalled','waiting']) {
          audio.addEventListener(name, () => { state.value = name })
        }
        document.querySelector('#start').addEventListener('click', async () => {
          try { await audio.play(); state.value = 'play-promise-resolved' }
          catch (error) { state.value = 'play-rejected:' + error.name + ':' + error.message }
        })
        window.testAudio = audio
      </script>
    </body></html>
  `)
  await page.locator('#audio').evaluate((audio, src) => { audio.src = src; audio.load() }, mediaURL)
  await page.locator('#start').click()
  await page.waitForFunction(() => {
    const audio = window.testAudio
    return audio && !audio.paused && audio.readyState >= 2 && audio.currentTime > 0
  }, null, { timeout: 60000 })
  const started = await page.evaluate(() => ({
    paused: window.testAudio.paused,
    readyState: window.testAudio.readyState,
    currentTime: window.testAudio.currentTime,
    duration: window.testAudio.duration,
    networkState: window.testAudio.networkState,
    errorCode: window.testAudio.error?.code ?? 0,
  }))
  if (started.paused || started.currentTime <= 0 || started.readyState < 2) {
    throw new Error('HTMLAudio did not enter active playback: ' + JSON.stringify(started))
  }

  await page.evaluate(() => window.testAudio.pause())
  await page.waitForFunction(() => window.testAudio.paused)
  const seekTarget = await page.evaluate(() => {
    const audio = window.testAudio
    if (!Number.isFinite(audio.duration) || audio.duration <= 2) {
      throw new Error('media duration is too short to verify seeking')
    }
    audio.currentTime = 1
    return 1
  })
  await page.waitForFunction(target => Math.abs(window.testAudio.currentTime - target) < 0.75, seekTarget, { timeout: 15000 })
  const seekResult = await page.evaluate(target => ({
    requested: target,
    actual: window.testAudio.currentTime,
    duration: window.testAudio.duration,
  }), seekTarget)
  if (Math.abs(seekResult.actual - seekResult.requested) > 0.75) {
    throw new Error('HTMLAudio seek did not reach requested position: ' + JSON.stringify(seekResult))
  }
  await page.evaluate(() => { window.testAudio.volume = 0.35 })
  await page.evaluate(async () => { await window.testAudio.play() })
  await page.waitForFunction(() => !window.testAudio.paused && window.testAudio.currentTime > 0.1, null, { timeout: 30000 })
  const resumed = await page.evaluate(() => ({
    paused: window.testAudio.paused,
    currentTime: window.testAudio.currentTime,
    volume: window.testAudio.volume,
    readyState: window.testAudio.readyState,
  }))
  if (resumed.paused || resumed.volume !== 0.35) {
    throw new Error('HTMLAudio pause/resume/volume smoke test failed: ' + JSON.stringify(resumed))
  }
  await page.evaluate(() => { window.testAudio.pause(); window.testAudio.removeAttribute('src'); window.testAudio.load() })
  const stopped = await page.evaluate(() => ({
    paused: window.testAudio.paused,
    currentSrc: window.testAudio.currentSrc,
    readyState: window.testAudio.readyState,
  }))
  if (!stopped.paused || stopped.currentSrc || stopped.readyState !== 0) {
    throw new Error('HTMLAudio stop did not release playback: ' + JSON.stringify(stopped))
  }
  console.log('REAL_HTMLAUDIO_PLAYBACK_PASS', JSON.stringify({ started, resumed, seekResult, stopped }))
} finally {
  await browser.close()
}
