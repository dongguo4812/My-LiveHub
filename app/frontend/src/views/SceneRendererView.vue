<script setup lang="ts">
import { computed, onMounted, onUnmounted, onUpdated, ref } from 'vue'
import { useRoute } from 'vue-router'

type Layer = { id: string; type: string; name: string; x: number; y: number; width: number; height: number; z_index: number; visible: boolean; opacity: number; text?: string; url?: string; color?: string; font_size?: number; queue_display_count?: number; gift_display_duration?: number; super_chat_display_duration?: number; live2d_scale?: number; live2d_x?: number; live2d_y?: number; emoticon_size?: number; emoticon_speed?: number; emoticon_drift?: number; emoticon_max_count?: number; content_scale?: number; request_notice_display_duration?: number }
type Scene = { id: string; name: string; width: number; height: number; background: string; layers: Layer[] }
type EmoticonToken = { text: string; url: string; cache_url?: string; width?: number; height?: number }
type LiveEvent = { id: number; type: string; user_name: string; content: string; gift_name?: string; gift_count?: number; gift_image_url?: string; gift_image_cache_url?: string; gift_animation_url?: string; gift_animation_cache_url?: string; amount?: number; timestamp: number; emoticons?: EmoticonToken[] }
type GiftDisplayItem = { id: string; user_name: string; gift_name: string; gift_count: number; gift_image_url?: string; gift_image_cache_url?: string; gift_animation_url?: string; gift_animation_cache_url?: string; timestamp: number }
type FloatingEmoticon = { id: number; layerId: string; url: string; cacheURL?: string; x: number; size: number; drift: number; duration: number }
type Track = { id: string; source_id: string; title: string; artist?: string; album?: string; duration_ms: number; cover_url?: string }
type Playback = { state: string; track?: Track; position_ms: number; duration_ms: number }
type MusicQueueItem = { id: string; track: Track; requested_by?: string }
type MusicQueue = { current?: MusicQueueItem; items: MusicQueueItem[]; capacity: number }
type VideoQueueItem = { id: number; bvid: string; title: string; duration_seconds: number; requester: string }
type VideoQueue = { enabled: boolean; running: boolean; current?: VideoQueueItem; items: VideoQueueItem[]; queue_count: number }
type Live2DDisplay = { enabled: boolean; runtime_version: 'moc2' | 'moc3'; model_url: string; model_scale: number; model_offset_x: number; model_offset_y: number }
type Live2DWindow = Window & { live2dSetModelTransform?: (scale: number, x: number, y: number) => boolean; live2dSetMouthOpen?: (value: number) => void }
type MusicDisplayConfig = { enabled: boolean; command_enabled: boolean; command_prefix: string; skip_command_enabled: boolean; skip_command: string; require_fan_medal: boolean; fan_medal_level: number }
type RequestNotice = { id: number; kind: 'music_request_success' | 'music_request_failed' | 'music_skip_success' | 'music_skip_failed' | 'video_request_success' | 'video_request_failed' | 'video_skip_success' | 'video_skip_failed'; user_name: string; item_name?: string; reason?: string; timestamp: number }
const route = useRoute()
const scene = ref<Scene | null>(null)
const lolHexFrameURLs = ref<Record<string, string>>({})
const lolHexReadyChecks = new Map<string, string>()
let sceneRendererUnmounted = false
async function waitForLolHexService(layerId: string, pageURL: string) {
  if (lolHexReadyChecks.get(layerId) === pageURL) return
  lolHexReadyChecks.set(layerId, pageURL)
  const healthURL = new URL('/api/health', pageURL).toString()
  while (!sceneRendererUnmounted && lolHexReadyChecks.get(layerId) === pageURL) {
    let ready = false
    try {
      const response = await fetch(healthURL, { cache: 'no-store', mode: 'cors' })
      if (response.ok) {
        const status = await response.json() as { ready?: boolean }
        ready = status.ready === true
      }
    } catch { /* LOL Hex 未启动或已关闭，隐藏对应展示并继续等待启动。 */ }

    if (ready) {
      if (lolHexFrameURLs.value[layerId] !== pageURL) {
        lolHexFrameURLs.value = { ...lolHexFrameURLs.value, [layerId]: pageURL }
      }
    } else if (lolHexFrameURLs.value[layerId]) {
      const next = { ...lolHexFrameURLs.value }
      delete next[layerId]
      lolHexFrameURLs.value = next
    }

    await new Promise(resolve => setTimeout(resolve, 1200))
  }
}
function lolHexDisplayURL(layer: Layer) {
  const url = new URL(lolHexFrameURLs.value[layer.id])
  url.searchParams.set('content_scale', String(Math.max(25, Math.min(300, layer.content_scale ?? 100))))
  return url.toString()
}
function prepareLolHexFrames(currentScene: Scene) {
  const activeIDs = new Set<string>()
  for (const layer of currentScene.layers) {
    if (!layer.visible || (layer.type !== 'lol_hex_display' && layer.type !== 'lol_hex_bar') || !layer.url) continue
    activeIDs.add(layer.id)
    void waitForLolHexService(layer.id, layer.url)
  }
  for (const id of Object.keys(lolHexFrameURLs.value)) {
    if (!activeIDs.has(id)) {
      const next = { ...lolHexFrameURLs.value }
      delete next[id]
      lolHexFrameURLs.value = next
      lolHexReadyChecks.delete(id)
    }
  }
}
const liveEvents = ref<LiveEvent[]>([])
const floatingEmoticons = ref<FloatingEmoticon[]>([])
let nextFloatingEmoticonID = 0
const now = ref(new Date())
const error = ref('')
const playback = ref<Playback | null>(null)
const musicQueue = ref<MusicQueue>({ items: [], capacity: 0 })
const videoQueue = ref<VideoQueue>({ enabled: false, running: false, items: [], queue_count: 0 })
const live2DDisplay = ref<Live2DDisplay | null>(null)
const live2DFrameURL = computed(() => {
  const value = live2DDisplay.value
  if (!value?.enabled || !value.model_url) return ''
  const params = new URLSearchParams({ model: value.model_url, scale: String(value.model_scale), x: String(value.model_offset_x), y: String(value.model_offset_y) })
  return value.runtime_version === 'moc2' ? '/live2d-runtime/moc2/index.html?' + params : '/live2d-runtime/moc3/index.html?model=' + encodeURIComponent(value.model_url)
})
const musicDisplayConfig = ref<MusicDisplayConfig | null>(null)
const musicError = ref('')
const currentRequestNotices = ref<Record<string, RequestNotice[]>>({})
const requestNoticeTimers = new Map<string, ReturnType<typeof setTimeout>>()
let requestNoticeSource: EventSource | undefined
const musicAccentColor = ref('#A83232')
const waveBars = Array.from({ length: 31 }, (_, index) => ({ height: 7 + Math.abs(Math.sin(index * 1.71) * 24), speed: 0.45 + (index % 7) * 0.13, delay: -(Math.random() * 1.8) }))
const musicColors = ['#FF3B30', '#FF7A00', '#FFD000', '#39D353', '#00D6C9', '#00A6FF', '#4169FF', '#9B5CFF', '#FF4FA3', '#FF6B6B']
let source: EventSource | undefined
let clockTimer: ReturnType<typeof setInterval> | undefined
let sceneRefreshTimer: ReturnType<typeof setInterval> | undefined
let musicRefreshTimer: ReturnType<typeof setInterval> | undefined
let musicQueueRefreshTimer: ReturnType<typeof setInterval> | undefined
let videoQueueRefreshTimer: ReturnType<typeof setInterval> | undefined
let musicConfigRefreshTimer: ReturnType<typeof setInterval> | undefined
let live2DRefreshTimer: ReturnType<typeof setInterval> | undefined
let musicTrackKey = ''
let musicRefreshing = false
let mouthSyncChannel: BroadcastChannel | null = null
let mouthSyncPollTimer: ReturnType<typeof setInterval> | undefined
let currentMouthOpen = 0
async function pollMouthSync() {
  if (!live2DDisplay.value?.enabled) {
    currentMouthOpen = 0
    document.querySelectorAll<HTMLIFrameElement>('.live2d-output-frame').forEach(frame => {
      (frame.contentWindow as Live2DWindow | null)?.live2dSetMouthOpen?.(0)
    })
    return
  }
  try {
    const response = await fetch('/api/live2d/mouth', { cache: 'no-store' })
    if (!response.ok) return
    const data = await response.json() as { value?: number }
    const value = Math.max(0, Math.min(1, Number(data.value) || 0))
    if (Math.abs(value - currentMouthOpen) < 0.005) return
    currentMouthOpen = value
    document.querySelectorAll<HTMLIFrameElement>('.live2d-output-frame').forEach(frame => {
      (frame.contentWindow as Live2DWindow | null)?.live2dSetMouthOpen?.(currentMouthOpen)
    })
  } catch { /* Keep the last received mouth value while the local relay reconnects. */ }
}
function handleMouthSyncMessage(event: MessageEvent) {
  if (event.data?.type !== 'live2d-mouth-open') return
  if (!live2DDisplay.value?.enabled) {
    currentMouthOpen = 0
    return
  }
  currentMouthOpen = Math.max(0, Math.min(1, Number(event.data.value) || 0))
  document.querySelectorAll<HTMLIFrameElement>('.live2d-output-frame').forEach(frame => {
    (frame.contentWindow as Live2DWindow | null)?.live2dSetMouthOpen?.(currentMouthOpen)
  })
}

