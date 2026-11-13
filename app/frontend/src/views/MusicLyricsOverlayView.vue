<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import * as MusicService from '../../bindings/github.com/dongguo4812/My-LiveHub/app/internal/desktop/musicservice.js'
import * as MusicLyricsWindowService from '../../bindings/github.com/dongguo4812/My-LiveHub/app/internal/desktop/musiclyricswindowservice.js'
import { getActiveSyncedLyric, parseSyncedLyrics, type SyncedLyricLine } from '../utils/syncedLyrics'

type PlaybackSnapshot = Awaited<ReturnType<typeof MusicService.GetPlaybackStatus>>
type LyricsStyle = {
  originalColor: string
  translationColor: string
  originalSize: number
  translationSize: number
  textShadow: boolean
}
const defaultLyricsStyle: LyricsStyle = {
  originalColor: '#ffffff',
  translationColor: '#c6d0e6',
  originalSize: 27,
  translationSize: 18,
  textShadow: true,
}
const lyricsStyle = ref<LyricsStyle>({ ...defaultLyricsStyle })
const settingsVisible = ref(false)
const styleStorageKey = 'my-livehub.music-lyrics-style'

function loadLyricsStyle() {
  try {
    const saved = localStorage.getItem(styleStorageKey)
    if (!saved) return
    const value = JSON.parse(saved) as Partial<LyricsStyle>
    if (typeof value.originalColor === 'string' && /^#[0-9a-fA-F]{6}$/.test(value.originalColor)) lyricsStyle.value.originalColor = value.originalColor
    if (typeof value.translationColor === 'string' && /^#[0-9a-fA-F]{6}$/.test(value.translationColor)) lyricsStyle.value.translationColor = value.translationColor
    if (typeof value.originalSize === 'number' && Number.isFinite(value.originalSize)) lyricsStyle.value.originalSize = Math.min(48, Math.max(16, value.originalSize))
    if (typeof value.translationSize === 'number' && Number.isFinite(value.translationSize)) lyricsStyle.value.translationSize = Math.min(36, Math.max(12, value.translationSize))
    if (typeof value.textShadow === 'boolean') lyricsStyle.value.textShadow = value.textShadow
  } catch { /* Ignore unavailable or invalid local settings. */ }
}
watch(lyricsStyle, value => {
  try { localStorage.setItem(styleStorageKey, JSON.stringify(value)) } catch { /* The overlay remains usable when storage is unavailable. */ }
}, { deep: true })
function resetLyricsStyle() {
  lyricsStyle.value = { ...defaultLyricsStyle }
}

const playback = ref<PlaybackSnapshot | null>(null)
const lyricPosition = ref(0)
let lyricPositionAnchor = 0
let lyricPositionAnchorAt = 0
let lyricClockTimer: ReturnType<typeof setInterval> | undefined
const syncedLyrics = ref<SyncedLyricLine[]>([])
const lyricsStatus = ref<'idle' | 'loading' | 'ready' | 'empty' | 'error'>('idle')
let lyricsTrackKey = ''
let lyricsRequestID = 0
let refreshTimer: ReturnType<typeof setInterval> | undefined
let refreshing = false

const activeLyric = computed(() => {
  if (!playback.value?.track) return { text: '等待播放歌曲', translation: '' }
  if (lyricsStatus.value === 'loading' || lyricsStatus.value === 'idle') return { text: '歌词加载中…', translation: '' }
  if (lyricsStatus.value === 'error') return { text: '歌词获取失败，稍后重试', translation: '' }
  if (lyricsStatus.value === 'empty' || syncedLyrics.value.length === 0) return { text: '暂无歌词', translation: '' }
  return getActiveSyncedLyric(syncedLyrics.value, lyricPosition.value) ?? { text: '歌词加载中…', translation: '' }
})
const trackTitle = computed(() => playback.value?.track
  ? `${playback.value.track.title} - ${playback.value.track.artist || '未知歌手'}`
  : 'My-LiveHub 桌面歌词')
const playbackLabel = computed(() => playback.value?.state === 'playing'
  ? '正在播放'
  : playback.value?.state === 'paused'
    ? '已暂停'
    : playback.value?.state === 'error'
      ? '播放失败'
      : '等待播放')

async function loadTrackLyrics(track: NonNullable<PlaybackSnapshot['track']>, key: string) {
  const requestID = ++lyricsRequestID
  lyricsStatus.value = 'loading'
  try {
    const lrc = await MusicService.GetLyrics(track)
    if (requestID !== lyricsRequestID || lyricsTrackKey !== key) return
    syncedLyrics.value = parseSyncedLyrics(lrc ?? '')
    lyricsStatus.value = syncedLyrics.value.length > 0 ? 'ready' : 'empty'
  } catch {
    if (requestID === lyricsRequestID && lyricsTrackKey === key) lyricsStatus.value = 'error'
  }
}

async function refreshPlayback() {
  if (refreshing) return
  refreshing = true
  try {
    const next = await MusicService.GetPlaybackStatus()
    playback.value = next
    lyricPositionAnchor = next.position_ms ?? 0
    lyricPositionAnchorAt = Date.now()
    lyricPosition.value = lyricPositionAnchor
    const track = next.track
    const key = track ? `${track.source_id}:${track.id}` : ''
    if (key !== lyricsTrackKey) {
      lyricsTrackKey = key
      syncedLyrics.value = []
      if (track) void loadTrackLyrics(track, key)
      else {
        lyricsRequestID++
        lyricsStatus.value = 'idle'
      }
    }
  } catch {
    playback.value = null
    if (lyricsTrackKey) {
      lyricsTrackKey = ''
      lyricsRequestID++
      syncedLyrics.value = []
      lyricsStatus.value = 'idle'
    }
  } finally {
    refreshing = false
  }
}

async function closeWindow() {
  try { await MusicLyricsWindowService.CloseWindow() } catch { /* The native window may already be closing. */ }
}

onMounted(() => {
  loadLyricsStyle()
  void refreshPlayback()
  refreshTimer = setInterval(() => void refreshPlayback(), 500)
  lyricClockTimer = setInterval(() => {
    if (!playback.value) return
    lyricPosition.value = lyricPositionAnchor + (playback.value.state === 'playing' ? Date.now() - lyricPositionAnchorAt : 0)
  }, 30)
})
onUnmounted(() => {
  if (refreshTimer) clearInterval(refreshTimer)
  if (lyricClockTimer) clearInterval(lyricClockTimer)
  lyricsRequestID++
})
</script>

<template>
  <main class="lyrics-overlay">
    <header class="lyrics-header">
      <div class="lyrics-track">
        <span class="eyebrow">MY-LIVEHUB · DESKTOP LYRICS</span>
        <strong>{{ trackTitle }}</strong>
      </div>
      <div class="header-actions">
        <button class="settings-button" title="歌词样式设置" aria-label="歌词样式设置" @click="settingsVisible = !settingsVisible">⚙</button>
        <button class="close-button" title="关闭桌面歌词" aria-label="关闭桌面歌词" @click="closeWindow">×</button>
      </div>
    </header>
    <section v-if="settingsVisible" class="settings-panel">
      <div class="settings-heading">
        <strong>歌词样式</strong>
        <button class="reset-button" @click="resetLyricsStyle">恢复默认</button>
      </div>
      <label class="style-control">
        <span>原文字体颜色</span>
        <input v-model="lyricsStyle.originalColor" type="color">
      </label>
      <label class="style-control">
        <span>翻译字体颜色</span>
        <input v-model="lyricsStyle.translationColor" type="color">
      </label>
      <label class="style-control">
        <span>原文字号 <b>{{ lyricsStyle.originalSize }}px</b></span>
        <input v-model.number="lyricsStyle.originalSize" type="range" min="16" max="48" step="1">
      </label>
      <label class="style-control">
        <span>翻译字号 <b>{{ lyricsStyle.translationSize }}px</b></span>
        <input v-model.number="lyricsStyle.translationSize" type="range" min="12" max="36" step="1">
      </label>
      <label class="shadow-control">
        <input v-model="lyricsStyle.textShadow" type="checkbox">
        <span>显示文字阴影</span>
      </label>
    </section>
    <section class="lyrics-content">
      <p class="original-lyric" :style="{ color: lyricsStyle.originalColor, fontSize: lyricsStyle.originalSize + 'px', textShadow: lyricsStyle.textShadow ? '0 2px 12px #0008' : 'none' }">{{ activeLyric.text }}</p>
      <p v-if="activeLyric.translation" class="translated-lyric" :style="{ color: lyricsStyle.translationColor, fontSize: lyricsStyle.translationSize + 'px', textShadow: lyricsStyle.textShadow ? '0 1px 8px #0007' : 'none' }">{{ activeLyric.translation }}</p>
    </section>
    <footer class="lyrics-footer">
      <span>{{ playbackLabel }}</span>
      <span v-if="playback?.duration_ms">{{ Math.floor((playback.position_ms ?? 0) / 1000 / 60) }}:{{ String(Math.floor((playback.position_ms ?? 0) / 1000) % 60).padStart(2, '0') }}</span>
    </footer>
  </main>
</template>

<style scoped>
:global(html), :global(body), :global(#app) { width:100%; height:100%; margin:0; overflow:hidden; background:transparent !important; }
.lyrics-overlay { --wails-draggable:drag; position:relative; box-sizing:border-box; display:flex; flex-direction:column; justify-content:space-between; width:100%; height:100%; padding:15px 22px 12px; border:1px solid #ffffff35; border-radius:14px; background:rgba(12,16,24,.86); color:#f6f7fb; font-family:Inter,"Segoe UI","Microsoft YaHei",sans-serif; text-align:center; box-shadow:0 8px 28px #0005; backdrop-filter:blur(12px); }
.lyrics-header { display:flex; align-items:flex-start; justify-content:space-between; gap:16px; text-align:left; }
.lyrics-track { display:flex; flex-direction:column; gap:4px; min-width:0; }
.eyebrow { color:#aeb9d4; font-size:9px; letter-spacing:.16em; }
.lyrics-track strong { overflow:hidden; color:#dce3f5; font-size:12px; font-weight:550; text-overflow:ellipsis; white-space:nowrap; }
.header-actions { --wails-draggable:no-drag; display:flex; gap:6px; flex:0 0 auto; }
.settings-button,.close-button { display:flex; align-items:center; justify-content:center; width:28px; height:28px; padding:0; border:0; border-radius:7px; background:#ffffff16; color:#e8ecf5; cursor:pointer; }
.settings-button { font-size:16px; }
.close-button { font-size:22px; line-height:1; }
.settings-button:hover,.close-button:hover { background:#ffffff2b; }
.settings-panel { --wails-draggable:no-drag; position:absolute; z-index:5; top:49px; right:22px; display:flex; flex-direction:column; gap:11px; width:230px; padding:14px; border:1px solid #ffffff30; border-radius:10px; background:rgba(20,25,37,.97); box-shadow:0 8px 28px #0008; text-align:left; }
.settings-heading { display:flex; align-items:center; justify-content:space-between; gap:8px; margin-bottom:2px; color:#f6f7fb; font-size:13px; }
.reset-button { padding:3px 7px; border:1px solid #ffffff30; border-radius:5px; background:#ffffff12; color:#cbd5e8; font-size:11px; cursor:pointer; }
.style-control { display:flex; flex-direction:column; gap:6px; color:#dce3f5; font-size:12px; }
.style-control span { display:flex; justify-content:space-between; align-items:center; }
.style-control b { color:#aeb9d4; font-weight:500; }
.style-control input[type="color"] { width:100%; height:27px; padding:2px; border:1px solid #ffffff30; border-radius:5px; background:#ffffff12; cursor:pointer; }
.style-control input[type="range"] { width:100%; accent-color:#8da8ed; cursor:pointer; }
.shadow-control { display:flex; align-items:center; gap:8px; color:#dce3f5; font-size:12px; }
.shadow-control input { accent-color:#8da8ed; }
.lyrics-content { display:flex; flex:1; flex-direction:column; justify-content:center; gap:7px; min-height:0; padding:4px 20px; }
.original-lyric { margin:0; font-weight:650; line-height:1.25; overflow-wrap:anywhere; }
.translated-lyric { margin:0; font-weight:450; line-height:1.3; overflow-wrap:anywhere; }
.lyrics-footer { display:flex; justify-content:space-between; color:#9ba7bf; font-size:10px; font-variant-numeric:tabular-nums; }
</style>
