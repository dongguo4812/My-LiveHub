import { Events } from '@wailsio/runtime'
import * as MusicService from '../../bindings/github.com/dongguo4812/My-LiveHub/app/internal/desktop/musicservice.js'

type Resource = {
  url: string
  source_id: string
  track_id: string
  quality: string
  requires_proxy?: boolean
  headers?: Record<string, string>
}
type Command = {
  command: 'load' | 'play' | 'pause' | 'stop' | 'seek' | 'volume' | 'loudness-normalization'
  session_id: number
  track: unknown
  resource: Resource
  position_ms?: number
  autoplay?: boolean
  volume?: number
  loudness_normalization?: boolean
}

let audio: HTMLAudioElement
let activeSession = 0
let disposed = false
let audioContext: AudioContext | null = null
let mediaSource: MediaElementAudioSourceNode | null = null
let normalizerNode: AudioWorkletNode | null = null
let outputGain: GainNode | null = null
let userVolume = 1
let lastAudibleVolume = 1
let webAudioAttached = false

function report(event: string, message = '', source: HTMLAudioElement = audio) {
  if (source !== audio || !activeSession) return
  void MusicService.ReportHTMLAudioEvent(
    activeSession,
    event,
    Number.isFinite(source.currentTime) ? Math.round(source.currentTime * 1000) : 0,
    Number.isFinite(source.duration) ? Math.round(source.duration * 1000) : 0,
    message,
  ).catch(error => console.error('HTMLAudio state bridge failed', error))
}

function bindAudioEvents(element: HTMLAudioElement) {
  element.addEventListener('loadstart', () => report('loadstart', '', element))
  element.addEventListener('playing', () => report('playing', '', element))
  element.addEventListener('pause', () => { if (!element.ended) report('pause', '', element) })
  element.addEventListener('durationchange', () => report('durationchange', '', element))
  element.addEventListener('waiting', () => report('waiting', '', element))
  element.addEventListener('stalled', () => report('stalled', '', element))
  element.addEventListener('ended', () => report('ended', '', element))
  element.addEventListener('error', () => {
    const code = element.error?.code ?? 0
    const message = element.error?.message || `HTMLAudio media error (code ${code})`
    report('error', message, element)
  })
}

function createAudioElement(): HTMLAudioElement {
  const element = new Audio()
  element.preload = 'none'
  bindAudioEvents(element)
  return element
}

audio = createAudioElement()

// Publish the real media clock to Go at a fixed 100ms cadence while playing.
// The desktop lyrics window consumes this snapshot directly; it must not
// extrapolate playback time independently.
window.setInterval(() => {
  if (!disposed && activeSession && !audio.paused && !audio.ended) {
    report('timeupdate')
  }
}, 100)

// Once MediaElementAudioSourceNode is created, that media element cannot be
// returned to native output. Disabling DSP therefore replaces the element,
// rather than leaving a disconnected Web Audio node that would mute playback.
async function switchAudioModeInternal(enabled: boolean) {
  if (disposed) return
  if (enabled && webAudioAttached) {
    if (!audioContext || audioContext.state === 'closed') {
      await switchAudioModeInternal(false)
      throw new Error('audio context closed; switched back to native HTMLAudio')
    }
    if (audioContext.state === 'suspended') {
      try {
        await audioContext.resume()
      } catch (error) {
        await switchAudioMode(false)
        throw error
      }
    }
    return
  }
  if (!enabled && !webAudioAttached) {
    audio.removeAttribute('crossorigin')
    return
  }

  if (!enabled && webAudioAttached) {
    const previous = audio
    previous.pause()
    previous.removeAttribute('src')
    previous.load()
    try { mediaSource?.disconnect() } catch { /* already disconnected */ }
    try { normalizerNode?.disconnect() } catch { /* already disconnected */ }
    try { outputGain?.disconnect() } catch { /* already disconnected */ }
    const oldContext = audioContext
    mediaSource = null
    normalizerNode = null
    outputGain = null
    audioContext = null
    webAudioAttached = false
    if (oldContext && oldContext.state !== 'closed') await oldContext.close()
    if (disposed) return
    audio = createAudioElement()
    audio.volume = userVolume
    return
  }

  audio.crossOrigin = 'anonymous'
  const context = new AudioContext({ latencyHint: 'playback' })
  audioContext = context
  let source: MediaElementAudioSourceNode | null = null
  let normalizer: AudioWorkletNode | null = null
  let gainNode: GainNode | null = null
  try {
    await context.resume()
    if (disposed) {
      if (context.state !== 'closed') await context.close()
      if (audioContext === context) audioContext = null
      return
    }
    await context.audioWorklet.addModule('/music-loudness-worklet.js')
    if (disposed) {
      if (context.state !== 'closed') await context.close()
      if (audioContext === context) audioContext = null
      return
    }
    normalizer = new AudioWorkletNode(context, 'mylivehub-loudness-normalizer', {
      numberOfInputs: 1,
      numberOfOutputs: 1,
      channelCount: 2,
      channelCountMode: 'explicit',
      outputChannelCount: [2],
    })
    gainNode = context.createGain()
    gainNode.gain.value = userVolume
    source = context.createMediaElementSource(audio)
    source.connect(normalizer)
    normalizer.connect(gainNode)
    gainNode.connect(context.destination)
    mediaSource = source
    normalizerNode = normalizer
    outputGain = gainNode
    audio.volume = 1
    webAudioAttached = true
  } catch (error) {
    if (source) {
      try { normalizer?.disconnect() } catch { /* not connected */ }
      gainNode ??= context.createGain()
      gainNode.gain.value = userVolume
      try { source.connect(gainNode) } catch { /* context is unavailable */ }
      try { gainNode.connect(context.destination) } catch { /* context is unavailable */ }
      mediaSource = source
      normalizerNode = null
      outputGain = gainNode
      audio.volume = 1
      webAudioAttached = true
      console.error('normalizer graph failed; using Web Audio bypass', error)
      return
    }
    if (context.state !== 'closed') await context.close()
    audioContext = null
    audio.removeAttribute('crossorigin')
    throw error
  }
}

