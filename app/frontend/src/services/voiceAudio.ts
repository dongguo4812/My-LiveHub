import { ref } from 'vue'
import { Events } from '@wailsio/runtime'
import * as VoiceService from '../../bindings/github.com/dongguo4812/My-LiveHub/app/internal/voice/voiceservice.js'

interface AudioReadyPayload {
  id: string
  text: string
  audio_base64: string
  format: string
}
interface ControlPayload { action: 'pause' | 'resume' | 'stop' }

export const voiceAudioUnlocked = ref(false)
export const voiceAudioPlaying = ref(false)

let context: AudioContext | null = null
let currentSource: AudioBufferSourceNode | null = null
let currentID = ''
let busy = false
let generation = 0
let initialized = false
const queue: AudioReadyPayload[] = []
const unsubscribers: Array<() => void> = []

function acknowledge(id: string) {
  if (id) void VoiceService.CompletePlayback(id).catch(() => undefined)
}

async function playNext() {
  if (!voiceAudioUnlocked.value || !context || context.state !== 'running' || busy || currentSource || queue.length === 0) return
  const item = queue.shift()
  if (!item) return
  const currentGeneration = generation
  busy = true
  try {
    const binary = atob(item.audio_base64)
    const bytes = new Uint8Array(binary.length)
    for (let index = 0; index < binary.length; index++) bytes[index] = binary.charCodeAt(index)
    const audioBuffer = await context.decodeAudioData(bytes.buffer.slice(0) as ArrayBuffer)
    if (currentGeneration !== generation || !context || context.state !== 'running') {
      acknowledge(item.id)
      busy = false
      return
    }
    const source = context.createBufferSource()
    source.buffer = audioBuffer
    source.connect(context.destination)
    currentID = item.id
    currentSource = source
    busy = false
    voiceAudioPlaying.value = true
    source.onended = () => {
      if (currentID !== item.id) return
      currentSource = null
      currentID = ''
      voiceAudioPlaying.value = false
      acknowledge(item.id)
      void playNext()
    }
    source.start()
  } catch {
    busy = false
    acknowledge(item.id)
    void playNext()
  }
}

export function initializeVoiceAudio() {
  if (initialized) return
  initialized = true
  unsubscribers.push(Events.On('voice:audio-ready', (event) => {
    queue.push(event.data as AudioReadyPayload)
    void playNext()
  }))
  unsubscribers.push(Events.On('voice:control', (event) => {
    const action = (event.data as ControlPayload).action
    if (action === 'pause') void pauseVoiceAudio()
    else if (action === 'resume') void resumeVoiceAudio()
    else if (action === 'stop') stopVoiceAudio()
  }))
}

export async function unlockVoiceAudio() {
  try {
    if (!context) context = new AudioContext()
    await context.resume()
    voiceAudioUnlocked.value = context.state === 'running'
    await playNext()
    return voiceAudioUnlocked.value
  } catch {
    voiceAudioUnlocked.value = false
    return false
  }
}

export async function pauseVoiceAudio() {
  if (context?.state === 'running') await context.suspend()
}

export async function resumeVoiceAudio() {
  if (!context) return unlockVoiceAudio()
  try {
    await context.resume()
    await playNext()
  } catch {
    voiceAudioUnlocked.value = false
  }
}

export function stopVoiceAudio() {
  generation++
  queue.splice(0)
  const id = currentID
  const source = currentSource
  currentID = ''
  currentSource = null
  busy = false
  voiceAudioPlaying.value = false
  if (source) {
    source.onended = null
    try { source.stop() } catch { /* The source may already have ended. */ }
  }
  acknowledge(id)
}

export function disposeVoiceAudio() {
  for (const unsubscribe of unsubscribers.splice(0)) unsubscribe()
  stopVoiceAudio()
  void context?.close()
  context = null
  voiceAudioUnlocked.value = false
  initialized = false
}
