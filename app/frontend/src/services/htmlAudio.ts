import { Events } from '@wailsio/runtime'
import * as MusicService from '../../bindings/github.com/dongguo4812/My-LiveHub/app/internal/desktop/musicservice.js'

type Resource = { url: string; source_id: string; track_id: string; quality: string }
type Command = {
  command: 'load' | 'play' | 'pause' | 'stop' | 'seek' | 'volume'
  session_id: number
  track: unknown
  resource: Resource
  position_ms?: number
  volume?: number
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
  if (command.session_id !== activeSession) return
  switch (command.command) {
    case 'play':
      try { await audio.play() }
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
    case 'seek':
      if (Number.isFinite(audio.duration)) audio.currentTime = Math.min(audio.duration, Math.max(0, (command.position_ms ?? 0) / 1000))
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
  audio.pause()
  audio.removeAttribute('src')
  audio.load()
}
