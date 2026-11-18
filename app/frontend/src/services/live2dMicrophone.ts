import { reactive } from 'vue'
import { getLive2DSettings, type Live2DConfig } from './live2d'
import { getScenes, getBrowserSourceURL } from './layouts'

const state = reactive({
  active: false,
  starting: false,
  error: '',
  level: 0,
  mouthOpen: 0,
})

let stream: MediaStream | null = null
let audioContext: AudioContext | null = null
let workletNode: AudioWorkletNode | null = null
let muteGain: GainNode | null = null
let mouthEndpoint = ''
let resumePromise: Promise<void> | null = null
let startPromise: Promise<void> | null = null
let generation = 0
let previousMouth = 0
let currentConfig: Live2DConfig | null = null
let mouthDriver: ((value: number) => void) | null = null

async function resolveMouthEndpoint() {
  const scenes = await getScenes()
  if (!scenes.length) throw new Error('尚未创建综合展示场景，无法连接 Live2D 口型接口')
  const sourceURL = await getBrowserSourceURL(scenes[0].id)
  const statusURL = new URL('/api/live2d', sourceURL).toString()
  mouthEndpoint = new URL('/api/live2d/mouth', sourceURL).toString()
  const response = await fetch(statusURL, { cache: 'no-store' })
  const status = response.ok ? await response.json() as { enabled?: boolean } : null
  if (status?.enabled !== true) throw new Error('Live2D 服务尚未就绪，请稍后重试')
}

function sendMouth(value: number) {
  const bounded = Math.max(0, Math.min(1, value))
  if (mouthDriver) {
    mouthDriver(bounded)
    return
  }
  if (mouthEndpoint) {
    void fetch(mouthEndpoint, {
      method: 'POST',
      headers: { 'Content-Type': 'text/plain;charset=UTF-8' },
      body: JSON.stringify({ value: bounded }),
      cache: 'no-store',
    }).catch(() => undefined)
  }
}

async function resumeAudio() {
  const context = audioContext
  if (!context || context.state === 'running' || context.state === 'closed' || !state.active) return
  if (resumePromise) return resumePromise
  resumePromise = context.resume().then(() => {
    if (context.state !== 'running' && state.active) {
      state.error = '麦克风音频上下文未能恢复运行，请重新启动麦克风'
    }
  }).catch(error => {
    if (state.active) state.error = '恢复麦克风音频失败：' + String(error)
  }).finally(() => {
    resumePromise = null
  })
  return resumePromise
}

function handleVisibilityChange() {
  if (document.visibilityState === 'visible') void resumeAudio()
}

function handleAudioStateChange() {
  if (audioContext?.state === 'suspended' && state.active) void resumeAudio()
}

function cleanupAudio() {
  if (workletNode) {
    workletNode.port.onmessage = null
    workletNode.disconnect()
    workletNode = null
  }
  muteGain?.disconnect()
  muteGain = null
  stream?.getTracks().forEach(track => track.stop())
  stream = null
  if (audioContext) {
    audioContext.removeEventListener('statechange', handleAudioStateChange)
    void audioContext.close()
  }
  audioContext = null
  resumePromise = null
}

export function setLive2DMouthDriver(driver: ((value: number) => void) | null) {
  mouthDriver = driver
  if (driver && state.active) driver(state.mouthOpen)
}

export function updateLive2DMicrophoneConfig(config: Live2DConfig) {
  currentConfig = config
}

export async function startLive2DMicrophone() {
  if (state.active) return
  if (startPromise) return startPromise
  const runGeneration = generation
  state.starting = true
  state.error = ''
  startPromise = (async () => {
    try {
      if (!navigator.mediaDevices?.getUserMedia) throw new Error('当前 WebView 不支持麦克风采集')
      await resolveMouthEndpoint()
      const config = currentConfig || await getLive2DSettings()
      currentConfig = config
      const nextStream = await navigator.mediaDevices.getUserMedia({
        audio: {
          deviceId: config.microphone_device_id ? { exact: config.microphone_device_id } : undefined,
          echoCancellation: false,
          noiseSuppression: false,
          autoGainControl: false,
        },
      })
      if (runGeneration !== generation) {
        nextStream.getTracks().forEach(track => track.stop())
        return
      }
      stream = nextStream
      audioContext = new AudioContext()
      await audioContext.audioWorklet.addModule('/live2d-runtime/mic-processor.js')
      await audioContext.resume()
      if (runGeneration !== generation) {
        cleanupAudio()
        return
      }
      const source = audioContext.createMediaStreamSource(stream)
      workletNode = new AudioWorkletNode(audioContext, 'livehub-mic-analyser')
      muteGain = audioContext.createGain()
      muteGain.gain.value = 0
      previousMouth = 0
      workletNode.port.onmessage = (event: MessageEvent<{ rms?: number }>) => {
        if (!state.active || !currentConfig) return
        const rms = Math.max(0, Number(event.data?.rms) || 0)
        const raw = Math.max(0, Math.min(1, (rms * currentConfig.microphone_sensitivity - 0.012) * 8))
        const smoothing = currentConfig.microphone_smoothing
        state.level = state.level * smoothing + Math.min(1, rms * 8) * (1 - smoothing)
        state.mouthOpen = previousMouth * smoothing + raw * (1 - smoothing)
        previousMouth = state.mouthOpen
        sendMouth(state.mouthOpen)
      }
      source.connect(workletNode)
      workletNode.connect(muteGain)
      muteGain.connect(audioContext.destination)
      state.active = true
      audioContext.addEventListener('statechange', handleAudioStateChange)
      await resumeAudio()
    } catch (error) {
      cleanupAudio()
      state.active = false
      state.level = 0
      state.mouthOpen = 0
      state.error = String(error)
      sendMouth(0)
    } finally {
      state.starting = false
      startPromise = null
    }
  })()
  return startPromise
}

export function stopLive2DMicrophone() {
  generation += 1
  cleanupAudio()
  state.active = false
  state.starting = false
  state.level = 0
  state.mouthOpen = 0
  previousMouth = 0
  sendMouth(0)
}

export function getLive2DMicrophoneState() {
  return state
}
