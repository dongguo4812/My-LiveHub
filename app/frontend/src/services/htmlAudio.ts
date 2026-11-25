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
let lastTimeUpdate = 0
let disposed = false
let audioContext: AudioContext | null = null
let mediaSource: MediaElementAudioSourceNode | null = null
let normalizerNode: AudioWorkletNode | null = null
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
  element.addEventListener('timeupdate', () => {
    if (element !== audio) return
    const now = performance.now()
    if (now - lastTimeUpdate < 250) return
    lastTimeUpdate = now
    report('timeupdate', '', element)
  })
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

// Once MediaElementAudioSourceNode is created, that media element cannot be
// returned to native output. Disabling DSP therefore replaces the element,
// rather than leaving a disconnected Web Audio node that would mute playback.
async function switchAudioMode(enabled: boolean) {
  if (enabled && webAudioAttached) {
    if (audioContext?.state === 'suspended') await audioContext.resume()
    return
  }
  if (!enabled && !webAudioAttached) {
    audio.removeAttribute('crossorigin')
    return
  }

  if (!enabled && webAudioAttached) {
    const previous = audio
    const volume = previous.volume
    previous.pause()
    previous.removeAttribute('src')
    previous.load()
    try { mediaSource?.disconnect() } catch { /* already disconnected */ }
    try { normalizerNode?.disconnect() } catch { /* already disconnected */ }
    const oldContext = audioContext
    mediaSource = null
    normalizerNode = null
    audioContext = null
    webAudioAttached = false
    if (oldContext && oldContext.state !== 'closed') await oldContext.close()
    audio = createAudioElement()
    audio.volume = volume
    return
  }

  audio.crossOrigin = 'anonymous'
  const context = new AudioContext({ latencyHint: 'playback' })
  audioContext = context
  let source: MediaElementAudioSourceNode | null = null
  let normalizer: AudioWorkletNode | null = null
  try {
    await context.resume()
    await context.audioWorklet.addModule('/music-loudness-worklet.js')
    normalizer = new AudioWorkletNode(context, 'mylivehub-loudness-normalizer', {
      numberOfInputs: 1,
      numberOfOutputs: 1,
      channelCount: 2,
      channelCountMode: 'explicit',
      outputChannelCount: [2],
    })
    source = context.createMediaElementSource(audio)
    source.connect(normalizer)
    normalizer.connect(context.destination)
    mediaSource = source
    normalizerNode = normalizer
    webAudioAttached = true
  } catch (error) {
    if (source) {
      try { normalizer?.disconnect() } catch { /* not connected */ }
      try { source.connect(context.destination) } catch { /* context is unavailable */ }
      mediaSource = source
      normalizerNode = null
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

export function getHTMLAudioVolume(): number {
  return Math.round(audio.volume * 100)
}

export async function prepareHTMLAudioForLoudnessNormalization() {
  await switchAudioMode(true)
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
    if (command.loudness_normalization) normalizerNode?.port.postMessage({ type: 'reset' })
    activeSession = command.session_id
    audio.volume = Math.min(1, Math.max(0, command.volume ?? 1))
    audio.src = command.resource.url
    const requestedTime = Math.max(0, (command.position_ms ?? 0) / 1000)
    if (requestedTime > 0) audio.currentTime = requestedTime
    audio.load()
    if (command.autoplay !== false) {
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
      try { await audio.play() }
      catch (reason) {
        const message = reason instanceof Error ? reason.message : String(reason)
        report('autoplayblocked', message)
      }
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
      audio.volume = Math.min(1, Math.max(0, command.volume ?? 1))
      break
  }
}

const unsubscribe = Events.On('music:html-audio:command', event => {
  void execute(event.data as Command)
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
  if (audioContext && audioContext.state !== 'closed') void audioContext.close()
  mediaSource = null
  normalizerNode = null
  audioContext = null
  webAudioAttached = false
}
