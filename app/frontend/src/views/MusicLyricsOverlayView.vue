<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import * as MusicService from '../../bindings/github.com/dongguo4812/My-LiveHub/app/internal/desktop/musicservice.js'
import * as MusicLyricsWindowService from '../../bindings/github.com/dongguo4812/My-LiveHub/app/internal/desktop/musiclyricswindowservice.js'
import { getActiveSyncedLyric, parseSyncedLyrics, type SyncedLyricLine } from '../utils/syncedLyrics'

type PlaybackSnapshot = Awaited<ReturnType<typeof MusicService.GetPlaybackStatus>>

const playback = ref<PlaybackSnapshot | null>(null)
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
  return getActiveSyncedLyric(syncedLyrics.value, playback.value.position_ms) ?? { text: '歌词加载中…', translation: '' }
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
  void refreshPlayback()
  refreshTimer = setInterval(() => void refreshPlayback(), 500)
})
onUnmounted(() => {
  if (refreshTimer) clearInterval(refreshTimer)
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
      <button class="close-button" title="关闭桌面歌词" aria-label="关闭桌面歌词" @click="closeWindow">×</button>
    </header>
    <section class="lyrics-content">
      <p class="original-lyric">{{ activeLyric.text }}</p>
      <p v-if="activeLyric.translation" class="translated-lyric">{{ activeLyric.translation }}</p>
    </section>
    <footer class="lyrics-footer">
      <span>{{ playbackLabel }}</span>
      <span v-if="playback?.duration_ms">{{ Math.floor((playback.position_ms ?? 0) / 1000 / 60) }}:{{ String(Math.floor((playback.position_ms ?? 0) / 1000) % 60).padStart(2, '0') }}</span>
    </footer>
  </main>
</template>

<style scoped>
:global(html), :global(body), :global(#app) { width:100%; height:100%; margin:0; overflow:hidden; background:transparent !important; }
.lyrics-overlay { --wails-draggable:drag; box-sizing:border-box; display:flex; flex-direction:column; justify-content:space-between; width:100%; height:100%; padding:15px 22px 12px; border:1px solid #ffffff35; border-radius:14px; background:rgba(12,16,24,.86); color:#f6f7fb; font-family:Inter,"Segoe UI","Microsoft YaHei",sans-serif; text-align:center; box-shadow:0 8px 28px #0005; backdrop-filter:blur(12px); }
.lyrics-header { display:flex; align-items:flex-start; justify-content:space-between; gap:16px; text-align:left; }
.lyrics-track { display:flex; flex-direction:column; gap:4px; min-width:0; }
.eyebrow { color:#aeb9d4; font-size:9px; letter-spacing:.16em; }
.lyrics-track strong { overflow:hidden; color:#dce3f5; font-size:12px; font-weight:550; text-overflow:ellipsis; white-space:nowrap; }
.close-button { --wails-draggable:no-drag; flex:0 0 28px; width:28px; height:28px; padding:0; border:0; border-radius:7px; background:#ffffff16; color:#e8ecf5; font-size:22px; line-height:1; cursor:pointer; }
.close-button:hover { background:#ffffff2b; }
.lyrics-content { display:flex; flex:1; flex-direction:column; justify-content:center; gap:7px; min-height:0; padding:4px 20px; }
.original-lyric { margin:0; color:#ffffff; font-size:27px; font-weight:650; line-height:1.25; text-shadow:0 2px 12px #0008; overflow-wrap:anywhere; }
.translated-lyric { margin:0; color:#c6d0e6; font-size:18px; font-weight:450; line-height:1.3; overflow-wrap:anywhere; }
.lyrics-footer { display:flex; justify-content:space-between; color:#9ba7bf; font-size:10px; font-variant-numeric:tabular-nums; }
</style>
