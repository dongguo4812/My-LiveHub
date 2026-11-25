import { Events } from '@wailsio/runtime'
import * as MusicService from '../../bindings/github.com/dongguo4812/My-LiveHub/app/internal/desktop/musicservice.js'

type Resource = { url: string; source_id: string; track_id: string; quality: string }
type Command = {
  command: 'load' | 'play' | 'pause' | 'stop' | 'seek' | 'volume' | 'loudness-normalization'
  session_id: number
  track: unknown
  resource: Resource
  position_ms?: number
  volume?: number
  loudness_normalization?: boolean
}

const audio = new Audio()
audio.preload = 'none'

// The route-level slider reads the singleton's live value so remounting the
// music view cannot display 100% while the actual Audio instance is quieter.
export function getHTMLAudioVolume(): number {
  return Math.round(audio.volume * 100)
}
let activeSession = 0
let activeURL = ''
let lastTimeUpdate = 0
let disposed = false
let loudnessNormalizationEnabled = false
let audioContext: AudioContext | undefined
let mediaSource: MediaElementAudioSourceNode | undefined
let compressor: DynamicsCompressorNode | undefined
let makeupGain: GainNode | undefined

function ensureAudioGraph() {
  if (audioContext) return
  audioContext = new AudioContext()
  mediaSource = audioContext.createMediaElementSource(audio)
  compressor = audioContext.createDynamicsCompressor()
  makeupGain = audioContext.createGain()
  compressor.threshold.value = -26
  compressor.knee.value = 24
  compressor.ratio.value = 4
  compressor.attack.value = 0.006
  compressor.release.value = 0.24
  makeupGain.gain.value = 1.35
  mediaSource.connect(audioContext.destination)
}

function applyLoudnessNormalization() {
  if (!loudnessNormalizationEnabled) {
    if (mediaSource && audioContext) {
      mediaSource.disconnect()
      mediaSource.connect(audioContext.destination)
    }
    return
  }
  ensureAudioGraph()
  if (!mediaSource || !compressor || !makeupGain || !audioContext) return
  mediaSource.disconnect()
  mediaSource.connect(compressor)
  compressor.connect(makeupGain)
  makeupGain.connect(audioContext.destination)
}

async function resumeAudioContext() {
  if (!loudnessNormalizationEnabled) return
  ensureAudioGraph()
  applyLoudnessNormalization()
  if (audioContext?.state === 'suspended') await audioContext.resume()
}

function report(event: string, message = '') {
  if (!activeSession) return
  void MusicService.ReportHTMLAudioEvent(
    activeSession,
    event,
    Number.isFinite(audio.currentTime) ? Math.round(audio.currentTime * 1000) : 0,
    Number.isFinite(audio.duration) ? Math.round(audio.duration * 1000) : 0,
    message,
  ).catch(error => console.error('HTMLAudio state bridge failed', error))
}

audio.addEventListener('loadstart', () => report('loadstart'))
audio.addEventListener('playing', () => report('playing'))
audio.addEventListener('pause', () => { if (!audio.ended) report('pause') })
audio.addEventListener('durationchange', () => report('durationchange'))
audio.addEventListener('timeupdate', () => {
  const now = performance.now()
  if (now - lastTimeUpdate < 250) return
  lastTimeUpdate = now
  report('timeupdate')
})
audio.addEventListener('waiting', () => report('waiting'))
audio.addEventListener('stalled', () => report('stalled'))
audio.addEventListener('ended', () => report('ended'))
audio.addEventListener('error', () => {
  const code = audio.error?.code ?? 0
  const message = audio.error?.message || `HTMLAudio media error (code ${code})`
  report('error', message)
})

async function execute(command: Command) {
  if (disposed || !command) return
  if (command.command === 'load') {
    activeSession = 0
    audio.pause()
    audio.removeAttribute('src')
    audio.load()
    activeSession = command.session_id
    loudnessNormalizationEnabled = command.loudness_normalization ?? loudnessNormalizationEnabled
    if (loudnessNormalizationEnabled) {
      try { await resumeAudioContext() } catch (reason) { report('error', `响度标准化音频处理启动失败：${reason instanceof Error ? reason.message : String(reason)}`); return }
    }
    audio.volume = Math.min(1, Math.max(0, command.volume ?? 1))
    audio.src = command.resource.url
    audio.load()
    try {
      await audio.play()
    } catch (reason) {
      const message = reason instanceof Error ? reason.message : String(reason)
      if (reason instanceof DOMException && reason.name === 'NotAllowedError') {
        report('autoplayblocked', message)
      } else {
        report('error', message)
      }
    }
    return
  }
  if (command.command === 'volume' && command.session_id === 0) {
    audio.volume = Math.min(1, Math.max(0, command.volume ?? 1))
    return
  }
  if (command.session_id !== activeSession) return
  switch (command.command) {
    case 'play':
      try { await resumeAudioContext(); await audio.play() }
      catch (reason) {
        const message = reason instanceof Error ? reason.message : String(reason)
        report('autoplayblocked', message)
      }
      break
    case 'pause': audio.pause(); break
    case 'stop':
      // Invalidate before pause dispatches its asynchronous event so a late pause report cannot overwrite stopped state.
      activeSession = 0
      audio.pause()
      audio.removeAttribute('src')
      audio.load()
      break
    case 'seek': {
      // Setting currentTime before loadedmetadata is valid: the browser queues
      // the seek and applies it when media metadata becomes available. Only
      // clamp when the media duration is already known; otherwise the old
      // finite-duration guard silently discarded every early seek command.
      const requestedTime = Math.max(0, (command.position_ms ?? 0) / 1000)
      audio.currentTime = Number.isFinite(audio.duration)
        ? Math.min(audio.duration, requestedTime)
        : requestedTime
      break
    }
    case 'loudness-normalization':
      loudnessNormalizationEnabled = command.loudness_normalization ?? false
      try { applyLoudnessNormalization(); await resumeAudioContext() }
      catch (reason) { report('error', `响度标准化设置应用失败：${reason instanceof Error ? reason.message : String(reason)}`) }
      break
    case 'volume':
      audio.volume = Math.min(1, Math.max(0, command.volume ?? 1))
      break
  }
}

// The module is imported once by the application shell. Keeping this one Audio
// object and its event subscription outside route components prevents duplicate
// playback instances when the music view is mounted/unmounted.
const unsubscribe = Events.On('music:html-audio:command', event => {
  void execute(event.data as Command)
})

export function disposeHTMLAudioForShutdown() {
  if (disposed) return
  disposed = true
  unsubscribe()
  // Invalidate first: pause/load can synchronously dispatch media events.
  activeSession = 0
  audio.pause()
  audio.removeAttribute('src')
  audio.load()
}
