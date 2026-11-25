<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import { Headset, VideoPause, VideoPlay } from '@element-plus/icons-vue'
import '../styles/player-center.css'
import { getHTMLAudioVolume } from '../services/htmlAudio'
import * as MusicService from '../../bindings/github.com/dongguo4812/My-LiveHub/app/internal/desktop/musicservice.js'
import * as MusicLyricsWindowService from '../../bindings/github.com/dongguo4812/My-LiveHub/app/internal/desktop/musiclyricswindowservice.js'

type MusicConfig = Awaited<ReturnType<typeof MusicService.GetConfig>>
type Track = Awaited<ReturnType<typeof MusicService.Search>>['tracks'][number]
type Playlist = Awaited<ReturnType<typeof MusicService.ListPlaylists>>[number]
type QueueSnapshot = Awaited<ReturnType<typeof MusicService.GetQueue>>
type PlaybackSnapshot = Awaited<ReturnType<typeof MusicService.GetPlaybackStatus>>

type MusicTab = 'now-playing' | 'search' | 'visitor-requests' | 'settings'
const activeTab = ref<MusicTab>('now-playing')
const route = useRoute()
watch(() => route.query.tab, value => {
  if (value === 'search' || value === 'visitor-requests' || value === 'settings' || value === 'now-playing') activeTab.value = value
}, { immediate: true })

const config = ref<MusicConfig | null>(null)
type MusicSourceOption = { id: string; name: string }
const musicSourceOptions: MusicSourceOption[] = [
  { id: 'tx', name: 'QQ 音乐' },
  { id: 'kw', name: '酷我音乐' },
  { id: 'kg', name: '酷狗音乐' },
  { id: 'wy', name: '网易云音乐' },
  { id: 'mg', name: '咪咕音乐' },
]
const searchableSources = computed(() => {
  const enabled = config.value?.enabled_search_sources ?? []
  return musicSourceOptions.filter(item => enabled.includes(item.id))
})
const availableSearchSources = computed(() => {
  const enabled = config.value?.enabled_search_sources ?? []
  return musicSourceOptions.filter(item => !enabled.includes(item.id))
})
function getLXEndpoint(index: number) {
  return config.value?.lx_api_endpoints[index] ?? ''
}
function setLXEndpoint(index: number, value: string) {
  if (!config.value) return
  while (config.value.lx_api_endpoints.length <= index) config.value.lx_api_endpoints.push('')
  config.value.lx_api_endpoints[index] = value
}
function swapLXEndpoints() {
  if (!config.value || config.value.lx_api_endpoints.length < 2) return
  ;[config.value.lx_api_endpoints[0], config.value.lx_api_endpoints[1]] = [config.value.lx_api_endpoints[1], config.value.lx_api_endpoints[0]]
}
function getSourceName(id: string): string {
  return musicSourceOptions.find((item: MusicSourceOption) => item.id === id)?.name ?? id
}
function setSearchSourceEnabled(id: string, enabled: boolean) {
  if (!config.value) return
  const current = config.value.enabled_search_sources ?? []
  config.value.enabled_search_sources = enabled
    ? [...current.filter(item => item !== id), id]
    : current.filter(item => item !== id)
}
function moveSearchSource(index: number, direction: -1 | 1) {
  if (!config.value) return
  const sources = [...(config.value.enabled_search_sources ?? [])]
  const target = index + direction
  if (target < 0 || target >= sources.length) return
  ;[sources[index], sources[target]] = [sources[target], sources[index]]
  config.value.enabled_search_sources = sources
}
const queue = ref<QueueSnapshot | null>(null)
const draggedQueueItemID = ref('')
const playback = ref<PlaybackSnapshot | null>(null)
const results = ref<Track[]>([])
const playlists = ref<Playlist[]>([])
const playlistDialogVisible = ref(false)
const playlistLoading = ref(false)
const playlistAdding = ref(false)
const selectedPlaylistID = ref('')
const trackToAdd = ref<Track | null>(null)
const searchErrors = ref<Record<string, string>>({})
const qualitySelections = ref<Record<string, string>>({})
const query = ref('')
const selectedSource = ref('all')

const searching = ref(false)
const currentPage = ref(1)
const maxPage = ref(0)
const total = ref(0)
const pageSize = ref(30)
let searchRequestID = 0
const saving = ref(false)
const error = ref('')
const success = ref('')

let statusTimer: ReturnType<typeof setInterval> | undefined
let playbackClockTimer: ReturnType<typeof setInterval> | undefined
let playbackPositionAnchor = 0
let playbackPositionAnchorAt = 0
const displayedPositionMs = ref(0)
const isSeeking = ref(false)
const seekPreviewMs = ref(0)
const playbackVolume = ref(100)
const lastAudibleVolume = ref(100)
const canPrevious = ref(false)
const playlistPlaybackMode = ref('sequential')
const playlistPlaybackActive = ref(false)
const canNextTrack = computed(() => (queue.value?.items.length ?? 0) > 0 || playlistPlaybackActive.value)

