<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { useRoute } from 'vue-router'

type Layer = { id: string; type: string; name: string; x: number; y: number; width: number; height: number; z_index: number; visible: boolean; opacity: number; text?: string; url?: string; color?: string; font_size?: number }
type Scene = { id: string; name: string; width: number; height: number; background: string; layers: Layer[] }
type LiveEvent = { id: number; type: string; user_name: string; content: string; gift_name?: string; gift_count?: number; amount?: number; timestamp: number }
type Track = { id: string; source_id: string; title: string; artist?: string; album?: string; duration_ms: number; cover_url?: string }
type Playback = { state: string; track?: Track; position_ms: number; duration_ms: number }
type LyricLine = { time: number; text: string; translation?: string }
type MusicResponse = { snapshot: Playback; lyrics: string; lyrics_error?: string }
const route = useRoute()
const scene = ref<Scene | null>(null)
const liveEvents = ref<LiveEvent[]>([])
const now = ref(new Date())
const error = ref('')
const playback = ref<Playback | null>(null)
const lyricLines = ref<LyricLine[]>([])
const musicError = ref('')
let source: EventSource | undefined
let clockTimer: ReturnType<typeof setInterval> | undefined
let sceneRefreshTimer: ReturnType<typeof setInterval> | undefined
let musicRefreshTimer: ReturnType<typeof setInterval> | undefined
let musicTrackKey = ''
let musicRefreshing = false

const danmaku = computed(() => liveEvents.value.filter(item => item.type === 'danmaku').slice(-8).reverse())
const gifts = computed(() => liveEvents.value.filter(item => item.type === 'gift').slice(-3).reverse())
const superChats = computed(() => liveEvents.value.filter(item => item.type === 'super_chat').slice(-3).reverse())
const activeLyric = computed(() => {
  if (!playback.value?.track) return null
  let active: LyricLine | null = null
  for (const line of lyricLines.value) {
    if (line.time > playback.value.position_ms) break
    active = line
  }
  return active
})
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
async function loadMusic() {
  if (musicRefreshing) return
  musicRefreshing = true
  try {
    const response = await fetch('/api/music', { cache: 'no-store' })
    if (!response.ok) throw new Error('音乐播放状态暂不可用')
    const data = await response.json() as MusicResponse
    playback.value = data.snapshot
    musicError.value = data.lyrics_error || ''
    const track = data.snapshot.track
    const key = track ? track.source_id + ':' + track.id : ''
    if (key !== musicTrackKey) {
      musicTrackKey = key
      lyricLines.value = track ? parseLyrics(data.lyrics || '') : []
    } else if (track && lyricLines.value.length === 0 && data.lyrics) {
      lyricLines.value = parseLyrics(data.lyrics)
    }
  } catch (cause) {
    musicError.value = cause instanceof Error ? cause.message : String(cause)
  } finally { musicRefreshing = false }
}
onMounted(() => {
  void loadScene()
  void loadMusic()
  sceneRefreshTimer = setInterval(() => { void loadScene() }, 1000)
  musicRefreshTimer = setInterval(() => { void loadMusic() }, 500)
  source = new EventSource('/api/events')
  source.onmessage = handleEvent
  source.onerror = () => { error.value = '直播事件连接中断，正在自动重连…' }
  source.onopen = () => { error.value = ''; void loadRecentEvents() }
  clockTimer = setInterval(() => { now.value = new Date() }, 500)
})
onUnmounted(() => {
  source?.close()
  if (clockTimer) clearInterval(clockTimer)
  if (sceneRefreshTimer) clearInterval(sceneRefreshTimer)
  if (musicRefreshTimer) clearInterval(musicRefreshTimer)
})
</script>

