import fs from 'node:fs'
import http from 'node:http'
import { createRequire } from 'node:module'
import { resolve } from 'node:path'

const require = createRequire(resolve(process.cwd(), 'package.json'))
const { chromium } = require('playwright')
const worklet = fs.readFileSync(resolve(process.cwd(), 'public/music-loudness-worklet.js'))

const server = http.createServer((request, response) => {
  if (request.url === '/worklet.js') {
    response.writeHead(200, { 'Content-Type': 'text/javascript; charset=utf-8', 'Cache-Control': 'no-store' })
    response.end(worklet)
    return
  }
  response.writeHead(200, { 'Content-Type': 'text/html; charset=utf-8' })
  response.end('<!doctype html><html><body>AudioWorklet smoke test</body></html>')
})
await new Promise((resolveListen, reject) => {
  server.once('error', reject)
  server.listen(0, '127.0.0.1', resolveListen)
})
const address = server.address()
if (!address || typeof address === 'string') throw new Error('local AudioWorklet test server did not bind')

const browser = await chromium.launch({
  headless: true,
  args: ['--autoplay-policy=no-user-gesture-required', '--use-fake-ui-for-media-stream'],
})
try {
  const page = await browser.newPage()
  await page.goto(`http://127.0.0.1:${address.port}/`)

  const normalized = await page.evaluate(async () => {
    const context = new AudioContext()
    await context.resume()
    await context.audioWorklet.addModule('/worklet.js')
    const oscillator = context.createOscillator()
    const inputGain = context.createGain()
    const normalizer = new AudioWorkletNode(context, 'mylivehub-loudness-normalizer', {
      numberOfInputs: 1,
      numberOfOutputs: 1,
      channelCount: 2,
      channelCountMode: 'explicit',
      outputChannelCount: [2],
    })
    const outputGain = context.createGain()
    outputGain.gain.value = 1
    const analyser = context.createAnalyser()
    analyser.fftSize = 4096
    inputGain.gain.value = 0.01
    oscillator.frequency.value = 440
    oscillator.connect(inputGain).connect(normalizer).connect(outputGain).connect(analyser).connect(context.destination)
    oscillator.start()
    await new Promise(resolveWait => setTimeout(resolveWait, 2600))
    const samples = new Float32Array(analyser.fftSize)
    analyser.getFloatTimeDomainData(samples)
    let squareSum = 0
    let peak = 0
    for (const sample of samples) {
      squareSum += sample * sample
      peak = Math.max(peak, Math.abs(sample))
    }
    outputGain.gain.value = 0.35
    await new Promise(resolveWait => setTimeout(resolveWait, 100))
    const attenuatedSamples = new Float32Array(analyser.fftSize)
    analyser.getFloatTimeDomainData(attenuatedSamples)
    let attenuatedSquareSum = 0
    for (const sample of attenuatedSamples) attenuatedSquareSum += sample * sample
    oscillator.stop()
    await context.close()
    return {
      rms: Math.sqrt(squareSum / samples.length),
      peak,
      attenuatedRms: Math.sqrt(attenuatedSquareSum / attenuatedSamples.length),
    }
  })
  if (!(normalized.rms > 0.01 && normalized.rms < 0.2)) {
    throw new Error('quiet media was not raised toward the configured loudness target: ' + JSON.stringify(normalized))
  }
  const volumeRatio = normalized.attenuatedRms / normalized.rms
  if (!(volumeRatio > 0.30 && volumeRatio < 0.40)) {
    throw new Error('post-normalization user volume gain was not preserved: ' + JSON.stringify({ normalized, volumeRatio }))
  }

  const limited = await page.evaluate(async () => {
    const context = new AudioContext()
    await context.resume()
    await context.audioWorklet.addModule('/worklet.js')
    const oscillator = context.createOscillator()
    const normalizer = new AudioWorkletNode(context, 'mylivehub-loudness-normalizer', {
      numberOfInputs: 1,
      numberOfOutputs: 1,
      channelCount: 2,
      channelCountMode: 'explicit',
      outputChannelCount: [2],
      processorOptions: { targetLUFS: 6 },
    })
    const analyser = context.createAnalyser()
    analyser.fftSize = 4096
    oscillator.frequency.value = 997
    oscillator.connect(normalizer).connect(analyser).connect(context.destination)
    oscillator.start()
    await new Promise(resolveWait => setTimeout(resolveWait, 1800))
    const samples = new Float32Array(analyser.fftSize)
    analyser.getFloatTimeDomainData(samples)
    let peak = 0
    for (const sample of samples) peak = Math.max(peak, Math.abs(sample))
    oscillator.stop()
    await context.close()
    return { peak }
  })
  if (!(limited.peak > 0.5 && limited.peak <= 0.95)) {
    throw new Error('true-peak limiter did not constrain the output: ' + JSON.stringify(limited))
  }

  console.log('LOUDNESS_WORKLET_PASS', JSON.stringify({ normalized, limited }))
} finally {
  await browser.close()
  await new Promise(resolveClose => server.close(resolveClose))
}