const progress = computed(() => {
  if (!playback.value || playback.value.duration_ms <= 0) return 0
  const position = isSeeking.value ? seekPreviewMs.value : displayedPositionMs.value
  return Math.min(100, Math.max(0, position / playback.value.duration_ms * 100))
})
const searchErrorText = computed(() => Object.values(searchErrors.value).join('；'))
function formatDuration(value?: number) {
  if (value == null || !Number.isFinite(Number(value)) || Number(value) < 0) return '--:--'
  // Playback snapshots and seek previews are both expressed in milliseconds.
  const numeric = Number(value)
  const seconds = Math.floor(numeric / 1000)
  return `${Math.floor(seconds / 60)}:${String(seconds % 60).padStart(2, '0')}`
}
function updatePlaybackClock() {
  if (isSeeking.value) return
  if (!playback.value) { displayedPositionMs.value = 0; return }
  const position = playback.value.state === 'playing'
    ? playbackPositionAnchor + Date.now() - playbackPositionAnchorAt
    : playbackPositionAnchor
  const duration = playback.value.duration_ms ?? 0
  displayedPositionMs.value = duration > 0 ? Math.min(duration, position) : Math.max(0, position)
}
function sourceName(id?: string) {
  const names: Record<string, string> = { kw: '酷我音乐', kg: '酷狗音乐', tx: 'QQ 音乐', wy: '网易云音乐', mg: '咪咕音乐' }
  return id ? (names[id] ?? id) : '来源未知'
}
function playbackSourceName(track?: Track | null) {
  return sourceName(track?.lx_source_key || track?.source_id)
}
function qualityNames(track: Track) {
  return (track.qualities ?? []).map(item => item.type).join(' / ') || '未知'
}
function trackRowKey(row: Track) { return `${row.source_id}:${row.id}` }
async function loadConfig() {
  error.value = ''
  try {
    config.value = await MusicService.GetConfig()
    config.value.lx_api_endpoints ??= []
    config.value.enabled_search_sources ??= []
    const savedVolume = Math.min(100, Math.max(0, Number(config.value.volume ?? 1) * 100))
    playbackVolume.value = savedVolume
    if (savedVolume > 0) lastAudibleVolume.value = savedVolume
    // Re-apply after the frontend Audio singleton has subscribed to commands.
    await MusicService.SetVolume(savedVolume / 100)
    await refreshQueue()
  } catch (reason) {
    error.value = reason instanceof Error ? reason.message : String(reason)
  } finally {
  }
}
async function refreshQueue() {
  try { queue.value = await MusicService.GetQueue() } catch { /* Keep the last known queue. */ }
}
function startQueueDrag(event: DragEvent, id: string) {
  draggedQueueItemID.value = id
  if (event.dataTransfer) {
    event.dataTransfer.effectAllowed = 'move'
    event.dataTransfer.setData('text/plain', id)
  }
}
async function dropQueueItem(event: DragEvent, targetIndex: number) {
  event.preventDefault()
  const id = event.dataTransfer?.getData('text/plain') || draggedQueueItemID.value
  draggedQueueItemID.value = ''
  if (!id || !queue.value) return
  const fromIndex = queue.value.items.findIndex(item => item.id === id)
  if (fromIndex < 0 || fromIndex === targetIndex) return
  error.value = ''
  try {
    await MusicService.MoveQueueItem(id, targetIndex)
    await refreshQueue()
  } catch (reason) {
    error.value = reason instanceof Error ? reason.message : String(reason)
  }
}
async function removeQueueItem(id: string) {
  error.value = ''
  try {
    await MusicService.RemoveQueueItem(id)
    await refreshQueue()
    success.value = '已从观众点播队列移除'
  } catch (reason) {
    error.value = reason instanceof Error ? reason.message : String(reason)
  }
}
async function clearQueue() {
  if (!queue.value?.items.length) return
  if (!window.confirm(`确定清空全部 ${queue.value.items.length} 首待播歌曲吗？当前正在播放的歌曲不会受到影响。`)) return
  error.value = ''
  try {
    await MusicService.ClearQueue()
    await refreshQueue()
    success.value = '观众点播待播队列已清空'
  } catch (reason) {
    error.value = reason instanceof Error ? reason.message : String(reason)
  }
}
async function refreshPlayback() {
  // 洛雪 can be playing a song started outside My-LiveHub's request queue.
  // Read the backend's shared playback snapshot so both external and queued
  // playback are reflected without issuing another direct 洛雪 API request.
  await refreshQueue()
  try {
    playback.value = await MusicService.GetCachedPlaybackStatus()
    canPrevious.value = await MusicService.CanPrevious()
    playlistPlaybackMode.value = await MusicService.GetPlaylistPlaybackMode()
    playlistPlaybackActive.value = await MusicService.GetPlaylistPlaybackActive()
    playbackPositionAnchor = playback.value.position_ms ?? 0
    playbackPositionAnchorAt = Date.now()
    updatePlaybackClock()
  } catch { /* Keep the last known playback snapshot if the cache is unavailable. */ }
}
async function saveConfig() {
  if (!config.value) return
  saving.value = true
  error.value = ''
  success.value = ''
  try {
    config.value.lx_api_endpoints = config.value.lx_api_endpoints.map(item => item.trim()).filter(Boolean)
    await MusicService.SaveConfig(config.value)
    success.value = '音乐设置已保存'
    config.value = await MusicService.GetConfig()
    selectedSource.value = 'all'
  } catch (reason) {
    error.value = reason instanceof Error ? reason.message : String(reason)
  } finally {
    saving.value = false
  }
}
async function loadSearchPage(page: number) {
  if (!query.value.trim()) { error.value = '请输入歌曲名称或歌手'; return }
  const requestID = ++searchRequestID
  searching.value = true
  error.value = ''
  success.value = ''
  searchErrors.value = {}
  try {
    const response = selectedSource.value === 'all'
      ? await MusicService.SearchPage(query.value, page)
      : await MusicService.SearchSourcePage(query.value, selectedSource.value, page)
    if (requestID !== searchRequestID) return
    results.value = response.tracks ?? []
    currentPage.value = response.page ?? page
    maxPage.value = response.max_page ?? 1
    total.value = response.total ?? results.value.length
    pageSize.value = response.page_size ?? pageSize.value
    searchErrors.value = Object.fromEntries(
      Object.entries(response.errors ?? {}).filter((entry): entry is [string, string] => typeof entry[1] === 'string'),
    )
    if (results.value.length === 0 && Object.keys(searchErrors.value).length === 0) success.value = '没有找到匹配歌曲'
  } catch (reason) {
    if (requestID === searchRequestID) error.value = reason instanceof Error ? reason.message : String(reason)
  } finally {
    if (requestID === searchRequestID) searching.value = false
  }
}
function search() {
  currentPage.value = 1
  maxPage.value = 0
  total.value = 0
  void loadSearchPage(1)
}
async function openAddToPlaylist(track: Track) {
  trackToAdd.value = track
  selectedPlaylistID.value = ''
  playlistDialogVisible.value = true
  playlistLoading.value = true
  error.value = ''
  try {
    playlists.value = await MusicService.ListPlaylists()
    selectedPlaylistID.value = playlists.value[0]?.id ?? ''
  } catch (reason) {
    error.value = reason instanceof Error ? reason.message : String(reason)
  } finally {
    playlistLoading.value = false
  }
}
async function confirmAddToPlaylist() {
  if (!trackToAdd.value || !selectedPlaylistID.value) return
  const playlist = playlists.value.find(item => item.id === selectedPlaylistID.value)
  if (!playlist) return
  playlistAdding.value = true
  error.value = ''
  try {
    await MusicService.AddPlaylistTrack(playlist.id, trackToAdd.value)
    success.value = `已添加到歌单「${playlist.name}」：${trackToAdd.value.title} - ${trackToAdd.value.artist || '未知歌手'}`
    playlistDialogVisible.value = false
    trackToAdd.value = null
  } catch (reason) {
    error.value = reason instanceof Error ? reason.message : String(reason)
  } finally {
    playlistAdding.value = false
  }
}
async function addToQueue(track: Track) {
  error.value = ''
  try {
    const selectedQuality = qualitySelections.value[trackRowKey(track)] ?? ''
    const queuedTrack = Object.assign({}, track, { selected_quality: selectedQuality })
    await MusicService.Enqueue(queuedTrack, '__my_livehub_search_play__')
    success.value = `已加入队列：${track.title}${selectedQuality ? `（${selectedQuality}）` : '（自动选择最佳音质）'}`
    await refreshQueue()
  } catch (reason) {
    error.value = reason instanceof Error ? reason.message : String(reason)
  }
}
async function playSearchTrackNow(track: Track) {
  error.value = ''
  try {
    const selectedQuality = qualitySelections.value[trackRowKey(track)] ?? ''
    const immediateTrack = Object.assign({}, track, { selected_quality: selectedQuality })
    await MusicService.PlayTrackNow(immediateTrack)
    await Promise.all([refreshPlayback(), refreshQueue()])
    success.value = `正在播放：${track.title}（未加入观众点播队列）`
  } catch (reason) {
    error.value = reason instanceof Error ? reason.message : String(reason)
  }
}
async function setPlaylistPlaybackMode(mode: string) {
  error.value = ''
  try { await MusicService.SetPlaylistPlaybackMode(mode); playlistPlaybackMode.value = mode }
  catch (reason) { error.value = reason instanceof Error ? reason.message : String(reason) }
}
async function nextTrack() {
  error.value = ''
  try {
    await MusicService.Next()
    await refreshPlayback()
  } catch (reason) {
    error.value = reason instanceof Error ? reason.message : String(reason)
  }
}
async function previousTrack() {
  error.value = ''
  try {
    await MusicService.Previous()
    await refreshPlayback()
  } catch (reason) {
    error.value = reason instanceof Error ? reason.message : String(reason)
  }
}
async function pause() { try { await MusicService.Pause(); await refreshPlayback() } catch (reason) { error.value = String(reason) } }
async function openLyricsWindow() {
  error.value = ''
  try { await MusicLyricsWindowService.OpenWindow() }
  catch (reason) { error.value = reason instanceof Error ? reason.message : String(reason) }
}
async function resume() { try { await MusicService.Resume(); await refreshPlayback() } catch (reason) { error.value = String(reason) } }
async function stopPlayback() {
  error.value = ''
  try {
    await MusicService.Stop()
    isSeeking.value = false
    seekPreviewMs.value = 0
    playbackPositionAnchor = 0
    playbackPositionAnchorAt = Date.now()
    displayedPositionMs.value = 0
    await refreshPlayback()
  } catch (reason) {
    error.value = reason instanceof Error ? reason.message : String(reason)
  }
}
async function seekPlayback(value: number | number[]) {
  const duration = playback.value?.duration_ms ?? 0
  const percentage = Math.min(100, Math.max(0, Number(Array.isArray(value) ? value[0] : value)))
  const target = Math.round(percentage * duration / 100)
  try {
    await MusicService.Seek(target)
    playbackPositionAnchor = target
    playbackPositionAnchorAt = Date.now()
    displayedPositionMs.value = target
  } catch (reason) {
    error.value = reason instanceof Error ? reason.message : String(reason)
    updatePlaybackClock()
  } finally {
    isSeeking.value = false
  }
}
async function setPlaybackVolume(value: number | number[]) { try { await MusicService.SetVolume(Number(Array.isArray(value) ? value[0] : value) / 100); } catch (reason) { error.value = String(reason) } }
function handleSeekInput(value: number | number[]) {
  const percentage = Math.min(100, Math.max(0, Number(Array.isArray(value) ? value[0] : value)))
  const duration = playback.value?.duration_ms ?? 0
  if (duration <= 0) return
  isSeeking.value = true
  seekPreviewMs.value = Math.round(percentage * duration / 100)
}
function handleSeekChange(value: number | number[]) {
  void seekPlayback(value)
}
function handleVolumeChange(value: number | number[]) {
  const numeric = Math.min(100, Math.max(0, Number(Array.isArray(value) ? value[0] : value)))
  playbackVolume.value = numeric
  if (numeric > 0) lastAudibleVolume.value = numeric
  void setPlaybackVolume(numeric)
}
function toggleMute() {
  if (playbackVolume.value > 0) {
    lastAudibleVolume.value = playbackVolume.value
    handleVolumeChange(0)
  } else {
    handleVolumeChange(lastAudibleVolume.value > 0 ? lastAudibleVolume.value : 100)
  }
}
onMounted(() => {
  playbackVolume.value = getHTMLAudioVolume()
  if (playbackVolume.value > 0) lastAudibleVolume.value = playbackVolume.value
  void loadConfig()
  void refreshPlayback()
  statusTimer = setInterval(() => void refreshPlayback(), 1500)
  playbackClockTimer = setInterval(updatePlaybackClock, 100)
})
onUnmounted(() => {
  if (statusTimer) clearInterval(statusTimer)
  if (playbackClockTimer) clearInterval(playbackClockTimer)
})
</script>