const danmaku = computed(() => liveEvents.value.filter(item => item.type === 'danmaku').slice(-8).reverse())
function giftEvents(layer: Layer): GiftDisplayItem[] {
  const duration = Math.max(1, Math.min(120, layer.gift_display_duration || 8)) * 1000
  const currentTime = now.value.getTime()
  const events = liveEvents.value
    .filter(item => item.type === 'gift')
    .slice()
    .sort((a, b) => a.timestamp - b.timestamp)
  const grouped = new Map<string, GiftDisplayItem>()
  for (const event of events) {
    const giftName = event.gift_name || '礼物'
    const imageKey = event.gift_animation_url || event.gift_image_url || event.gift_animation_cache_url || event.gift_image_cache_url || ''
    const key = JSON.stringify([event.user_name, giftName, imageKey])
    const existing = grouped.get(key)
    if (existing && event.timestamp - existing.timestamp <= duration) {
      existing.gift_count += Math.max(1, event.gift_count || 1)
      existing.timestamp = event.timestamp
    } else {
      grouped.set(key, {
        id: key + ':' + event.id,
        user_name: event.user_name,
        gift_name: giftName,
        gift_count: Math.max(1, event.gift_count || 1),
        gift_image_url: event.gift_image_url,
        gift_image_cache_url: event.gift_image_cache_url,
        gift_animation_url: event.gift_animation_url,
        gift_animation_cache_url: event.gift_animation_cache_url,
        timestamp: event.timestamp,
      })
    }
  }
  return [...grouped.values()]
    .filter(item => currentTime - item.timestamp <= duration)
    .sort((a, b) => a.timestamp - b.timestamp)
}
function giftNeedsScroll(layer: Layer, items: GiftDisplayItem[]) {
  if (!scene.value || items.length < 2) return false
  const cardWidth = scene.value.height * 0.3
  const gap = scene.value.height * 0.005
  return items.length * (cardWidth + gap) - gap > layer.width
}
function giftScrollDuration(items: GiftDisplayItem[]) {
  return Math.max(8, items.length * 4) + 's'
}
function superChatEvents(layer: Layer) {
  const duration = Math.max(1, Math.min(120, layer.super_chat_display_duration || 8)) * 1000
  const currentTime = now.value.getTime()
  return liveEvents.value
    .filter(item => item.type === 'super_chat' && currentTime - item.timestamp <= duration)
}
const progress = computed(() => {
  const duration = playback.value?.duration_ms || playback.value?.track?.duration_ms || 0
  if (!duration) return 0
  return Math.max(0, Math.min(100, (playback.value?.position_ms || 0) / duration * 100))
})
function formatTime(value: number) {
  const seconds = Math.max(0, Math.floor(value / 1000))
  return Math.floor(seconds / 60) + ':' + String(seconds % 60).padStart(2, '0')
}
function layerStyle(layer: Layer) {
  const current = scene.value
  if (!current) return {}
  const style: Record<string, string | number> = {
    left: (layer.x / current.width * 100) + '%',
    top: (layer.y / current.height * 100) + '%',
    width: (layer.width / current.width * 100) + '%',
    height: (layer.height / current.height * 100) + '%',
    zIndex: layer.z_index,
    opacity: layer.opacity,
    color: layer.color || '#ffffff',
    fontSize: ((layer.font_size || 24) / current.height * 100) + 'cqh',
  }
  if (layer.type === 'request_notice') {
    style.top = 'auto'
    style.bottom = ((current.height - layer.y - layer.height) / current.height * 100) + '%'
    style.width = 'max-content'
    style.maxWidth = (layer.width / current.width * 100) + '%'
    style.height = 'auto'
  }
  return style
}
function formatClock() { return now.value.toLocaleTimeString('zh-CN', { hour12: false }) }
function removeFloatingEmoticon(id: number) {
  floatingEmoticons.value = floatingEmoticons.value.filter(item => item.id !== id)
}
function spawnFloatingEmoticons(event: LiveEvent) {
  if (event.type !== 'danmaku' || !event.emoticons?.length || !scene.value) return
  for (const layer of scene.value.layers) {
    if (layer.type !== 'emoticon' || !layer.visible || layer.width <= 0 || layer.height <= 0) continue
    const maxCount = Math.max(1, Math.min(40, layer.emoticon_max_count || 12))
    const configuredSize = Math.max(24, Math.min(160, layer.emoticon_size || 64))
    const size = Math.max(16, Math.min(configuredSize, layer.width * 0.4, layer.height * 0.4))
    const driftLimit = Math.max(0, Math.min(120, layer.emoticon_drift ?? 48, Math.max(0, (layer.width - size) / 2)))
    const speed = Math.max(20, Math.min(300, layer.emoticon_speed || 90))
    const duration = Math.max(1, Math.min(30, layer.height * 1.25 / speed))
    const incoming: FloatingEmoticon[] = []
    for (const token of event.emoticons) {
      if (!token.url || !token.text || !event.content.includes(token.text)) continue
      const occurrences = Math.min(8, event.content.split(token.text).length - 1)
      for (let index = 0; index < occurrences; index++) {
        const minX = size / 2 + driftLimit
        const maxX = Math.max(minX, layer.width - size / 2 - driftLimit)
        incoming.push({
          id: ++nextFloatingEmoticonID,
          layerId: layer.id,
          url: token.url,
          cacheURL: token.cache_url,
          x: minX + Math.random() * (maxX - minX),
          size,
          drift: driftLimit ? Math.random() * driftLimit : 0,
          duration,
        })
      }
    }
    if (!incoming.length) continue
    const existing = floatingEmoticons.value.filter(item => item.layerId === layer.id)
    const keep = floatingEmoticons.value.filter(item => item.layerId !== layer.id)
    const combined = [...existing, ...incoming].slice(-maxCount)
    floatingEmoticons.value = [...keep, ...combined]
  }
}
function handleRequestNotice(event: MessageEvent<string>) {
  try {
    const notice = JSON.parse(event.data) as RequestNotice
    for (const layer of scene.value?.layers || []) {
      if (layer.type !== 'request_notice' || !layer.visible) continue
      const notices = currentRequestNotices.value[layer.id] || (currentRequestNotices.value[layer.id] = [])
      notices.push(notice)
      const timerKey = layer.id + ':' + notice.id + ':' + notice.timestamp
      const duration = Math.max(1, Math.min(120, layer.request_notice_display_duration || 5)) * 1000
      requestNoticeTimers.set(timerKey, setTimeout(() => {
        currentRequestNotices.value[layer.id] = (currentRequestNotices.value[layer.id] || []).filter(item => item !== notice)
        requestNoticeTimers.delete(timerKey)
      }, duration))
    }
  } catch { /* Ignore malformed request notice frames. */ }
}
function requestNoticeText(notice: RequestNotice) {
  const music = notice.kind.startsWith('music')
  const icon = music ? '🎵' : '🎬'
  const user = '「' + (notice.user_name || '未知观众') + '」'
  switch (notice.kind) {
    case 'music_request_success': return icon + user + '点播成功：《' + (notice.item_name || '歌曲') + '》'
    case 'music_request_failed': return icon + user + '点播失败：' + (notice.reason || '未知原因')
    case 'music_skip_success': return '🎵' + user + '音乐切换成功'
    case 'music_skip_failed': return '🎵' + user + '音乐切换失败：' + (notice.reason || '未知原因')
    case 'video_request_success': return '🎬' + user + '点播成功：《' + (notice.item_name || '视频') + '》'
    case 'video_request_failed': return '🎬' + user + '点播失败：' + (notice.reason || '未知原因')
    case 'video_skip_success': return '🎬' + user + '视频切换成功'
    case 'video_skip_failed': return '🎬' + user + '视频切换失败：' + (notice.reason || '未知原因')
  }
}
function handleEvent(event: MessageEvent<string>) {
  try {
    const value = JSON.parse(event.data) as LiveEvent
    if (liveEvents.value.some(item => item.id === value.id)) return
    spawnFloatingEmoticons(value)
    liveEvents.value.push(value)
    liveEvents.value.sort((a, b) => a.id - b.id)
    if (liveEvents.value.length > 100) liveEvents.value.splice(0, liveEvents.value.length - 100)
  } catch { /* Ignore malformed event frames. */ }
}
async function loadRecentEvents() {
  try {
    const response = await fetch('/api/events/recent', { cache: 'no-store' })
    if (!response.ok) return
    const items = await response.json() as LiveEvent[]
    const known = new Set(liveEvents.value.map(item => item.id))
    liveEvents.value = [...items.filter(item => !known.has(item.id)), ...liveEvents.value].sort((a, b) => a.id - b.id).slice(-100)
  } catch { /* The live stream remains available if history is unavailable. */ }
}
async function loadScene() {
  const id = encodeURIComponent(String(route.params.sceneID ?? ''))
  try {
    const response = await fetch('/api/scenes/' + id, { cache: 'no-store' })
    if (!response.ok) throw new Error('场景不存在或浏览器源服务不可用')
    scene.value = await response.json() as Scene
    prepareLolHexFrames(scene.value)
    error.value = ''
    document.title = scene.value.name + ' - My-LiveHub'
    document.documentElement.style.background = 'transparent'
    document.body.style.background = 'transparent'
    const root = document.getElementById('app')
    if (root) root.style.background = 'transparent'
  } catch (cause) { error.value = cause instanceof Error ? cause.message : String(cause) }
}
function isAudienceRequest(value?: string) {
  return Boolean(value?.trim())
    && value !== '__my_livehub_search_play__'
    && value !== '__my_livehub_host_playlist__'
}
const audienceCurrent = computed(() => isAudienceRequest(musicQueue.value.current?.requested_by) ? musicQueue.value.current : null)
const audienceQueueItems = computed(() => musicQueue.value.items.filter(item => isAudienceRequest(item.requested_by)))
function audienceQueueDisplayItems(layer: Layer) {
  return audienceQueueItems.value.slice(0, Math.max(1, layer.queue_display_count || 10))
}
async function loadLive2DDisplay() {
  try {
    const response = await fetch('/api/live2d', { cache: 'no-store' })
    if (response.ok) {
      live2DDisplay.value = await response.json() as Live2DDisplay
      if (!live2DDisplay.value.enabled) {
        currentMouthOpen = 0
        document.querySelectorAll<HTMLIFrameElement>('.live2d-output-frame').forEach(frame => {
          (frame.contentWindow as Live2DWindow | null)?.live2dSetMouthOpen?.(0)
        })
      }
    }
  } catch { /* Keep the last known Live2D configuration. */ }
}
function applyLive2DFrameTransforms() {
  const value = live2DDisplay.value
  if (!value) return
  document.querySelectorAll<HTMLIFrameElement>('.live2d-output-frame').forEach(frame => {
    const layer = scene.value?.layers.find(item => item.id === frame.dataset.layerId)
    if (!layer) return
    const target = frame.contentWindow as Live2DWindow | null
    target?.live2dSetModelTransform?.(
      value.model_scale * (layer.live2d_scale || 100) / 100,
      value.model_offset_x / 100 + (layer.live2d_x || 0) / 100,
      value.model_offset_y / 100 + (layer.live2d_y || 0) / 100,
    )
  })
}
function handleLive2DMessage(event: MessageEvent) {
  if (event.data?.type === 'moc3-model-loaded' || event.data?.type === 'moc2-model-loaded') {
    applyLive2DFrameTransforms()
    const frame = Array.from(document.querySelectorAll<HTMLIFrameElement>('.live2d-output-frame')).find(item => item.contentWindow === event.source)
    ;(frame?.contentWindow as Live2DWindow | null)?.live2dSetMouthOpen?.(currentMouthOpen)
  }
}
async function loadMusicDisplayConfig() {
  try {
    const response = await fetch('/api/music/config', { cache: 'no-store' })
    if (!response.ok) return
    musicDisplayConfig.value = await response.json() as MusicDisplayConfig
  } catch { /* Keep the last known command settings. */ }
}
function videoQueueDisplayItems(layer: Layer) {
  return videoQueue.value.items.slice(0, Math.max(1, layer.queue_display_count || 10))
}
async function loadVideoQueue() {
  try {
    const response = await fetch('/api/video/queue', { cache: 'no-store' })
    if (!response.ok) return
    const data = await response.json() as Partial<VideoQueue>
    videoQueue.value = {
      enabled: data.enabled === true,
      running: data.running === true,
      current: data.current,
      items: Array.isArray(data.items) ? data.items : [],
      queue_count: Number.isFinite(data.queue_count) ? Number(data.queue_count) : 0,
    }
  } catch { /* Keep the last known video queue while the local source reconnects. */ }
}
async function loadMusicQueue() {
  try {
    const response = await fetch('/api/music/queue', { cache: 'no-store' })
    if (!response.ok) return
    musicQueue.value = await response.json() as MusicQueue
  } catch { /* Keep the last known queue while the local source reconnects. */ }
}
async function loadMusic() {
  if (musicRefreshing) return
  musicRefreshing = true
  try {
    const response = await fetch('/api/music', { cache: 'no-store' })
    if (!response.ok) throw new Error('音乐播放状态暂不可用')
    const snapshot = await response.json() as Playback
    playback.value = snapshot
    const track = snapshot.track
    const key = track ? track.source_id + ':' + track.id : ''
    if (key !== musicTrackKey) {
      musicTrackKey = key
      if (track) {
        const nextColors = musicColors.filter(color => color !== musicAccentColor.value)
        musicAccentColor.value = nextColors[Math.floor(Math.random() * nextColors.length)] || musicColors[0]
      }
      musicError.value = ''
    }
  } catch (cause) {
    musicError.value = cause instanceof Error ? cause.message : String(cause)
  } finally { musicRefreshing = false }
}
onMounted(() => {
  mouthSyncChannel = typeof BroadcastChannel !== 'undefined' ? new BroadcastChannel('my-livehub-live2d-mouth') : null
  if (mouthSyncChannel) mouthSyncChannel.onmessage = handleMouthSyncMessage
  void loadScene()
  void loadMusic()
  void loadMusicQueue()
  void loadVideoQueue()
  void loadMusicDisplayConfig()
  void loadLive2DDisplay()
  live2DRefreshTimer = setInterval(() => { void loadLive2DDisplay() }, 3000)
  mouthSyncPollTimer = setInterval(() => { void pollMouthSync() }, 33)
  void pollMouthSync()
  window.addEventListener('message', handleLive2DMessage)
  sceneRefreshTimer = setInterval(() => { void loadScene() }, 1000)
  musicRefreshTimer = setInterval(() => { void loadMusic() }, 500)
  musicQueueRefreshTimer = setInterval(() => { void loadMusicQueue() }, 1000)
  videoQueueRefreshTimer = setInterval(() => { void loadVideoQueue() }, 1000)
  musicConfigRefreshTimer = setInterval(() => { void loadMusicDisplayConfig() }, 3000)
  source = new EventSource('/api/events')
  source.onmessage = handleEvent
  requestNoticeSource = new EventSource('/api/request-notices')
  requestNoticeSource.onmessage = handleRequestNotice
  source.onerror = () => { error.value = '直播事件连接中断，正在自动重连…' }
  source.onopen = () => { error.value = ''; void loadRecentEvents() }
  clockTimer = setInterval(() => { now.value = new Date() }, 500)
})
onUpdated(() => { applyLive2DFrameTransforms() })
onUnmounted(() => {
  sceneRendererUnmounted = true
  mouthSyncChannel?.close()
  mouthSyncChannel = null
  if (mouthSyncPollTimer) clearInterval(mouthSyncPollTimer)
  source?.close()
  requestNoticeSource?.close()
  for (const timer of requestNoticeTimers.values()) clearTimeout(timer)
  requestNoticeTimers.clear()
  if (clockTimer) clearInterval(clockTimer)
  if (sceneRefreshTimer) clearInterval(sceneRefreshTimer)
  if (musicRefreshTimer) clearInterval(musicRefreshTimer)
  if (musicQueueRefreshTimer) clearInterval(musicQueueRefreshTimer)
  if (videoQueueRefreshTimer) clearInterval(videoQueueRefreshTimer)
  if (musicConfigRefreshTimer) clearInterval(musicConfigRefreshTimer)
  if (live2DRefreshTimer) clearInterval(live2DRefreshTimer)
  window.removeEventListener('message', handleLive2DMessage)
})
</script>

