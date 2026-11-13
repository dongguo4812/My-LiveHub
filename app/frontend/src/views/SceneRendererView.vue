<script setup lang="ts">
import { computed, onMounted, onUnmounted, onUpdated, ref } from 'vue'
import { useRoute } from 'vue-router'

type Layer = { id: string; type: string; name: string; x: number; y: number; width: number; height: number; z_index: number; visible: boolean; opacity: number; text?: string; url?: string; color?: string; font_size?: number }
type Scene = { id: string; name: string; width: number; height: number; background: string; layers: Layer[] }
type LiveEvent = { id: number; type: string; user_name: string; content: string; gift_name?: string; gift_count?: number; amount?: number; timestamp: number }
type Track = { id: string; source_id: string; title: string; artist?: string; album?: string; duration_ms: number; cover_url?: string }
type Playback = { state: string; track?: Track; position_ms: number; duration_ms: number }
type MusicQueueItem = { id: string; track: Track; requested_by?: string }
type MusicQueue = { current?: MusicQueueItem; items: MusicQueueItem[]; capacity: number }
type MusicDisplayConfig = { enabled: boolean; command_enabled: boolean; command_prefix: string; skip_command_enabled: boolean; skip_command: string; require_fan_medal: boolean; fan_medal_level: number }
type LyricLine = { time: number; text: string; translation?: string }
type MusicLyricsResponse = { lyrics: string }
const route = useRoute()
const scene = ref<Scene | null>(null)
const liveEvents = ref<LiveEvent[]>([])
const now = ref(new Date())
const error = ref('')
const playback = ref<Playback | null>(null)
const musicQueue = ref<MusicQueue>({ items: [], capacity: 0 })
const musicDisplayConfig = ref<MusicDisplayConfig | null>(null)
const lyricLines = ref<LyricLine[]>([])
const lyricsLoading = ref(false)
const lyricsFailed = ref(false)
const musicError = ref('')
const musicAccentColor = ref('#A83232')
const waveBars = Array.from({ length: 31 }, (_, index) => ({ height: 7 + Math.abs(Math.sin(index * 1.71) * 24), speed: 0.45 + (index % 7) * 0.13, delay: -(Math.random() * 1.8) }))
const musicColors = ['#FF3B30', '#FF7A00', '#FFD000', '#39D353', '#00D6C9', '#00A6FF', '#4169FF', '#9B5CFF', '#FF4FA3', '#FF6B6B']
let source: EventSource | undefined
let clockTimer: ReturnType<typeof setInterval> | undefined
let sceneRefreshTimer: ReturnType<typeof setInterval> | undefined
let musicRefreshTimer: ReturnType<typeof setInterval> | undefined
let musicQueueRefreshTimer: ReturnType<typeof setInterval> | undefined
let musicConfigRefreshTimer: ReturnType<typeof setInterval> | undefined
let musicTrackKey = ''
let musicRefreshing = false
let lyricsRequestID = 0