<template>
  <section class="music-page player-page">
    <header class="page-heading player-heading">
      <div>
        <p class="eyebrow">MUSIC PLAYER</p>
        <h2>音乐中心</h2>
        <p class="description">管理独立 HTMLAudio 音乐播放、歌曲搜索与观众点播队列。</p>
        <el-alert v-if="config && !config.enabled" title="音乐模块当前已关闭：无法加入歌曲队列或播放。请在下方「点歌设置」中启用音乐模块并保存全部设置。" type="warning" :closable="false" show-icon style="margin-top:12px" />
      </div>
      <el-tag type="success" effect="plain">直播音乐控制台</el-tag>
    </header>

    <el-alert v-if="error" :title="error" type="error" :closable="true" show-icon @close="error = ''" />
    <el-alert v-if="playback?.error" :title="playback.error" type="error" :closable="false" show-icon />
    <el-alert v-if="success" :title="success" type="success" :closable="true" show-icon @close="success = ''" />

    <nav class="music-tabs player-tabs" aria-label="音乐页面导航">
      <button :class="{ active: activeTab === 'now-playing' }" @click="activeTab = 'now-playing'">正在播放</button>
      <button :class="{ active: activeTab === 'search' }" @click="activeTab = 'search'">歌曲搜索</button>
      <button :class="{ active: activeTab === 'visitor-requests' }" @click="activeTab = 'visitor-requests'">观众点播 <span>{{ queue?.items.length ?? 0 }}</span></button>
      <button @click="$router.push('/music/playlists')">我的列表</button>
      <button :class="{ active: activeTab === 'settings' }" @click="activeTab = 'settings'">音乐设置</button>
      <button @click="$router.push('/music/sources')">JS 音源脚本管理</button>
    </nav>

    <el-card v-if="activeTab === 'now-playing'" class="player-card" shadow="never">
      <template #header><div class="card-heading"><strong>正在播放</strong><div style="display:flex;align-items:center;gap:10px"><el-tag :type="playback?.resolution_status === 'resolving' ? 'warning' : playback?.state === 'playing' ? 'success' : playback?.state === 'error' ? 'danger' : 'info'">{{ playback?.resolution_status === 'resolving' ? '正在解析' : playback?.state === 'error' ? '播放失败' : playback?.state ?? '播放器未启动' }}</el-tag><el-button size="small" :disabled="!playback?.track" @click="openLyricsWindow">桌面歌词</el-button></div></div></template>
      <div class="now-playing">
        <div class="track-cover">
          <img v-if="playback?.track?.cover_url" :src="playback.track.cover_url" alt="歌曲封面" />
          <el-icon v-else><Headset /></el-icon>
        </div>
        <div class="track-info">
          <strong>{{ playback?.track ? `${playback.track.title} - ${playback.track.artist || '未知歌手'}` : '尚未播放歌曲' }}</strong>
          <small v-if="playback?.track">{{ playbackSourceName(playback.track) }}</small>
          <small v-if="playback?.track?.resolved_quality">
            取链成功档位：{{ playback.track.resolved_quality }}
            <span v-if="playback.track.selected_quality && playback.track.resolved_quality !== playback.track.selected_quality">（请求{{ playback.track.selected_quality }}，音源已降级）</span>
          </small>
          <small v-if="playback?.resolution_status === 'resolving'" class="resolution-status">正在解析播放地址，请稍候…</small>
          
          <el-tag v-if="queue?.current && queue.current.requested_by !== '__my_livehub_search_play__' && queue.current.requested_by !== '__my_livehub_playlist_play__'" type="warning" size="small">观众点播</el-tag>
          <el-tag v-else-if="queue?.current?.requested_by === '__my_livehub_playlist_play__'" type="success" size="small">歌单播放</el-tag>
        </div>
      </div>
      <div class="progress-row">
        <span class="progress-time">{{ formatDuration(isSeeking ? seekPreviewMs : displayedPositionMs) }}</span>
        <el-slider :model-value="progress" :min="0" :max="100" :step="0.1" :disabled="!playback?.track || (playback?.duration_ms ?? 0) <= 0" @input="handleSeekInput" @change="handleSeekChange" />
        <span class="progress-time">{{ formatDuration(playback?.duration_ms) }}</span>
      </div>
      <div class="volume-row"><span>音量</span><el-slider :model-value="playbackVolume" :min="0" :max="100" :step="1" style="max-width:220px" @input="handleVolumeChange" /><el-button size="small" @click="toggleMute">{{ playbackVolume === 0 ? "恢复声音" : "静音" }}</el-button></div>
      <p class="hint">播放进度和音量由 My-LiveHub 内置 HTMLAudio 控制。</p>
      <div class="playback-order-row">
        <span>歌单播放模式</span>
        <el-radio-group :model-value="playlistPlaybackMode" size="small" @change="setPlaylistPlaybackMode">
          <el-radio-button value="sequential">顺序播放</el-radio-button>
          <el-radio-button value="loop">列表循环</el-radio-button>
          <el-radio-button value="random">随机播放</el-radio-button>
        </el-radio-group>
      </div>
      <p class="hint">播放模式仅作用于当前歌单；观众点播队列始终优先，且不受随机或循环模式影响。</p>
      <div class="player-controls">
        <el-button :disabled="playback?.state !== 'playing'" @click="pause">暂停</el-button>
        <el-button :disabled="playback?.state !== 'paused'" @click="resume">继续</el-button>
        <el-button :disabled="!playback?.track" @click="stopPlayback">停止</el-button>
        <el-button :disabled="!canPrevious" @click="previousTrack">上一首</el-button>
        <el-button :disabled="!canNextTrack" @click="nextTrack">下一首</el-button>
      </div>
      <p class="hint">播放由 My-LiveHub 内置 HTMLAudio 负责，无需启动外部播放器。</p>
    </el-card>

    <el-card v-if="activeTab === 'search'" class="player-card" shadow="never">
      <template #header><strong>搜索歌曲</strong></template>
      <p class="section-description">搜索平台及优先级在音乐设置中统一管理。</p>
      <div class="search-row">
        <el-select v-model="selectedSource" style="width:160px" @change="search">
          <el-option label="全部可用音源" value="all" :disabled="searchableSources.length === 0" />
          <el-option v-for="source in searchableSources" :key="source.id" :label="source.name" :value="source.id" />
        </el-select>
        <el-input v-model="query" placeholder="输入歌曲名或歌手，例如：稻香 周杰伦" clearable @keyup.enter="search" />
        <el-button type="primary" :loading="searching" @click="search">搜索</el-button>
      </div>
      <el-alert v-if="searchErrorText" :title="searchErrorText" type="warning" :closable="false" show-icon />
      <div v-if="total > 0" class="search-summary">共 {{ total }} 条结果 · 第 {{ currentPage }} / {{ maxPage || 1 }} 页</div>
      <el-table v-if="results.length" :data="results" :row-key="trackRowKey" style="width:100%;margin-top:14px">
        <el-table-column prop="title" label="歌曲" min-width="180" />
        <el-table-column prop="artist" label="歌手" min-width="140" />
        <el-table-column prop="album" label="专辑" min-width="140" />
        <el-table-column label="音源" width="130"><template #default="scope">{{ sourceName(scope.row.source_id) }}</template></el-table-column>
        <el-table-column label="可用音质" min-width="150"><template #default="scope">{{ qualityNames(scope.row) }}</template></el-table-column>
        <el-table-column label="播放音质" min-width="135"><template #default="scope"><el-select v-model="qualitySelections[trackRowKey(scope.row)]" size="small" clearable placeholder="自动"><el-option v-for="quality in (scope.row.qualities ?? [])" :key="quality.type" :label="quality.type" :value="quality.type" /></el-select></template></el-table-column>
        <el-table-column label="时长" width="90"><template #default="scope">{{ formatDuration(scope.row.duration_ms) }}</template></el-table-column>
        <el-table-column label="操作" width="250" fixed="right"><template #default="scope"><el-button link type="success" @click="playSearchTrackNow(scope.row)">立即播放</el-button><el-button link type="primary" @click="addToQueue(scope.row)">加入队列</el-button><el-button link type="warning" @click="openAddToPlaylist(scope.row)">添加歌单</el-button></template></el-table-column>
      </el-table>
      <div v-if="maxPage > 1" class="search-pagination">
        <el-pagination
          :current-page="currentPage"
          :page-count="maxPage"
          :page-size="pageSize"
          layout="prev, pager, next, jumper"
          :disabled="searching"
          @current-change="loadSearchPage"
        />
      </div>
      <el-empty v-else-if="!searching && results.length === 0" description="搜索结果将在这里显示" :image-size="76" />
    </el-card>

    <el-dialog v-model="playlistDialogVisible" title="添加到歌单" width="420px">
      <div v-if="trackToAdd" class="playlist-add-track">
        <strong>{{ trackToAdd.title }}</strong>
        <span>{{ trackToAdd.artist || '未知歌手' }} · {{ sourceName(trackToAdd.source_id) }}</span>
      </div>
      <el-skeleton v-if="playlistLoading" :rows="2" animated />
      <template v-else>
        <el-select v-if="playlists.length" v-model="selectedPlaylistID" style="width:100%" placeholder="选择目标歌单">
          <el-option v-for="playlist in playlists" :key="playlist.id" :label="`${playlist.name}（${playlist.tracks.length} 首）`" :value="playlist.id" />
        </el-select>
        <el-empty v-else description="还没有歌单，请先创建一个歌单" :image-size="72">
          <el-button type="primary" @click="playlistDialogVisible = false; $router.push('/music/playlists')">前往我的列表创建</el-button>
        </el-empty>
      </template>
      <template #footer>
        <el-button @click="playlistDialogVisible = false">取消</el-button>
        <el-button type="primary" :loading="playlistAdding" :disabled="playlistLoading || !playlists.length || !selectedPlaylistID" @click="confirmAddToPlaylist">确认添加</el-button>
      </template>
    </el-dialog>

    <el-card v-if="activeTab === 'visitor-requests'" class="player-card" shadow="never">
      <template #header>
        <div class="card-heading">
          <div class="card-heading"><strong>观众点播队列</strong><el-tag effect="plain">{{ queue?.items.length ?? 0 }} 首待播</el-tag></div>
          <el-button type="danger" plain size="small" :disabled="!queue?.items.length" @click="clearQueue">清空待播</el-button>
        </div>
      </template>
      <div v-if="queue?.current" class="queue-current">当前播放：{{ queue.current.track.title }} - {{ queue.current.track.artist || '未知歌手' }}（{{ queue.current.requested_by === '__my_livehub_playlist_play__' ? '歌单播放' : queue.current.requested_by || '观众点播' }}）</div>
      <div v-if="queue?.items.length" class="queue-list">
        <div
          v-for="(item, index) in queue.items"
          :key="item.id"
          class="queue-item"
          :class="{ 'queue-item-dragging': draggedQueueItemID === item.id }"
          draggable="true"
          @dragstart="startQueueDrag($event, item.id)"
          @dragover.prevent
          @drop="dropQueueItem($event, index)"
          @dragend="draggedQueueItemID = ''"
        >
          <span class="queue-drag-handle" title="拖动调整顺序">⠿</span>
          <span class="queue-index">{{ index + 1 }}</span>
          <div class="queue-track">
            <strong>{{ item.track.title }} - {{ item.track.artist || '未知歌手' }}</strong>
            <span>{{ sourceName(item.track.source_id) }}</span>
          </div>
          <span v-if="item.requested_by !== '__my_livehub_search_play__'" class="queue-requester">{{ item.requested_by || '观众' }}</span>
          <el-button link type="danger" @click.stop="removeQueueItem(item.id)">删除</el-button>
        </div>
      </div>
      <el-empty v-else description="队列暂时为空" :image-size="76" />
      <p v-if="queue?.items.length" class="hint">拖动歌曲左侧的拖动图标即可调整待播顺序；当前播放歌曲不受影响。</p>
    </el-card>

    <el-card v-if="activeTab === 'settings'" class="player-card player-settings-card" shadow="never">
      <template #header><strong>点歌设置</strong></template>
      <div v-if="config" class="setting-list">
        <div class="setting-row"><div><strong>启用音乐模块</strong><p>关闭后无法加入歌曲队列或播放歌曲，也不会处理弹幕点歌；MV 视频播放不受影响。</p></div><el-switch v-model="config.enabled" /></div>
        <el-divider />
        <div class="setting-row"><div><strong>响度标准化</strong><p>实时测量播放响度并平滑调整增益，使用本地媒体代理解决跨域音频接入，并通过峰值限制减少突然过响；关闭后恢复原生 HTMLAudio 播放。</p></div><el-switch v-model="config.loudness_normalization" /></div>
        <el-divider />
        <div class="setting-row"><div><strong>允许弹幕点歌</strong><p>收到点歌指令后搜索已启用的音源并加入统一队列。</p></div><el-switch v-model="config.command_enabled" /></div>
        <el-divider />
        <div class="setting-row"><div><strong>粉丝勋章等级限制</strong><p>只检查用户是否拥有当前主播的粉丝勋章，不要求佩戴；主播和房管免检。</p></div><el-switch v-model="config.require_fan_medal" /></div>
        <el-divider />
        <div class="setting-row"><div><strong>最低粉丝勋章等级</strong><p>未拥有当前主播粉丝勋章或等级不足时，拒绝本次点歌。</p></div><el-input-number v-model="config.fan_medal_level" :min="1" :max="120" /></div>
        <el-divider />
        <div class="setting-row"><div><strong>点歌指令前缀</strong><p>例如：点歌 稻香 周杰伦。</p></div><el-input v-model="config.command_prefix" maxlength="16" style="width:220px" /></div>
        <el-divider />
        <div class="setting-row"><div><strong>允许主播和房管切歌</strong><p>只有当前直播间主播本人或房管发送切歌命令时，才会播放下一首；普通观众无效。</p></div><el-switch v-model="config.skip_command_enabled" /></div>
        <el-divider />
        <div class="setting-row"><div><strong>切歌命令</strong><p>发送完整命令即可切换下一首，例如：切歌。</p></div><el-input v-model="config.skip_command" maxlength="16" style="width:220px" /></div>
        <el-divider />
        <div class="setting-row"><div><strong>播放队列容量</strong><p>统一队列允许等待的歌曲数量。</p></div><el-input-number v-model="config.queue_capacity" :min="1" :max="1000" /></div>
        <el-divider />
        <div class="setting-row"><div><strong>普通观众点播冷却时间（秒）</strong><p>每位普通观众成功点播后，需要等待指定时间才能再次点播；设置为 0 则不限制。主播和房管不受限制。</p></div><el-input-number v-model="config.request_cooldown_seconds" :min="0" :max="86400" :step="5" /></div>
 
      </div>
      <div class="card-actions"><el-button type="primary" :loading="saving" :disabled="!config" @click="saveConfig">保存全部设置</el-button></div>
    </el-card>
    <el-card v-if="activeTab === 'settings'" class="player-card player-settings-card" shadow="never">
      <template #header><strong>旧版洛雪 API 配置（兼容保留）</strong></template>
      <p class="section-description">以下地址仅为兼容历史 music.json 配置保留，不参与当前搜索和 HTMLAudio 播放；新播放器无需安装或启动洛雪客户端。</p>
      <div v-if="config" class="setting-list">
        <div class="setting-row">
          <strong>主 API 地址</strong>
          <el-input :model-value="getLXEndpoint(0)" placeholder="http://127.0.0.1:23330" @update:model-value="setLXEndpoint(0, $event)" />
        </div>
        <div class="setting-row">
          <strong>备用 API 地址</strong>
          <el-input :model-value="getLXEndpoint(1)" placeholder="备用地址（可留空）" @update:model-value="setLXEndpoint(1, $event)" />
        </div>
      </div>
      <div class="card-actions">
        <el-button plain :disabled="!config || config.lx_api_endpoints.length < 2" @click="swapLXEndpoints">交换主备地址</el-button>
        <el-button type="primary" :loading="saving" :disabled="!config" @click="saveConfig">保存旧版配置</el-button>
      </div>
      <el-divider />
      <strong>搜索平台启用及优先级</strong>
      <p class="section-description">勾选参与搜索和自动换源的平台；上方平台优先尝试。QQ 音乐默认排在第一位。</p>
      <div v-if="config" class="setting-list">
        <div v-for="(source, index) in config.enabled_search_sources" :key="source" class="setting-row">
          <div><strong>{{ index + 1 }}. {{ getSourceName(source) }}</strong></div>
          <div style="display:flex;gap:6px">
            <el-button link :disabled="index === 0" @click="moveSearchSource(index, -1)">上移</el-button>
            <el-button link :disabled="index === config.enabled_search_sources.length - 1" @click="moveSearchSource(index, 1)">下移</el-button>
            <el-button link type="danger" @click="setSearchSourceEnabled(source, false)">禁用</el-button>
          </div>
        </div>
        <div v-for="source in availableSearchSources" :key="source.id" class="setting-row">
          <strong>{{ source.name }}</strong>
          <el-button link type="primary" @click="setSearchSourceEnabled(source.id, true)">启用</el-button>
        </div>
      </div>
      <div class="card-actions"><el-button type="primary" :loading="saving" :disabled="!config" @click="saveConfig">保存音乐配置</el-button></div>
    </el-card>

    <div v-if="activeTab === 'search' || activeTab === 'visitor-requests'" class="mini-player">
      <div class="mini-player-track">
        <img v-if="playback?.track?.cover_url" :src="playback.track.cover_url" alt="当前歌曲封面" />
        <el-icon v-else><Headset /></el-icon>
        <div>
          <strong>{{ playback?.track ? `${playback.track.title} - ${playback.track.artist || '未知歌手'}` : '尚未播放歌曲' }}</strong>
        </div>
      </div>
      <div class="mini-player-actions">
        <span>{{ playback?.state === 'playing' ? '正在播放' : playback?.state === 'paused' ? '已暂停' : playback?.state === 'error' ? '播放失败' : '待机中' }}</span>
        <el-button circle text :disabled="playback?.state !== 'playing'" aria-label="暂停" @click="pause"><el-icon><VideoPause /></el-icon></el-button>
        <el-button circle text :disabled="playback?.state !== 'paused'" aria-label="继续播放" @click="resume"><el-icon><VideoPlay /></el-icon></el-button>
        <el-button circle text :disabled="!queue?.items?.length" @click="nextTrack">下一首</el-button>
      </div>
    </div>
  </section>