<template>
  <main v-if="scene" class="scene-output" :style="{ aspectRatio: scene.width + ' / ' + scene.height, background: scene.background === 'transparent' ? 'transparent' : scene.background }">
    <div v-for="layer in scene.layers.filter(item => item.visible)" :key="layer.id" class="output-layer" :class="{ 'music-output-layer': layer.type === 'music' }" :style="layerStyle(layer)">
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
      <section v-else-if="layer.type === 'music'" class="music-card">
        <div class="music-artwork">
          <img v-if="playback?.track?.cover_url" :src="playback.track.cover_url" alt="歌曲封面" />
          <span v-else class="music-artwork-placeholder">♫</span>
        </div>
        <div class="music-details">
          <div class="music-heading">
            <strong>{{ playback?.track?.title || '等待播放歌曲' }}</strong>
            <span>{{ playback?.track?.artist || (playback?.track ? '未知歌手' : 'My-LiveHub 音乐') }}</span>
          </div>
          <div class="music-progress">
            <div class="music-progress-track"><i :style="{ width: progress + '%' }"></i></div>
            <div class="music-time"><span>{{ formatTime(playback?.position_ms || 0) }}</span><span>{{ formatTime(playback?.duration_ms || playback?.track?.duration_ms || 0) }}</span></div>
          </div>
          <div class="music-lyrics">
            <strong>{{ activeLyric?.text || (playback?.track ? (musicError ? '歌词暂不可用' : '暂无同步歌词') : '等待歌曲播放') }}</strong>
            <span v-if="activeLyric?.translation">{{ activeLyric.translation }}</span>
          </div>
        </div>
        <span class="music-state" :class="{ playing: playback?.state === 'playing' }">{{ playback?.state === 'playing' ? '正在播放' : playback?.state === 'paused' ? '已暂停' : playback?.state === 'error' ? '播放错误' : '待机' }}</span>
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
.music-output-layer{padding:0;text-shadow:none;white-space:normal}
.music-card{position:relative;display:flex;align-items:center;gap:4%;width:100%;height:100%;box-sizing:border-box;padding:3.5%;overflow:hidden;border:1px solid #ffffff24;border-radius:14px;background:linear-gradient(135deg,#171b27f2,#292e3bf0);color:#fff;box-shadow:0 8px 28px #0005}
.music-artwork{flex:none;width:29%;height:100%;overflow:hidden;border-radius:9px;background:#343b4c}
.music-artwork img{display:block;width:100%;height:100%;object-fit:cover}
.music-artwork-placeholder{display:grid;place-items:center;width:100%;height:100%;font-size:clamp(28px,5cqh,64px);color:#cbd2e5}
.music-details{display:flex;flex:1;flex-direction:column;justify-content:space-between;gap:8%;min-width:0;height:100%}
.music-heading{display:flex;flex-direction:column;gap:5px;min-width:0}
.music-heading strong{overflow:hidden;text-overflow:ellipsis;white-space:nowrap;font-size:clamp(14px,3.1cqh,34px);font-weight:750}
.music-heading span{overflow:hidden;text-overflow:ellipsis;white-space:nowrap;color:#c5cada;font-size:clamp(10px,1.8cqh,20px)}
.music-progress{display:flex;flex-direction:column;gap:5px}
.music-progress-track{height:5px;overflow:hidden;border-radius:10px;background:#ffffff30}
.music-progress-track i{display:block;height:100%;border-radius:10px;background:#a9b8ff;transition:width .25s linear}
.music-time{display:flex;justify-content:space-between;color:#c5cada;font-size:clamp(9px,1.45cqh,16px);font-variant-numeric:tabular-nums}
.music-lyrics{display:flex;flex-direction:column;gap:4px;min-width:0}
.music-lyrics strong,.music-lyrics span{overflow:hidden;text-overflow:ellipsis;white-space:nowrap}
.music-lyrics strong{font-size:clamp(11px,2cqh,23px);font-weight:650}
.music-lyrics span{color:#c5cada;font-size:clamp(9px,1.55cqh,18px)}
.music-state{position:absolute;right:3.5%;top:3.5%;padding:3px 7px;border-radius:20px;background:#ffffff18;color:#c5cada;font-size:clamp(8px,1.1cqh,13px)}
.music-state.playing{background:#5369a455;color:#dce3ff}
.scene-error{display:grid;place-items:center;width:100vw;height:100vh;background:transparent;color:#fff;font:16px "Microsoft YaHei",sans-serif}
</style>