const danmaku = computed(() => liveEvents.value.filter(item => item.type === 'danmaku').slice(-8).reverse())
const gifts = computed(() => liveEvents.value.filter(item => item.type === 'gift').slice(-3).reverse())
const superChats = computed(() => liveEvents.value.filter(item => item.type === 'super_chat').slice(-3).reverse())
const activeLyricIndex = computed(() => {
  if (!playback.value?.track) return -1
  let activeIndex = -1
  for (let index = 0; index < lyricLines.value.length; index++) {
    if (lyricLines.value[index].time > playback.value.position_ms) break
    activeIndex = index
  }
  return activeIndex
})
const activeLyric = computed(() => activeLyricIndex.value >= 0 ? lyricLines.value[activeLyricIndex.value] : null)
const lyricScrollProgress = computed(() => {
  const index = activeLyricIndex.value
  const current = playback.value
  if (index < 0 || !current) return 0
  const start = lyricLines.value[index].time
  const end = lyricLines.value[index + 1]?.time ?? (current.duration_ms || current.track?.duration_ms || start)
  if (end <= start) return 0
  return Math.max(0, Math.min(1, (current.position_ms - start) / (end - start)))
})
function updateLyricScroll() {
  const progress = lyricScrollProgress.value
  document.querySelectorAll<HTMLElement>('.music-lyrics').forEach(container => {
    const lyricIndex = container.dataset.lyricIndex ?? ''
    const lineChanged = container.dataset.lastLyricIndex !== lyricIndex
    container.dataset.lastLyricIndex = lyricIndex
    container.querySelectorAll<HTMLElement>('.music-lyric-text').forEach(text => {
      const viewport = text.parentElement
      if (!(viewport instanceof HTMLElement)) return
      const overflow = Math.max(0, text.scrollWidth - viewport.clientWidth)
      viewport.dataset.scrolling = overflow > 1 ? 'true' : 'false'
      viewport.style.justifyContent = overflow > 1 ? 'flex-start' : 'center'
      text.style.transition = lineChanged ? 'none' : ''
      text.style.transform = overflow > 1 ? `translate3d(-${overflow * progress}px, 0, 0)` : 'translate3d(0, 0, 0)'
    })
  })
}
const progress = computed(() => {
  const duration = playback.value?.duration_ms || playback.value?.track?.duration_ms || 0
  if (!duration) return 0
  return Math.max(0, Math.min(100, (playback.value?.position_ms || 0) / duration * 100))
})
function parseLyrics(raw: string): LyricLine[] {
  const parsed: Array<{ time: number; text: string }> = []
  for (const line of raw.split(/\r?\n/)) {
    const match = line.match(/^\[(\d{1,2}):(\d{2})(?:[.:](\d{1,3}))?\](.*)$/)
    if (!match) continue
    const fraction = match[3] ?? '0'
    const time = Number(match[1]) * 60000 + Number(match[2]) * 1000 + Number(fraction.padEnd(3, '0').slice(0, 3))
    const text = match[4].trim()
    if (text) parsed.push({ time, text })
  }
  parsed.sort((a, b) => a.time - b.time)
  const result: LyricLine[] = []
  for (const line of parsed) {
    const previous = result[result.length - 1]
    if (previous?.time === line.time) {
      if (line.text !== previous.text && !previous.translation) previous.translation = line.text
    } else result.push({ ...line })
  }
  return result
}
function formatTime(value: number) {
  const seconds = Math.max(0, Math.floor(value / 1000))
  return Math.floor(seconds / 60) + ':' + String(seconds % 60).padStart(2, '0')
}
function layerStyle(layer: Layer) {
  const current = scene.value
  if (!current) return {}
  return {
    left: (layer.x / current.width * 100) + '%',
    top: (layer.y / current.height * 100) + '%',
    width: (layer.width / current.width * 100) + '%',
    height: (layer.height / current.height * 100) + '%',
    zIndex: layer.z_index,
    opacity: layer.opacity,
    color: layer.color || '#ffffff',
    fontSize: ((layer.font_size || 24) / current.height * 100) + 'cqh',
  }
}
function formatClock() { return now.value.toLocaleTimeString('zh-CN', { hour12: false }) }
function handleEvent(event: MessageEvent<string>) {
  try {
    const value = JSON.parse(event.data) as LiveEvent
    if (liveEvents.value.some(item => item.id === value.id)) return
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
    error.value = ''
    document.title = scene.value.name + ' - My-LiveHub'
    document.documentElement.style.background = 'transparent'
    document.body.style.background = 'transparent'
    const root = document.getElementById('app')
    if (root) root.style.background = 'transparent'
  } catch (cause) { error.value = cause instanceof Error ? cause.message : String(cause) }
}
async function loadTrackLyrics(track: Track, key: string) {
  const requestID = ++lyricsRequestID
  lyricsLoading.value = true
  lyricsFailed.value = false
  try {
    const response = await fetch('/api/music/lyrics', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(track),
      cache: 'no-store',
    })
    if (!response.ok) throw new Error('歌词获取失败')
    const data = await response.json() as MusicLyricsResponse
    if (requestID !== lyricsRequestID || musicTrackKey !== key) return
    lyricLines.value = parseLyrics(data.lyrics || '')
    musicError.value = ''
  } catch (cause) {
    if (requestID === lyricsRequestID && musicTrackKey === key) {
      lyricLines.value = []
      lyricsFailed.value = true
      musicError.value = cause instanceof Error ? cause.message : String(cause)
    }
  } finally {
    if (requestID === lyricsRequestID && musicTrackKey === key) lyricsLoading.value = false
  }
}
function displayRequester(value?: string, fallback = '观众') {
  return !value || value === '__my_livehub_search_play__' ? fallback : value
}
async function loadMusicDisplayConfig() {
  try {
    const response = await fetch('/api/music/config', { cache: 'no-store' })
    if (!response.ok) return
    musicDisplayConfig.value = await response.json() as MusicDisplayConfig
  } catch { /* Keep the last known command settings. */ }
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
      lyricsRequestID++
      lyricLines.value = []
      lyricsLoading.value = Boolean(track)
      lyricsFailed.value = false
      musicError.value = ''
      if (track) void loadTrackLyrics(track, key)
    }
  } catch (cause) {
    musicError.value = cause instanceof Error ? cause.message : String(cause)
  } finally { musicRefreshing = false }
}
onMounted(() => {
  void loadScene()
  void loadMusic()
  void loadMusicQueue()
  void loadMusicDisplayConfig()
  sceneRefreshTimer = setInterval(() => { void loadScene() }, 1000)
  musicRefreshTimer = setInterval(() => { void loadMusic() }, 500)
  musicQueueRefreshTimer = setInterval(() => { void loadMusicQueue() }, 1000)
  musicConfigRefreshTimer = setInterval(() => { void loadMusicDisplayConfig() }, 3000)
  source = new EventSource('/api/events')
  source.onmessage = handleEvent
  source.onerror = () => { error.value = '直播事件连接中断，正在自动重连…' }
  source.onopen = () => { error.value = ''; void loadRecentEvents() }
  clockTimer = setInterval(() => { now.value = new Date() }, 500)
})
onUpdated(updateLyricScroll)
onUnmounted(() => {
  source?.close()
  lyricsRequestID++
  if (clockTimer) clearInterval(clockTimer)
  if (sceneRefreshTimer) clearInterval(sceneRefreshTimer)
  if (musicRefreshTimer) clearInterval(musicRefreshTimer)
  if (musicQueueRefreshTimer) clearInterval(musicQueueRefreshTimer)
  if (musicConfigRefreshTimer) clearInterval(musicConfigRefreshTimer)
})
</script>