</template>


<style scoped>
.playlist-header{display:flex;align-items:flex-start;justify-content:space-between;gap:16px}.playlist-header .section-description{margin:7px 0 0}.playlist-header-actions,.playlist-toolbar{display:flex;align-items:center;gap:9px;flex-wrap:wrap}.playlist-toolbar{margin:18px 0 14px}.playlist-cards{display:grid;grid-template-columns:repeat(auto-fill,minmax(190px,1fr));gap:12px;margin:14px 0 20px}.playlist-card{display:flex;flex-direction:column;align-items:flex-start;gap:8px;min-height:112px;padding:15px;border:1px solid var(--app-border);border-radius:11px;background:var(--el-bg-color);color:var(--el-text-color-primary);text-align:left;cursor:pointer;transition:border-color .15s,background .15s}.playlist-card:hover,.playlist-card.active{border-color:var(--app-accent);background:var(--app-bg-control)}.playlist-card.active{box-shadow:inset 0 0 0 1px var(--app-accent)}.playlist-card-icon{font-size:24px;color:var(--app-accent)}.playlist-card-name{font-weight:650}.playlist-card-meta{font-size:12px;color:var(--el-text-color-secondary)}@media(max-width:800px){.playlist-header{flex-direction:column}.playlist-header-actions{width:100%}.playlist-toolbar{align-items:stretch}}.music-tabs{display:flex;align-items:center;gap:6px;overflow-x:auto;padding:0 2px 8px;border-bottom:1px solid var(--app-border)}
.music-tabs button{display:inline-flex;align-items:center;gap:7px;white-space:nowrap;border:0;border-bottom:2px solid transparent;background:transparent;color:var(--el-text-color-secondary);padding:11px 14px;margin-bottom:-1px;font:inherit;font-size:14px;cursor:pointer;transition:color .15s,border-color .15s}
.music-tabs button:hover{color:var(--el-text-color-primary)}
.music-tabs button.active{color:var(--app-accent);border-bottom-color:var(--app-accent);font-weight:600}
.music-tabs button span{font-size:11px;padding:1px 6px;border-radius:10px;background:var(--app-bg-control)}
.mini-player-track>div{min-width:0}.mini-player{position:sticky;bottom:0;z-index:30;display:flex;align-items:center;justify-content:space-between;gap:16px;flex-shrink:0;margin-top:auto;margin-left:-32px;margin-right:-32px;margin-bottom:-28px;padding:12px 32px calc(12px + env(safe-area-inset-bottom));min-height:68px;border:1px solid var(--app-border);border-bottom:0;border-radius:12px 12px 0 0;background:var(--el-bg-color);box-shadow:0 -8px 28px #00000018}
.mini-player-track,.mini-player-actions{display:flex;align-items:center;gap:12px;min-width:0}
.mini-player-track img{width:42px;height:42px;object-fit:cover;border-radius:7px}.mini-player-track>.el-icon{font-size:28px;color:var(--el-text-color-secondary)}
.mini-player-track>div{display:flex;flex-direction:column;gap:3px;min-width:0}
.mini-player-track strong,.mini-player-track span{overflow:hidden;text-overflow:ellipsis;white-space:nowrap;max-width:280px}
.mini-player-track span,.mini-player-actions>span{font-size:12px;color:var(--el-text-color-secondary)}
.music-page{display:flex;flex-direction:column;gap:20px;min-height:calc(100vh - 146px)}.page-heading{display:flex;align-items:flex-start;justify-content:space-between;gap:20px}.page-heading h2{margin:0;font-size:25px}.eyebrow{margin:0 0 9px;color:var(--app-accent);font-size:11px;letter-spacing:.15em}.description,.section-description,.source-copy span,.setting-row p,.hint{color:var(--el-text-color-secondary);font-size:13px}.section-description{margin:0 0 16px}.card-heading{display:flex;align-items:center;justify-content:space-between}.source-list,.setting-list{display:flex;flex-direction:column}.source-row,.setting-row{display:flex;align-items:center;justify-content:space-between;gap:24px;padding:14px 0;border-bottom:1px solid var(--app-border)}.source-row:last-child{border-bottom:0}.source-copy,.track-info{display:flex;flex-direction:column;gap:5px}.source-copy span,.track-info span,.track-info small{font-size:12px;color:var(--el-text-color-secondary)}.setting-row p{margin:6px 0 0}.card-actions{display:flex;justify-content:flex-end;gap:10px;margin-top:20px}.now-playing-list{margin-top:22px;padding-top:18px;border-top:1px solid var(--app-border)}.now-playing-list-header{display:flex;align-items:center;justify-content:space-between;gap:12px;margin-bottom:12px}.now-playing-list-header>div{display:flex;flex-direction:column;gap:4px}.now-playing-list-header strong{font-size:15px}.now-playing-list-header span{font-size:12px;color:var(--el-text-color-secondary)}.now-playing-track-list{display:flex;flex-direction:column;gap:6px}.now-playing-track{display:flex;align-items:center;gap:12px;padding:10px 12px;border:1px solid transparent;border-radius:8px;background:var(--el-fill-color-lighter)}.now-playing-track-active{border-color:var(--app-accent);background:var(--app-bg-control)}.now-playing-track-index{width:28px;color:var(--el-text-color-secondary);font-size:12px;font-variant-numeric:tabular-nums;text-align:center}.now-playing-track-info{display:flex;flex:1;flex-direction:column;gap:3px;min-width:0}.now-playing-track-info strong,.now-playing-track-info span{overflow:hidden;text-overflow:ellipsis;white-space:nowrap}.now-playing-track-info strong{font-size:13px}.now-playing-track-info span{font-size:12px;color:var(--el-text-color-secondary)}.now-playing{display:flex;align-items:center;gap:16px}.track-cover{width:76px;height:76px;border-radius:10px;background:var(--app-bg-control);display:flex;align-items:center;justify-content:center;overflow:hidden;color:var(--el-text-color-secondary);font-size:30px}.track-cover img{width:100%;height:100%;object-fit:cover}.track-info strong{font-size:17px}.time-row{display:flex;justify-content:space-between;color:var(--el-text-color-secondary);font-size:12px;font-variant-numeric:tabular-nums}.player-controls{display:flex;flex-wrap:wrap;justify-content:center;gap:8px;margin:14px 0}.playback-order-row{display:flex;align-items:center;justify-content:center;gap:12px;margin:8px 0 14px}.playback-order-row span{font-size:13px;color:var(--el-text-color-secondary)}.volume-row{display:grid;grid-template-columns:50px 1fr;align-items:center;gap:12px}.volume-row span{font-size:13px;color:var(--el-text-color-secondary)}.hint{margin:12px 0 0}.search-row{display:flex;gap:10px}.lx-import-row{display:flex;gap:10px;align-items:center;min-width:min(520px,55%)}.lx-import-row :deep(.el-input){flex:1;min-width:180px}.lx-import-mode-row :deep(.el-radio-group){flex-shrink:0}.hidden-file-input{display:none}.lx-source-list{display:flex;flex-direction:column;margin-top:16px}.lx-source-row{display:flex;align-items:center;justify-content:space-between;gap:16px;padding:12px 0;border-top:1px solid var(--app-border)}.lx-source-row .source-copy{min-width:0}.lx-source-row .source-copy span{overflow-wrap:anywhere}.lx-search-source-settings{display:flex;flex-direction:column;gap:6px;margin-top:8px}.lx-search-source-checkboxes{display:flex;flex-wrap:wrap;gap:8px 12px}.lx-search-source-toggle{display:inline-flex;align-items:center;gap:7px;padding:4px 9px;border:1px solid var(--app-border);border-radius:6px;background:var(--el-bg-color);color:var(--el-text-color-regular);font:inherit;font-size:13px;cursor:pointer;transition:border-color .15s,background .15s,color .15s}.lx-search-source-toggle:hover{border-color:var(--app-accent)}.lx-search-source-toggle:has(.lx-search-source-checkbox:checked){border-color:var(--app-accent);background:var(--app-bg-control);color:var(--app-accent)}.lx-search-source-checkbox{width:15px;height:15px;margin:0;flex:0 0 15px;accent-color:var(--app-accent);cursor:pointer}.lx-search-priority-list{display:flex;flex-direction:column;align-items:stretch;gap:5px;margin-top:10px;padding:9px 10px;max-width:360px;border:1px solid var(--app-border);border-radius:7px;background:var(--el-fill-color-lighter)}.lx-search-priority-list>strong{font-size:13px}.lx-search-priority-empty{font-size:12px;color:var(--el-text-color-secondary)}.lx-search-priority-row{display:flex;align-items:center;justify-content:space-between;padding:5px 9px;border:1px solid var(--app-border);border-radius:6px;background:var(--el-bg-color)}.lx-search-priority-row strong{font-size:12px}.lx-search-priority-actions{display:flex;gap:6px}
.lx-proxy-fields{display:grid;grid-template-columns:minmax(0,1fr) 180px;gap:16px}.lx-proxy-fields label{display:flex;flex-direction:column;gap:8px;color:var(--el-text-color-regular);font-size:13px}.lx-source-actions{display:flex;align-items:center;gap:8px;flex-shrink:0}.lx-source-test-results{display:flex;flex-direction:column;gap:6px;width:100%;padding:8px 0 4px;border-top:1px dashed var(--app-border)}.lx-source-test-result{display:flex;align-items:center;gap:9px;flex-wrap:wrap;font-size:12px}.lx-source-test-result>span{color:var(--el-text-color-secondary)}.lx-source-test-result small{color:var(--el-text-color-secondary)}.search-summary{margin-top:12px;color:var(--el-text-color-secondary);font-size:12px}.search-pagination{display:flex;justify-content:center;margin-top:18px}.playlist-add-track{display:flex;flex-direction:column;gap:5px;margin-bottom:14px;padding:10px 12px;border-radius:8px;background:var(--app-bg-control)}.playlist-add-track span{color:var(--el-text-color-secondary);font-size:12px}.queue-current{padding:10px 12px;margin-bottom:12px;border-radius:8px;background:var(--app-bg-control);font-size:13px}.queue-list{display:flex;flex-direction:column;gap:8px}.queue-item{display:flex;align-items:center;gap:12px;padding:11px 12px;border:1px solid var(--app-border);border-radius:9px;background:var(--el-bg-color);transition:border-color .15s,opacity .15s}.queue-item:hover{border-color:var(--app-accent)}.queue-item-dragging{opacity:.45}.queue-drag-handle{display:flex;align-items:center;color:var(--el-text-color-secondary);cursor:grab;touch-action:none}.queue-drag-handle:active{cursor:grabbing}.queue-index{width:24px;color:var(--el-text-color-secondary);font-size:12px;text-align:center}.queue-track{display:flex;flex:1;flex-direction:column;gap:4px;min-width:0}.queue-track strong,.queue-track span{overflow:hidden;text-overflow:ellipsis;white-space:nowrap}.queue-track span,.queue-requester{color:var(--el-text-color-secondary);font-size:12px}.queue-requester{max-width:120px;overflow:hidden;text-overflow:ellipsis;white-space:nowrap}.setting-row p{margin:6px 0 0}@media(max-width:800px){.music-page{min-height:calc(100vh - 126px)}.mini-player{margin-left:-22px;margin-right:-22px;margin-bottom:-22px;padding-left:22px;padding-right:22px}.source-row,.setting-row{align-items:flex-start;flex-direction:column}.search-row,.lx-import-row{flex-direction:column;align-items:stretch}.lx-source-row{align-items:flex-start}.mini-player{align-items:flex-start;flex-direction:column}.mini-player-actions{width:100%;justify-content:space-between}}
.replace-track-original,.replace-track-comparison{display:flex;flex-direction:column;gap:6px;padding:12px 14px;margin-bottom:14px;border:1px solid var(--app-border);border-radius:9px;background:var(--el-fill-color-lighter)}.replace-track-original small,.replace-track-comparison small{color:var(--el-text-color-secondary);font-size:12px}.replace-candidate-list{display:flex;flex-direction:column;align-items:stretch;width:100%;gap:4px}.replace-candidate{display:flex;align-items:flex-start;width:100%;height:auto;margin:0;padding:9px 10px;border-bottom:1px solid var(--app-border)}.replace-candidate-main{display:flex;flex-direction:column;gap:4px;min-width:0}.replace-candidate-main small{color:var(--el-text-color-secondary);font-size:12px}</style>