<template>
  <main v-if="scene" class="scene-output" :style="{ aspectRatio: scene.width + ' / ' + scene.height, background: scene.background === 'transparent' ? 'transparent' : scene.background }">
    <div v-for="layer in scene.layers.filter(item => item.visible && (item.type !== 'music_queue' || musicDisplayConfig?.command_enabled === true) && (item.type !== 'video_queue' || (videoQueue.running === true && videoQueue.enabled === true)) && (item.type !== 'live2d' || Boolean(live2DFrameURL)))" :key="layer.id" class="output-layer" :class="{ 'request-notice-output-layer': layer.type === 'request_notice', 'music-output-layer': layer.type === 'music', 'music-queue-output-layer': layer.type === 'music_queue', 'video-queue-output-layer': layer.type === 'video_queue', 'emoticon-output-layer': layer.type === 'emoticon' }" :style="layerStyle(layer)">
      <div v-if="layer.type === 'request_notice'" class="request-notice-stack">
        <div v-for="notice in currentRequestNotices[layer.id] || []" :key="notice.id + ':' + notice.timestamp" class="request-notice-widget" :class="{ 'is-failed': notice.kind.endsWith('_failed') }" :style="{ color: notice.kind.endsWith('_failed') ? '#ffb2b2' : (layer.color || '#a7f3c0') }">{{ requestNoticeText(notice) }}</div>
      </div>
      <span v-else-if="layer.type === 'text'">{{ layer.text }}</span>
      <img v-else-if="layer.type === 'image' && layer.url" :src="layer.url" alt="" />
      <iframe v-else-if="(layer.type === 'lol_hex_display' || layer.type === 'lol_hex_bar') && lolHexFrameURLs[layer.id]" class="lol-hex-output-frame" :src="lolHexDisplayURL(layer)" :title="layer.name" :style="{ width: '100%', height: '100%', border: '0', background: 'transparent', pointerEvents: 'none' }" />
      <time v-else-if="layer.type === 'clock'">{{ formatClock() }}</time>
      <div v-else-if="layer.type === 'emoticon'" class="floating-emoticon-layer">
        <img
          v-for="particle in floatingEmoticons.filter(item => item.layerId === layer.id)"
          :key="particle.id"
          class="floating-emoticon"
          :src="particle.cacheURL || particle.url"
          alt=""
          referrerpolicy="no-referrer"
          :style="{ left: particle.x + 'px', width: particle.size + 'px', height: particle.size + 'px', '--emoticon-drift': particle.drift + 'px', '--emoticon-duration': particle.duration + 's' }"
          @animationend="removeFloatingEmoticon(particle.id)"
        />
      </div>
      <iframe v-else-if="layer.type === 'live2d' && live2DFrameURL" class="live2d-output-frame" :data-layer-id="layer.id" :src="live2DFrameURL" title="Live2D" allowtransparency="true" :style="{ backgroundColor: 'transparent' }" />
      <div v-else-if="layer.type === 'danmaku'" class="event-stack">
        <div v-for="item in danmaku" :key="item.id" class="event-line"><strong>{{ item.user_name }}</strong><span>{{ item.content }}</span></div>
      </div>
      <div v-else-if="layer.type === 'gift'" class="gift-event-stack">
        <div class="gift-event-track" :class="{ 'is-scrolling': giftNeedsScroll(layer, giftEvents(layer)) }" :style="{ '--gift-scroll-duration': giftScrollDuration(giftEvents(layer)) }">
          <template v-for="(item, index) in (giftNeedsScroll(layer, giftEvents(layer)) ? [...giftEvents(layer), ...giftEvents(layer)] : giftEvents(layer))" :key="item.id + '-' + index">
            <article class="gift-event-card">
              <img v-if="item.gift_animation_cache_url || item.gift_animation_url || item.gift_image_cache_url || item.gift_image_url"
                class="gift-event-image"
                :src="item.gift_animation_cache_url || item.gift_animation_url || item.gift_image_cache_url || item.gift_image_url"
                :alt="item.gift_name"
                referrerpolicy="no-referrer" />
              <div v-else class="gift-event-image-fallback">🎁</div>
              <div class="gift-event-caption"><strong>{{ item.gift_name }} × {{ item.gift_count }}</strong><span>{{ item.user_name }} 赠送</span></div>
            </article>
          </template>
        </div>
      </div>
      <div v-else-if="layer.type === 'super_chat'" class="event-stack sc-event-stack">
        <div v-for="item in superChatEvents(layer)" :key="item.id" class="event-line sc-line"><strong>{{ item.user_name }} · ¥{{ Number(item.amount || 0).toFixed(2) }}</strong><span>{{ item.content }}</span></div>
      </div>
      <section v-else-if="layer.type === 'video_queue'" class="music-queue-widget video-queue-widget" :style="{ '--queue-accent': layer.color || '#9fc0ff', fontSize: ((layer.font_size || 22) / (scene?.height || 1080) * 100) + 'cqh' }">
        <section class="music-queue-section music-queue-prompt">
          <div class="music-queue-prompt-single">
            <strong class="music-queue-prompt-title"><span class="music-queue-heading-mark">▸</span> 视频点播指令</strong>
            <span><b>点播</b> BV号</span>
          </div>
        </section>
        <section class="music-queue-section music-queue-current-section">
          <div class="music-queue-item music-queue-current music-queue-current-inline music-queue-current-stacked">
            <strong class="music-queue-current-label"><span class="music-queue-heading-mark">▶</span> 当前播放</strong>
            <span v-if="videoQueue.current" class="music-queue-current-track">
              <strong class="music-queue-current-title">{{ videoQueue.current.title || videoQueue.current.bvid }}</strong>
              <span class="music-queue-current-artist">{{ videoQueue.current.bvid }}</span>
            </span>
            <span v-else class="music-queue-empty">暂无点播视频</span>
            <span v-if="videoQueue.current" class="music-queue-requester">{{ videoQueue.current.requester }}</span>
          </div>
        </section>
        <section class="music-queue-section music-queue-pending">
          <strong class="music-queue-heading"><span class="music-queue-heading-mark">Ⅱ</span> 待播队列 <span class="music-queue-count">{{ videoQueue.queue_count }} 个</span></strong>
          <div v-if="videoQueue.items.length" class="music-queue-list">
            <div v-for="(item, index) in videoQueueDisplayItems(layer)" :key="item.id" class="music-queue-item">
              <span class="music-queue-index">{{ String(index + 1).padStart(2, '0') }}</span>
              <span class="music-queue-track"><strong>{{ item.title || item.bvid }}</strong><small>{{ item.bvid }}</small></span>
              <span class="music-queue-requester">{{ item.requester }}</span>
            </div>
          </div>
          <div v-else class="music-queue-empty">暂无待播视频</div>
        </section>
      </section>
      <section v-else-if="layer.type === 'music_queue'" class="music-queue-widget" :style="{ '--queue-accent': layer.color || '#9fc0ff', fontSize: ((layer.font_size || 22) / (scene?.height || 1080) * 100) + 'cqh' }">        <section v-if="musicDisplayConfig?.enabled && musicDisplayConfig.command_enabled" class="music-queue-section music-queue-prompt">
          <div class="music-queue-prompt-single">
            <strong class="music-queue-prompt-title"><span class="music-queue-heading-mark">♪</span> 歌曲点播指令</strong>
            <span><b>{{ musicDisplayConfig.command_prefix }}</b> 歌曲名称</span>
          </div>
        </section>
        <section class="music-queue-section music-queue-current-section">
          <div v-if="audienceCurrent" class="music-queue-item music-queue-current music-queue-current-inline music-queue-current-stacked">
            <strong class="music-queue-current-label"><span class="music-queue-heading-mark">♫</span> 当前播放</strong>
            <span class="music-queue-current-track">
              <strong class="music-queue-current-title">{{ audienceCurrent.track.title }}</strong>
              <span class="music-queue-current-artist">{{ audienceCurrent.track.artist || '未知歌手' }}</span>
            </span>
            <span class="music-queue-requester">{{ audienceCurrent.requested_by }}</span>
          </div>
          <div v-else class="music-queue-item music-queue-current music-queue-current-inline music-queue-current-stacked">
            <strong class="music-queue-current-label"><span class="music-queue-heading-mark">♫</span> 当前播放</strong>
            <span class="music-queue-empty">暂无点播歌曲</span>
          </div>
        </section>
        <section class="music-queue-section music-queue-pending">
          <strong class="music-queue-heading"><span class="music-queue-heading-mark">Ⅱ</span> 待播队列 <span class="music-queue-count">{{ audienceQueueItems.length }} 首</span></strong>
          <div v-if="audienceQueueItems.length" class="music-queue-list">
            <div v-for="(item, index) in audienceQueueDisplayItems(layer)" :key="item.id" class="music-queue-item">
              <span class="music-queue-index">{{ String(index + 1).padStart(2, '0') }}</span>
              <span class="music-queue-track"><strong>{{ item.track.title }}</strong><small>{{ item.track.artist || '未知歌手' }}</small></span>
              <span class="music-queue-requester">{{ item.requested_by }}</span>
            </div>
          </div>
          <div v-else class="music-queue-empty">暂无待播歌曲</div>
        </section>
      </section>
      <section v-else-if="layer.type === 'music' && playback?.state === 'playing' && playback?.track" class="music-widget" :style="{ '--random-color': musicAccentColor, '--details-bg': playback.track.cover_url ? 'url(' + JSON.stringify(playback.track.cover_url) + ')' : 'none' }">
        <div class="music-main">
          <div class="music-cover-wrap">
            <div class="music-cover-blur" :style="{ backgroundImage: playback.track.cover_url ? 'url(' + JSON.stringify(playback.track.cover_url) + ')' : 'none' }"></div>
            <div v-if="!playback.track.cover_url" class="music-cover-fallback">♫</div>
            <img v-if="playback.track.cover_url" class="music-cover" :src="playback.track.cover_url" alt="" referrerpolicy="no-referrer" />
          </div>
          <div class="music-details">
            <div class="music-song-info">
              <div class="music-song-title">{{ playback.track.title }}</div>
              <div class="music-song-author">{{ playback.track.artist || '未知歌手' }}</div>
            </div>
            <div class="music-song-time">
              <span class="music-time">{{ formatTime(playback.position_ms) }}</span>
              <div class="music-wave" aria-hidden="true">
                <i v-for="(bar, index) in waveBars" :key="index" class="music-wavebar" :style="{ '--h': bar.height + 'px', '--speed': bar.speed + 's', '--delay': bar.delay + 's' }"></i>
              </div>
              <span class="music-time">{{ formatTime(playback.duration_ms || playback.track.duration_ms) }}</span>
            </div>
          </div>
        </div>
      </section>
    </div>
  </main>
  <main v-else class="scene-error">{{ error || '正在加载场景…' }}</main>