<template>
  <main v-if="scene" class="scene-output" :style="{ aspectRatio: scene.width + ' / ' + scene.height, background: scene.background === 'transparent' ? 'transparent' : scene.background }">
    <div v-for="layer in scene.layers.filter(item => item.visible)" :key="layer.id" class="output-layer" :class="{ 'music-output-layer': layer.type === 'music', 'music-queue-output-layer': layer.type === 'music_queue', 'music-prompt-output-layer': layer.type === 'music_prompt' }" :style="layerStyle(layer)">
      <span v-if="layer.type === 'text'">{{ layer.text }}</span>
      <img v-else-if="layer.type === 'image' && layer.url" :src="layer.url" alt="" />
      <time v-else-if="layer.type === 'clock'">{{ formatClock() }}</time>
      <div v-else-if="layer.type === 'danmaku'" class="event-stack">
        <div v-for="item in danmaku" :key="item.id" class="event-line"><strong>{{ item.user_name }}</strong><span>{{ item.content }}</span></div>
      </div>
      <div v-else-if="layer.type === 'gift'" class="event-stack">
        <div v-for="item in gifts" :key="item.id" class="event-line gift-line"><strong>{{ item.user_name }}</strong><span>赠送 {{ item.gift_name }} × {{ item.gift_count || 1 }}</span></div>
      </div>
      <div v-else-if="layer.type === 'super_chat'" class="event-stack">
        <div v-for="item in superChats" :key="item.id" class="event-line sc-line"><strong>{{ item.user_name }} · ¥{{ Number(item.amount || 0).toFixed(2) }}</strong><span>{{ item.content }}</span></div>
      </div>
      <section v-else-if="layer.type === 'music_queue'" class="music-simple-widget" :style="{ fontSize: ((layer.font_size || 22) / (scene?.height || 1080) * 100) + 'cqh' }">
        <strong class="music-simple-heading">待播队列</strong>
        <div v-if="musicQueue.items.length" class="music-simple-list">
          <div v-for="(item, index) in musicQueue.items" :key="item.id" class="music-simple-row">
            <span class="music-simple-index">{{ index + 1 }}.</span>
            <span class="music-simple-song">{{ item.track.title }} - {{ item.track.artist || '未知歌手' }}</span>
            <span class="music-simple-requester">{{ displayRequester(item.requested_by) }}</span>
          </div>
        </div>
        <div v-else class="music-simple-empty">暂无待播歌曲</div>
      </section>
      <section v-else-if="layer.type === 'music_prompt'" class="music-simple-widget music-prompt-widget" :style="{ fontSize: ((layer.font_size || 22) / (scene?.height || 1080) * 100) + 'cqh' }">
        <strong class="music-simple-heading">点歌提示</strong>
        <template v-if="musicDisplayConfig?.enabled">
          <div v-if="musicDisplayConfig.command_enabled" class="music-prompt-line">点歌：{{ musicDisplayConfig.command_prefix }} 歌曲名称</div>
          <div v-if="musicDisplayConfig.skip_command_enabled" class="music-prompt-line">切歌：{{ musicDisplayConfig.skip_command }}（主播 / 房管）</div>
          <div v-if="musicDisplayConfig.require_fan_medal" class="music-prompt-note">需拥有主播粉丝勋章，等级达到 {{ musicDisplayConfig.fan_medal_level }} 级</div>
          <div v-if="!musicDisplayConfig.command_enabled && !musicDisplayConfig.skip_command_enabled" class="music-simple-empty">当前未启用点歌指令</div>
        </template>
        <div v-else class="music-simple-empty">音乐点歌功能未启用</div>
      </section>
      <section v-else-if="layer.type === 'music'" class="music-simple-widget music-current-widget" :style="{ fontSize: ((layer.font_size || 22) / (scene?.height || 1080) * 100) + 'cqh' }">
        <strong class="music-simple-heading">当前播放</strong>
        <div v-if="playback?.state === 'playing' && playback?.track" class="music-simple-row">
          <span class="music-simple-song">{{ playback.track.title }} - {{ playback.track.artist || '未知歌手' }}</span>
          <span class="music-simple-requester">{{ displayRequester(musicQueue.current?.requested_by, '主播') }}</span>
        </div>
        <div v-else class="music-simple-empty">当前没有正在播放的歌曲</div>
      </section>
    </div>
  </main>
  <main v-else class="scene-error">{{ error || '正在加载场景…' }}</main>