let audioModeTransition: Promise<void> = Promise.resolve()

// All callers share one transition chain. In particular, the source-preview
// gesture and the Go playback command must never initialise two graphs for
// the same HTMLAudioElement concurrently.
function switchAudioMode(enabled: boolean): Promise<void> {
  const transition = audioModeTransition.then(
    () => switchAudioModeInternal(enabled),
    () => switchAudioModeInternal(enabled),
  )
  audioModeTransition = transition.catch(() => {})
  return transition
}

export function getHTMLAudioVolume(): number {
  return Math.round(userVolume * 100)
}

export function getHTMLAudioLastAudibleVolume(): number {
  return Math.round(lastAudibleVolume * 100)
}

export async function prepareHTMLAudioForLoudnessNormalization() {
  await switchAudioMode(true)
}

function startAudioPlayback(sessionID: number, failureMode: 'error' | 'autoplayblocked') {
  void audio.play().catch(reason => {
    if (disposed || activeSession !== sessionID) return
    if (reason instanceof DOMException && reason.name === 'AbortError') return
    const message = reason instanceof Error ? reason.message : String(reason)
    if (failureMode === 'autoplayblocked' || (reason instanceof DOMException && reason.name === 'NotAllowedError')) {
      report('autoplayblocked', message)
    } else {
      report('error', message)
    }
  })
}

async function execute(command: Command) {
  if (disposed || !command) return
  if (command.command === 'load') {
    activeSession = 0
    audio.pause()
    audio.removeAttribute('src')
    audio.load()
    try {
      await switchAudioMode(command.loudness_normalization === true)
    } catch (error) {
      console.error('loudness normalization initialisation failed; keeping media audible through native playback', error)
    }
    if (disposed) return
    if (command.loudness_normalization) normalizerNode?.port.postMessage({ type: 'reset' })
    activeSession = command.session_id
    userVolume = Math.min(1, Math.max(0, command.volume ?? userVolume))
    if (userVolume > 0) lastAudibleVolume = userVolume
    audio.volume = webAudioAttached ? 1 : userVolume
    if (outputGain) outputGain.gain.value = userVolume
    audio.src = command.resource.url
    const requestedTime = Math.max(0, (command.position_ms ?? 0) / 1000)
    if (requestedTime > 0) audio.currentTime = requestedTime
    audio.load()
    if (command.autoplay !== false) startAudioPlayback(command.session_id, 'error')
    return
  }
  if (command.command === 'volume' && command.session_id === 0) {
    userVolume = Math.min(1, Math.max(0, command.volume ?? userVolume))
    if (userVolume > 0) lastAudibleVolume = userVolume
    if (webAudioAttached && outputGain) outputGain.gain.value = userVolume
    else audio.volume = userVolume
    return
  }
  if (command.session_id !== activeSession) return
  switch (command.command) {
    case 'play':
      startAudioPlayback(activeSession, 'autoplayblocked')
      break
    case 'pause':
      audio.pause()
      break
    case 'stop':
      activeSession = 0
      audio.pause()
      audio.removeAttribute('src')
      audio.load()
      break
    case 'seek': {
      const requestedTime = Math.max(0, (command.position_ms ?? 0) / 1000)
      audio.currentTime = Number.isFinite(audio.duration)
        ? Math.min(audio.duration, requestedTime)
        : requestedTime
      break
    }
    case 'loudness-normalization':
      // The Go bridge reloads the active track with a new transport URL and
      // session so mode changes preserve position and paused/playing state.
      break
    case 'volume':
      userVolume = Math.min(1, Math.max(0, command.volume ?? userVolume))
      if (webAudioAttached && outputGain) outputGain.gain.value = userVolume
      else audio.volume = userVolume
      break
  }
}

let commandQueue: Promise<void> = Promise.resolve()
const unsubscribe = Events.On('music:html-audio:command', event => {
  const command = event.data as Command
  commandQueue = commandQueue
    .then(() => execute(command))
    .catch(error => console.error('HTMLAudio command failed', error))
})

export function disposeHTMLAudioForShutdown() {
  if (disposed) return
  disposed = true
  unsubscribe()
  activeSession = 0
  audio.pause()
  audio.removeAttribute('src')
  audio.load()
  try { mediaSource?.disconnect() } catch { /* already disconnected */ }
  try { normalizerNode?.disconnect() } catch { /* already disconnected */ }
  try { outputGain?.disconnect() } catch { /* already disconnected */ }
  if (audioContext && audioContext.state !== 'closed') void audioContext.close()
  mediaSource = null
  normalizerNode = null
  outputGain = null
  audioContext = null
  webAudioAttached = false
}