</template>

<style>
html,body,#app{width:100%;height:100%;margin:0;overflow:hidden;background:transparent!important}
body{color:#fff;font-family:Inter,"Segoe UI","Microsoft YaHei",sans-serif}
.scene-output{position:relative;width:100vw;height:100vh;overflow:hidden;container-type:size;background:transparent}
.request-notice-output-layer{width:max-content!important;height:auto!important;min-width:0;min-height:0;overflow:visible;align-items:flex-start;justify-content:flex-end;white-space:normal;text-shadow:none}.request-notice-stack{display:flex;flex-direction:column;align-items:flex-start;justify-content:flex-end;gap:.35em;width:max-content;height:auto;max-width:100%;overflow:visible}.request-notice-widget{display:block;flex:none;box-sizing:border-box;width:max-content;height:auto;max-width:100%;padding:.5em .8em;border:1px solid rgba(120,220,160,.6);border-radius:.55em;background:rgba(12,24,20,.94);color:var(--notice-color,#a7f3c0);font-weight:800;line-height:1.4;text-align:left;text-shadow:0 2px 5px #000;box-shadow:0 8px 24px #0006;white-space:normal;overflow-wrap:anywhere;word-break:break-word;overflow:visible;pointer-events:none}.request-notice-widget.is-failed{border-color:rgba(255,125,125,.7);background:rgba(38,15,18,.95);color:#ffb2b2}
.output-layer{position:absolute;display:flex;align-items:flex-start;justify-content:flex-start;overflow:hidden;white-space:pre-wrap;word-break:break-word;font-weight:600;text-shadow:0 2px 6px #000b}
.emoticon-output-layer{padding:0;white-space:normal;text-shadow:none}
.floating-emoticon-layer{position:absolute;inset:0;overflow:hidden;pointer-events:none}
.floating-emoticon{position:absolute;top:100%;display:block;max-width:40%;max-height:40%;object-fit:contain;transform:translateX(-50%);opacity:0;pointer-events:none;animation:emoticon-rise var(--emoticon-duration) linear both}
@keyframes emoticon-rise{
  0%{top:100%;opacity:0;transform:translate3d(-50%,0,0)}
  10%{opacity:1}
  25%{transform:translate3d(calc(-50% + var(--emoticon-drift)),0,0)}
  50%{transform:translate3d(-50%,0,0)}
  75%{transform:translate3d(calc(-50% - var(--emoticon-drift)),0,0)}
  82%{opacity:1}
  100%{top:-25%;opacity:0;transform:translate3d(-50%,0,0)}
}
.output-layer img{width:100%;height:100%;object-fit:contain}.output-layer iframe.live2d-output-frame{display:block;width:100%;height:100%;border:0;background:transparent!important;background-color:transparent!important;pointer-events:none}
.event-stack{display:flex;flex-direction:column;gap:.35em;width:100%;height:100%;overflow:hidden}
.sc-event-stack{justify-content:flex-end}
.sc-event-stack .sc-line{flex:0 0 auto}
.event-line{display:flex;gap:.5em;align-items:baseline;padding:.2em .4em;border-radius:.3em;background:#10131ac9;font-size:1em;line-height:1.35}
.sc-line{display:block;overflow-wrap:anywhere;white-space:normal}
.sc-line strong{display:inline;margin-right:.5em}
.sc-line span{display:inline;min-width:0;overflow-wrap:anywhere}
.event-line strong{color:#ffe08a;flex:none}
.event-line span{min-width:0;overflow-wrap:anywhere}
.gift-line{background:#382448dc}.sc-line{background:#603c1edc}
.gift-event-stack{display:flex;justify-content:flex-end;width:100%;height:100%;overflow:hidden;white-space:normal}
.gift-event-track{display:flex;flex-direction:row;direction:rtl;align-items:stretch;gap:.5cqh;width:max-content;min-width:100%;height:100%}
.gift-event-track.is-scrolling{animation:gift-event-marquee var(--gift-scroll-duration,12s) linear infinite}
.gift-event-card{box-sizing:border-box;display:flex;flex:0 0 30cqh;width:30cqh;height:100%;min-width:30cqh;max-width:30cqh;flex-direction:column;align-items:center;justify-content:flex-start;gap:.18em;padding:.25em .35em;border:0;border-radius:0;background:transparent;text-align:center;direction:ltr;line-height:1.25;overflow:hidden}
.gift-event-image{display:block;width:auto!important;height:auto!important;max-width:100%;max-height:65%;min-height:0;object-fit:contain}
.gift-event-image-fallback{display:grid;place-items:center;min-height:2.2em;font-size:2em}
.gift-event-caption{display:flex;flex-direction:column;align-items:center;gap:.1em;width:100%;max-width:100%;min-width:0;font-size:.72em}
.gift-event-caption strong,.gift-event-caption span{display:block;width:100%;max-width:100%;min-width:0;overflow:hidden;text-overflow:ellipsis;white-space:nowrap}
.gift-event-caption span{font-size:.85em;color:#d8cce3}
@keyframes gift-event-marquee{from{transform:translateX(0)}to{transform:translateX(-50%)}}

.music-output-layer{padding:0;text-shadow:none;white-space:normal;overflow:visible;font-family:Arial,"Microsoft YaHei",sans-serif;color:#e7c89d}
.music-queue-output-layer,.video-queue-output-layer{padding:0;text-shadow:none;white-space:normal;overflow:hidden;font-family:Arial,"Microsoft YaHei",sans-serif}
.music-simple-widget{display:flex;flex-direction:column;gap:.45em;width:100%;height:100%;overflow:hidden;color:inherit;text-shadow:none;white-space:normal;font-family:Arial,"Microsoft YaHei",sans-serif}
.music-simple-heading{flex:none;font-size:1.08em;line-height:1.35;padding:0 0 .3em;border-bottom:1px solid currentColor}
.music-simple-list{display:flex;flex-direction:column;gap:.3em;min-height:0;overflow:hidden}
.music-simple-row{display:flex;align-items:center;gap:.55em;min-width:0;padding:.32em .45em;border-radius:.3em;background:rgba(16,19,26,.62);line-height:1.35}
.music-simple-index{flex:none;opacity:.78;font-variant-numeric:tabular-nums}
.music-simple-song{flex:1;min-width:0;overflow:hidden;text-overflow:ellipsis;white-space:nowrap}
.music-simple-requester{flex:none;max-width:30%;overflow:hidden;text-overflow:ellipsis;white-space:nowrap;font-size:.82em;opacity:.82}
.music-simple-empty,.music-prompt-note{padding:.4em .5em;border-radius:.3em;background:rgba(16,19,26,.45);font-size:.86em;opacity:.82}
.music-prompt-line{padding:.42em .5em;border-radius:.3em;background:rgba(16,19,26,.62);line-height:1.35}
.music-prompt-widget{align-items:stretch}
.music-queue-output-layer{padding:0;text-shadow:none;white-space:normal;overflow:hidden;font-family:Arial,"Microsoft YaHei",sans-serif}
.music-queue-widget{display:flex;flex-direction:column;gap:0;width:100%;height:auto;max-height:100%;overflow:hidden;color:#f4f6fb;text-shadow:0 2px 5px rgba(0,0,0,.5)}
.music-queue-section{display:flex;flex-direction:column;gap:0;min-width:0;padding:.55em .68em;border:1px solid rgba(159,192,255,.16);border-radius:.55em;background:linear-gradient(135deg,rgba(20,26,38,.88),rgba(13,17,26,.78));box-shadow:0 4px 14px rgba(0,0,0,.16)}
.music-queue-heading{display:flex;align-items:center;gap:.42em;min-width:0;padding-bottom:.32em;border-bottom:1px solid rgba(159,192,255,.22);color:#9fc0ff;font-size:.82em;font-weight:800;line-height:1.35}
.music-queue-heading-mark{flex:none;color:#ffd66b}
.music-queue-count{margin-left:auto;color:#aebbd0;font-size:.72em;font-weight:600}
.music-queue-pending{flex:none;min-height:0;max-height:100%;overflow:hidden}.music-queue-prompt{border-color:rgba(255,214,107,.2);background:linear-gradient(135deg,rgba(48,39,25,.88),rgba(22,23,29,.82))}
.music-queue-prompt .music-queue-heading{color:#ffd66b;border-bottom-color:rgba(255,214,107,.24)}
.music-queue-prompt-single{display:flex;align-items:center;flex-wrap:nowrap;gap:.8em;min-width:0;overflow:hidden;white-space:nowrap;font-size:.82em;line-height:1.35}
.music-queue-prompt-title{display:flex;align-items:center;gap:.3em;flex:none;color:#ffd66b;font-size:1em}.music-queue-prompt-title .music-queue-heading-mark{color:#ffd66b;font-size:1.2em;text-shadow:0 0 7px rgba(255,214,107,.55)}
.music-queue-prompt-single span{flex:none;color:#d7deea}
.music-queue-prompt-single b{color:#ffd66b;font-weight:800}
.music-queue-current-inline{flex-wrap:nowrap;gap:.55em;padding:.34em 0;border:0;border-radius:0;background:transparent;box-shadow:none;white-space:nowrap}
.music-queue-current-stacked{display:flex;flex-wrap:wrap;align-items:center;gap:.28em .55em;white-space:normal;min-width:0}.music-queue-current-stacked .music-queue-current-label{flex:0 0 100%;width:100%}.music-queue-current-stacked .music-queue-current-track{box-sizing:border-box;flex:1 1 0;width:auto;max-width:none;min-width:0;padding-left:0;white-space:nowrap}.music-queue-current-stacked .music-queue-empty{box-sizing:border-box;flex:1 1 0;width:auto;max-width:none;min-width:0;padding-left:0;white-space:nowrap}.music-queue-current-stacked .music-queue-requester{box-sizing:border-box;flex:0 1 auto;width:auto;max-width:30%;padding-left:0;white-space:nowrap}
.music-queue-current-label{flex:none;color:#ffd66b;font-size:.82em;white-space:nowrap}
.music-queue-current-track{display:flex;flex:1;align-items:baseline;gap:.35em;min-width:0;overflow:hidden;white-space:nowrap}
.music-queue-current-title{flex:0 1 auto;min-width:0;overflow:hidden;text-overflow:ellipsis;white-space:nowrap;color:#f7f8fc;font-size:.88em}
.music-queue-current-artist{flex:0 1 auto;min-width:0;overflow:hidden;text-overflow:ellipsis;white-space:nowrap;color:#aeb9ca;font-size:.76em}
.music-queue-current-inline .music-queue-requester{max-width:24%}.music-queue-current-inline .music-queue-empty{padding:0;white-space:nowrap}.music-queue-current-stacked.music-queue-current-inline .music-queue-requester{box-sizing:border-box;flex:0 0 100%;width:100%;max-width:100%;padding-left:calc(.5em + 1.7em + .58em);white-space:normal}
.music-queue-list{gap:0}
.music-queue-list .music-queue-item{padding-top:.08em;padding-bottom:.08em;border-radius:0;border-bottom:1px solid rgba(255,255,255,.045)}
.music-queue-list{display:flex;flex:none;flex-direction:column;gap:0;min-height:0;overflow:hidden}
.music-queue-item{display:flex;align-items:center;gap:.58em;min-width:0;padding:.38em .5em;border:1px solid rgba(255,255,255,.055);border-radius:.36em;background:rgba(255,255,255,.055);line-height:1.3}
.music-queue-current{border-color:rgba(255,214,107,.2);background:rgba(255,214,107,.075)}.music-queue-item.music-queue-current-inline{padding:.34em 0;border:0;border-radius:0;background:transparent;box-shadow:none}
.music-queue-index{flex:none;min-width:1.7em;color:#9fc0ff;font-size:.78em;font-weight:800;font-variant-numeric:tabular-nums}
.music-queue-track{display:flex;flex:1;flex-direction:row;align-items:baseline;gap:.35em;min-width:0;overflow:hidden;white-space:nowrap}
.music-queue-track strong,.music-queue-track small{flex:0 1 auto;min-width:0;overflow:hidden;text-overflow:ellipsis;white-space:nowrap}
.music-queue-track strong{color:#f7f8fc;font-size:.88em;font-weight:750}
.music-queue-track small{color:#aeb9ca;font-size:.7em}
.music-queue-requester{flex:none;max-width:30%;overflow:hidden;text-overflow:ellipsis;white-space:nowrap;color:#65b8ff;font-size:.72em;font-weight:700}
.music-queue-empty{padding:.38em .5em;color:#aeb9ca;font-size:.76em;line-height:1.4}
.music-widget{width:min(405px,100%);display:flex;flex-direction:column;gap:8px;position:relative;overflow:hidden;border-radius:9px;user-select:none;color:#e7c89d}
.music-main{height:112px;display:flex;gap:8px;min-width:0;position:relative}
.music-cover-wrap{height:112px;width:112px;flex:0 0 112px;position:relative;overflow:hidden;border-radius:5px;background:rgba(102,76,60,.35)}
.music-cover-blur{position:absolute;inset:-20px;background-position:center;background-size:cover;filter:blur(20px) brightness(.8);opacity:.55;transform:scale(1.2)}
.music-cover{position:absolute;inset:0;width:100%;height:100%;object-fit:cover;z-index:1}
.music-cover-fallback{position:absolute;inset:0;display:grid;place-items:center;font-size:38px;color:rgba(231,200,157,.65);background:linear-gradient(145deg,#765c49,#34271f)}
.music-details{min-width:0;flex:1;display:flex;flex-direction:column;justify-content:center;gap:7px;overflow:hidden;position:relative;isolation:isolate;border-radius:5px;background:rgba(30,23,20,.25)}
.music-details::before{content:"";position:absolute;inset:0;z-index:-2;background-image:var(--details-bg,none);background-position:center;background-size:cover;background-repeat:no-repeat;opacity:.6;filter:blur(3px) brightness(.65);transform:scale(1.04);pointer-events:none}
.music-details::after{content:"";position:absolute;inset:0;z-index:-1;background:rgba(0,0,0,.2);pointer-events:none}
.music-song-info{min-width:0;display:flex;flex-direction:column;gap:7px;overflow:hidden;border-bottom:1px solid var(--random-color);padding:0 0 7px 8px;position:relative;z-index:1}
.music-song-title{font-size:18px;font-weight:700;line-height:1.3;white-space:nowrap;overflow:hidden;text-overflow:ellipsis;text-shadow:0 1px 4px #24170f;color:var(--random-color)}
.music-song-author{font-size:14px;line-height:1.3;white-space:nowrap;overflow:hidden;text-overflow:ellipsis;opacity:.94;color:var(--random-color)}
.music-song-time{height:36px;width:100%;display:flex;align-items:center;justify-content:space-between;gap:5px;padding:0 10px;margin:0;position:relative;z-index:1}
.music-time{width:42px;flex:0 0 42px;font-size:12px;font-weight:700;font-variant-numeric:tabular-nums;color:var(--random-color)}
.music-time:last-child{text-align:right}
.music-wave{height:36px;flex:1;display:flex;align-items:center;justify-content:center;gap:3px;overflow:hidden}
.music-wavebar{width:3px;min-width:2px;height:var(--h);border-radius:3px;background:var(--random-color);opacity:.92;transform:scaleY(.45);transform-origin:center;animation:music-wave var(--speed) ease-in-out infinite alternate;animation-delay:var(--delay)}
@keyframes music-wave{from{transform:scaleY(.22)}to{transform:scaleY(1)}}
@media(max-width:300px){.music-widget{gap:5px}.music-main{height:84px}.music-cover-wrap{height:84px;width:84px;flex-basis:84px}.music-song-title{font-size:14px}.music-song-author{font-size:12px}.music-song-time{height:56px;padding:0 10px}.music-time{width:34px;flex-basis:34px;font-size:10px}}
.scene-error{display:grid;place-items:center;width:100vw;height:100vh;background:transparent;color:#fff;font:16px "Microsoft YaHei",sans-serif}
.output-layer iframe.live2d-output-frame{display:block;width:100%;height:100%;border:0;background:transparent!important;background-color:transparent!important;color-scheme:normal;pointer-events:none}
</style>