</template>

<style>
html,body,#app{width:100%;height:100%;margin:0;overflow:hidden;background:transparent!important}
body{color:#fff;font-family:Inter,"Segoe UI","Microsoft YaHei",sans-serif}
.scene-output{position:relative;width:100vw;height:100vh;overflow:hidden;container-type:size;background:transparent}
.output-layer{position:absolute;display:flex;align-items:flex-start;justify-content:flex-start;overflow:hidden;white-space:pre-wrap;word-break:break-word;font-weight:600;text-shadow:0 2px 6px #000b}
.output-layer img{width:100%;height:100%;object-fit:contain}
.event-stack{display:flex;flex-direction:column;gap:.35em;width:100%;height:100%;overflow:hidden}
.event-line{display:flex;gap:.5em;align-items:baseline;padding:.2em .4em;border-radius:.3em;background:#10131ac9;font-size:1em;line-height:1.35}
.event-line strong{color:#ffe08a;flex:none}
.event-line span{min-width:0;overflow-wrap:anywhere}
.gift-line{background:#382448dc}.sc-line{background:#603c1edc}
.music-output-layer{padding:0;text-shadow:none;white-space:normal;overflow:visible;font-family:Arial,"Microsoft YaHei",sans-serif;color:#e7c89d}
.music-queue-output-layer{padding:0;text-shadow:none;white-space:normal;overflow:hidden;font-family:Arial,"Microsoft YaHei",sans-serif}
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
.music-queue-widget{display:flex;flex-direction:column;gap:.45em;width:100%;height:100%;overflow:hidden}
.music-queue-heading{font-size:1.08em;line-height:1.35;padding:0 0 .3em;border-bottom:1px solid currentColor}
.music-queue-list{display:flex;flex-direction:column;gap:.35em;min-height:0;overflow:hidden}
.music-queue-item{display:flex;align-items:center;gap:.55em;min-width:0;padding:.35em .5em;border-radius:.35em;background:rgba(16,19,26,.72);line-height:1.3}
.music-queue-index{flex:none;min-width:1.4em;text-align:center;opacity:.78;font-variant-numeric:tabular-nums}
.music-queue-track{display:flex;flex:1;flex-direction:column;gap:.12em;min-width:0;overflow:hidden}
.music-queue-track strong,.music-queue-track small{overflow:hidden;text-overflow:ellipsis;white-space:nowrap}
.music-queue-track strong{font-size:1em}
.music-queue-track small{font-size:.78em;opacity:.78}
.music-queue-requester{flex:none;max-width:28%;overflow:hidden;text-overflow:ellipsis;white-space:nowrap;font-size:.78em;opacity:.82}
.music-queue-empty{padding:.55em .6em;border-radius:.35em;background:rgba(16,19,26,.6);font-size:.85em;opacity:.8}
.music-widget{width:min(405px,100%);display:flex;flex-direction:column;gap:8px;position:relative;overflow:hidden;border-radius:9px;user-select:none;color:#e7c89d}
.music-main{height:112px;display:flex;gap:8px;min-width:0;position:relative}
.music-cover-wrap{height:112px;width:112px;flex:0 0 112px;position:relative;overflow:hidden;border-radius:5px;background:rgba(102,76,60,.35)}
.music-cover-blur{position:absolute;inset:-20px;background-position:center;background-size:cover;filter:blur(20px) brightness(.8);opacity:.55;transform:scale(1.2)}
.music-cover{position:absolute;inset:0;width:100%;height:100%;object-fit:cover;z-index:1}
.music-cover-fallback{position:absolute;inset:0;display:grid;place-items:center;font-size:38px;color:rgba(231,200,157,.65);background:linear-gradient(145deg,#765c49,#34271f)}
.music-details{min-width:0;flex:1;display:flex;flex-direction:column;justify-content:center;gap:7px;overflow:hidden;position:relative;isolation:isolate;border-radius:5px;background:rgba(30,23,20,.25)}
.music-details::before{content:"";position:absolute;inset:0;z-index:-2;background-image:var(--details-bg,none);background-position:center;background-size:cover;background-repeat:no-repeat;opacity:.6;filter:blur(3px) brightness(.65);transform:scale(1.04);pointer-events:none}
.music-details::after{content:"";position:absolute;inset:0;z-index:-1;background:rgba(0,0,0,.2);pointer-events:none}
.music-song-info{min-width:0;display:flex;flex-direction:column;gap:7px;overflow:hidden;border-bottom:1px solid var(--random-color);padding-bottom:7px;position:relative;z-index:1}
.music-song-title{font-size:18px;font-weight:700;line-height:1.3;white-space:nowrap;overflow:hidden;text-overflow:ellipsis;text-shadow:0 1px 4px #24170f;color:var(--random-color)}
.music-song-author{font-size:14px;line-height:1.3;white-space:nowrap;overflow:hidden;text-overflow:ellipsis;opacity:.94;color:var(--random-color)}
.music-song-time{height:36px;width:100%;display:flex;align-items:center;justify-content:space-between;gap:5px;padding:0 10px;margin:0;position:relative;z-index:1}
.music-time{width:42px;flex:0 0 42px;font-size:12px;font-weight:700;font-variant-numeric:tabular-nums;color:var(--random-color)}
.music-time:last-child{text-align:right}
.music-wave{height:36px;flex:1;display:flex;align-items:center;justify-content:center;gap:3px;overflow:hidden}
.music-wavebar{width:3px;min-width:2px;height:var(--h);border-radius:3px;background:var(--random-color);opacity:.92;transform:scaleY(.45);transform-origin:center;animation:music-wave var(--speed) ease-in-out infinite alternate;animation-delay:var(--delay)}
@keyframes music-wave{from{transform:scaleY(.22)}to{transform:scaleY(1)}}
.music-lyrics{position:relative;min-height:42px;display:flex;flex-direction:column;justify-content:center;gap:3px;overflow:hidden;padding:5px 9px;border-radius:5px;background:transparent;text-align:center}
.music-lyrics::before,.music-lyrics::after{content:none}
.music-lyric-viewport{width:100%;min-width:0;overflow:hidden;display:flex;justify-content:center}
.music-lyric-text{position:relative;display:block;flex:none;width:max-content;max-width:none;overflow:visible;white-space:nowrap;transform:translate3d(0,0,0);transition:transform .48s linear;will-change:transform}
.music-lyrics strong.music-lyric-text{font-size:var(--music-lyric-size);font-weight:700;line-height:1.35;color:var(--random-color)}
.music-lyrics span.music-lyric-text{font-size:var(--music-translation-size);line-height:1.3;color:#e7c89d;opacity:.9}
.music-lyric-status{width:100%;text-align:center;font-size:var(--music-status-size);line-height:1.35;color:var(--random-color)}
@media(max-width:300px){.music-widget{gap:5px}.music-main{height:84px}.music-cover-wrap{height:84px;width:84px;flex-basis:84px}.music-song-title{font-size:14px}.music-song-author{font-size:12px}.music-song-time{height:56px;padding:0 10px}.music-time{width:34px;flex-basis:34px;font-size:10px}.music-lyrics{min-height:38px}.music-lyrics strong{font-size:12px}.music-lyrics span{font-size:10px}}
.scene-error{display:grid;place-items:center;width:100vw;height:100vh;background:transparent;color:#fff;font:16px "Microsoft YaHei",sans-serif}
</style>
