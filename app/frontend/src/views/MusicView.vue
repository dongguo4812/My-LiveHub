<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { Headset, VideoPause, VideoPlay } from '@element-plus/icons-vue'
import * as MusicService from '../../bindings/github.com/dongguo4812/My-LiveHub/app/internal/desktop/musicservice.js'
import { getActiveSyncedLyric, parseSyncedLyrics } from '../utils/syncedLyrics'

type MusicConfig = Awaited<ReturnType<typeof MusicService.GetConfig>>
type Track = Awaited<ReturnType<typeof MusicService.Search>>['tracks'][number]
type QueueSnapshot = Awaited<ReturnType<typeof MusicService.GetQueue>>
type PlaybackSnapshot = Awaited<ReturnType<typeof MusicService.GetPlaybackStatus>>

type MusicTab = 'now-playing' | 'search' | 'visitor-requests' | 'settings'
const activeTab = ref<MusicTab>('now-playing')

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
async function testLXAPIConnection() {
  if (!config.value) return
  saving.value = true
  error.value = ''
  success.value = ''
  try {
    config.value.lx_api_endpoints = config.value.lx_api_endpoints.map(item => item.trim()).filter(Boolean)
    await MusicService.SaveConfig(config.value)
    await MusicService.GetPlaybackStatus()
    success.value = '已成功连接洛雪客户端 API'
  } catch (reason) {
    error.value = reason instanceof Error ? reason.message : String(reason)
  } finally {
    saving.value = false
  }
}
const queue = ref<QueueSnapshot | null>(null)
const draggedQueueItemID = ref('')
const playback = ref<PlaybackSnapshot | null>(null)
const lyricPosition = ref(0)
let lyricPositionAnchor = 0
let lyricPositionAnchorAt = 0
let lyricClockTimer: ReturnType<typeof setInterval> | undefined
const syncedLyrics = ref<Array<{ time: number; text: string; translation?: string }>>([])
const lyricsTrackKey = ref('')
const lyricsStatus = ref<'idle' | 'loading' | 'ready' | 'empty' | 'error'>('idle')
let lyricsRequestID = 0
const results = ref<Track[]>([])
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

const progress = computed(() => {
  if (!playback.value || playback.value.duration_ms <= 0) return 0
  return Math.min(100, Math.max(0, playback.value.position_ms / playback.value.duration_ms * 100))
})
const searchErrorText = computed(() => Object.values(searchErrors.value).join('；'))
function formatDuration(value?: number) {
  if (value == null || !Number.isFinite(Number(value)) || Number(value) < 0) return '--:--'
  // Playback snapshots and seek previews are both expressed in milliseconds.
  const numeric = Number(value)
  const seconds = Math.floor(numeric / 1000)
  return `${Math.floor(seconds / 60)}:${String(seconds % 60).padStart(2, '0')}`
}
function formatSeekTooltip(percent: number) {
  const duration = playback.value?.duration_ms ?? 0
  return formatDuration(Math.round(duration * Math.min(100, Math.max(0, percent)) / 100))
}
const activeLyric = computed(() => {
  if (lyricsStatus.value === 'loading' || lyricsStatus.value === 'idle') return { text: '歌词加载中…', translation: '' }
  if (lyricsStatus.value === 'error') return { text: '歌词获取失败，稍后重试', translation: '' }
  if (lyricsStatus.value === 'empty' || lyricsStatus.value === 'ready' && syncedLyrics.value.length === 0) return { text: '暂无歌词', translation: '' }
  return getActiveSyncedLyric(syncedLyrics.value, lyricPosition.value) ?? { text: '歌词加载中…', translation: '' }
})
const currentLyric = computed(() => activeLyric.value.text)
const currentLyricTranslation = computed(() => activeLyric.value.translation ?? '')
async function loadTrackLyrics() {
  const track = playback.value?.track
  const key = track ? `${track.source_id}:${track.id}` : ''
  if (key === lyricsTrackKey.value) return
  lyricsTrackKey.value = key
  syncedLyrics.value = []
  const requestID = ++lyricsRequestID
  if (!track) {
    lyricsStatus.value = 'idle'
    return
  }
  lyricsStatus.value = 'loading'
  try {
    const lrc = await MusicService.GetLyrics(track)
    if (requestID !== lyricsRequestID || lyricsTrackKey.value !== key) return
    const parsed = parseSyncedLyrics(lrc ?? '')
    syncedLyrics.value = parsed
    lyricsStatus.value = parsed.length > 0 ? 'ready' : 'empty'
  } catch {
    if (requestID === lyricsRequestID && lyricsTrackKey.value === key) {
      // A network/API failure is not proof that the track has no lyrics.
      lyricsStatus.value = 'error'
    }
  }
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
  try {
    playback.value = await MusicService.GetPlaybackStatus()
    lyricPositionAnchor = playback.value.position_ms ?? 0
    lyricPositionAnchorAt = Date.now()
    lyricPosition.value = lyricPositionAnchor
    void loadTrackLyrics()
  } catch { /* The engine is not started until the first play request. */ }
  await refreshQueue()
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
async function resume() { try { await MusicService.Resume(); await refreshPlayback() } catch (reason) { error.value = String(reason) } }
onMounted(() => {
  void loadConfig()
  void refreshPlayback()
  statusTimer = setInterval(() => void refreshPlayback(), 1500)
  lyricClockTimer = setInterval(() => {
    if (!playback.value) return
    lyricPosition.value = lyricPositionAnchor + (playback.value.state === 'playing' ? Date.now() - lyricPositionAnchorAt : 0)
  }, 30)
})
onUnmounted(() => {
  if (statusTimer) clearInterval(statusTimer)
  if (lyricClockTimer) clearInterval(lyricClockTimer)
})
</script>

<template>
  <section class="music-page">
    <header class="page-heading">
      <div>
        <p class="eyebrow">MUSIC PLAYER</p>
        <h2>音乐中心</h2>
        <p class="description">管理洛雪音乐播放、歌曲搜索与观众点播队列。</p>
        <el-alert v-if="config && !config.enabled" title="音乐模块当前已关闭：无法加入歌曲队列或播放。请在下方「点歌设置」中启用音乐模块并保存全部设置。" type="warning" :closable="false" show-icon style="margin-top:12px" />
      </div>
      <el-tag type="success" effect="plain">直播音乐控制台</el-tag>
    </header>

    <el-alert v-if="error" :title="error" type="error" :closable="true" show-icon @close="error = ''" />
    <el-alert v-if="playback?.error" :title="playback.error" type="error" :closable="false" show-icon />
    <el-alert v-if="success" :title="success" type="success" :closable="true" show-icon @close="success = ''" />

    <nav class="music-tabs" aria-label="音乐页面导航">
      <button :class="{ active: activeTab === 'now-playing' }" @click="activeTab = 'now-playing'">正在播放</button>
      <button :class="{ active: activeTab === 'search' }" @click="activeTab = 'search'">歌曲搜索</button>
      <button :class="{ active: activeTab === 'visitor-requests' }" @click="activeTab = 'visitor-requests'">观众点播 <span>{{ queue?.items.length ?? 0 }}</span></button>
      <button :class="{ active: activeTab === 'settings' }" @click="activeTab = 'settings'">音乐设置</button>
    </nav>

    <el-card v-if="activeTab === 'now-playing'" shadow="never">
      <template #header><div class="card-heading"><strong>正在播放</strong><el-tag :type="playback?.state === 'playing' ? 'success' : playback?.state === 'error' ? 'danger' : 'info'">{{ playback?.state === 'error' ? '播放失败' : playback?.state ?? '播放器未启动' }}</el-tag></div></template>
      <div class="now-playing">
        <div class="track-cover">
          <img v-if="playback?.track?.cover_url" :src="playback.track.cover_url" alt="歌曲封面" />
          <el-icon v-else><Headset /></el-icon>
        </div>
        <div class="track-info">
          <strong>{{ playback?.track ? `${playback.track.title} - ${playback.track.artist || '未知歌手'}` : '尚未播放歌曲' }}</strong>
          <span class="current-lyric">{{ playback?.track ? currentLyric : '搜索歌曲并加入播放队列' }}</span>
          <span v-if="playback?.track && currentLyricTranslation" class="current-lyric-translation">{{ currentLyricTranslation }}</span>
          <small v-if="playback?.track">{{ playbackSourceName(playback.track) }}</small>
          <el-tag v-else-if="queue?.current && queue.current.requested_by !== '__my_livehub_search_play__'" type="warning" size="small">观众点播</el-tag>
        </div>
      </div>
      <el-slider :model-value="progress" :min="0" :max="100" :step="0.1" :format-tooltip="formatSeekTooltip" :disabled="true" />
      <div class="time-row"><span>{{ formatDuration(playback?.position_ms) }}</span><span>{{ formatDuration(playback?.duration_ms) }}</span></div>
      <p class="hint">音量调节和播放进度定位暂不受洛雪开放 API 支持，请在洛雪客户端中操作。</p>
      <div class="player-controls">
        <el-button :disabled="playback?.state !== 'playing'" @click="pause">暂停</el-button>
        <el-button :disabled="playback?.state !== 'paused'" @click="resume">继续</el-button>
        <el-button @click="previousTrack">上一首</el-button>
        <el-button @click="nextTrack">下一首</el-button>
      </div>
      <p class="hint">播放由本机洛雪客户端负责。请先启动洛雪，并在配置页填写其本地 API 地址。</p>
    </el-card>

    <el-card v-if="activeTab === 'search'" shadow="never">
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
        <el-table-column label="操作" width="170" fixed="right"><template #default="scope"><el-button link type="success" @click="playSearchTrackNow(scope.row)">立即播放</el-button><el-button link type="primary" @click="addToQueue(scope.row)">加入队列</el-button></template></el-table-column>
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

    <el-card v-if="activeTab === 'visitor-requests'" shadow="never">
      <template #header>
        <div class="card-heading">
          <div class="card-heading"><strong>观众点播队列</strong><el-tag effect="plain">{{ queue?.items.length ?? 0 }} 首待播</el-tag></div>
          <el-button type="danger" plain size="small" :disabled="!queue?.items.length" @click="clearQueue">清空待播</el-button>
        </div>
      </template>
      <div v-if="queue?.current" class="queue-current">当前播放：{{ queue.current.track.title }} - {{ queue.current.track.artist || '未知歌手' }}（{{ queue.current.requested_by || '观众点播' }}）</div>
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

    <el-card v-if="activeTab === 'settings'" shadow="never">
      <template #header><strong>洛雪客户端连接与搜索音源</strong></template>
      <p class="section-description">洛雪客户端需运行在本机。地址按从上到下的顺序尝试，前一个不可用时自动切换到下一个；播放通过 lxmusic:// 协议发起。</p>
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
        <el-button type="primary" :loading="saving" :disabled="!config" @click="testLXAPIConnection">保存并测试连接</el-button>
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

    <el-card v-if="activeTab === 'settings'" shadow="never">
      <template #header><strong>点歌设置</strong></template>
      <div v-if="config" class="setting-list">
        <div class="setting-row"><div><strong>启用音乐模块</strong><p>关闭后无法加入歌曲队列或播放歌曲，也不会处理弹幕点歌；MV 视频播放不受影响。</p></div><el-switch v-model="config.enabled" /></div>
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
 
      </div>
      <div class="card-actions"><el-button type="primary" :loading="saving" :disabled="!config" @click="saveConfig">保存全部设置</el-button></div>
    </el-card>
    <div v-if="activeTab !== 'now-playing'" class="mini-player">
      <div class="mini-player-track">
        <img v-if="playback?.track?.cover_url" :src="playback.track.cover_url" alt="当前歌曲封面" />
        <el-icon v-else><Headset /></el-icon>
        <div>
          <strong>{{ playback?.track ? `${playback.track.title} - ${playback.track.artist || '未知歌手'}` : '尚未播放歌曲' }}</strong>
          <span class="current-lyric">{{ playback?.track ? currentLyric : '从歌曲搜索开始播放' }}</span>
          <span v-if="playback?.track && currentLyricTranslation" class="current-lyric-translation">{{ currentLyricTranslation }}</span>
        </div>
      </div>
      <div class="mini-player-actions">
        <span>{{ playback?.state === 'playing' ? '正在播放' : playback?.state === 'paused' ? '已暂停' : playback?.state === 'error' ? '播放失败' : '待机中' }}</span>
        <el-button circle text :disabled="playback?.state !== 'playing'" aria-label="暂停" @click="pause"><el-icon><VideoPause /></el-icon></el-button>
        <el-button circle text :disabled="playback?.state !== 'paused'" aria-label="继续播放" @click="resume"><el-icon><VideoPlay /></el-icon></el-button>
        <el-button circle text :disabled="!playback?.track" @click="nextTrack">下一首</el-button>
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
.current-lyric-translation{display:block;overflow:hidden;text-overflow:ellipsis;white-space:nowrap;color:var(--el-text-color-secondary);font-size:11px;line-height:1.35}.current-lyric{display:block;overflow:hidden;text-overflow:ellipsis;white-space:nowrap;color:var(--el-text-color-secondary);font-size:12px;line-height:1.5}.mini-player-track>div{min-width:0}.mini-player-track .current-lyric{max-width:min(52vw,560px)}.track-info .current-lyric{max-width:100%}.mini-player{position:sticky;bottom:-28px;z-index:30;display:flex;align-items:center;justify-content:space-between;gap:16px;flex-shrink:0;margin-top:auto;margin-left:-32px;margin-right:-32px;margin-bottom:-28px;padding:12px 32px calc(12px + env(safe-area-inset-bottom));min-height:68px;border:1px solid var(--app-border);border-bottom:0;border-radius:12px 12px 0 0;background:var(--el-bg-color);box-shadow:0 -8px 28px #00000018}
.mini-player-track,.mini-player-actions{display:flex;align-items:center;gap:12px;min-width:0}
.mini-player-track img{width:42px;height:42px;object-fit:cover;border-radius:7px}.mini-player-track>.el-icon{font-size:28px;color:var(--el-text-color-secondary)}
.mini-player-track>div{display:flex;flex-direction:column;gap:3px;min-width:0}
.mini-player-track strong,.mini-player-track span{overflow:hidden;text-overflow:ellipsis;white-space:nowrap;max-width:280px}
.mini-player-track span,.mini-player-actions>span{font-size:12px;color:var(--el-text-color-secondary)}
.music-page{display:flex;flex-direction:column;gap:20px;min-height:calc(100vh - 146px)}.page-heading{display:flex;align-items:flex-start;justify-content:space-between;gap:20px}.page-heading h2{margin:0;font-size:25px}.eyebrow{margin:0 0 9px;color:var(--app-accent);font-size:11px;letter-spacing:.15em}.description,.section-description,.source-copy span,.setting-row p,.hint{color:var(--el-text-color-secondary);font-size:13px}.section-description{margin:0 0 16px}.card-heading{display:flex;align-items:center;justify-content:space-between}.source-list,.setting-list{display:flex;flex-direction:column}.source-row,.setting-row{display:flex;align-items:center;justify-content:space-between;gap:24px;padding:14px 0;border-bottom:1px solid var(--app-border)}.source-row:last-child{border-bottom:0}.source-copy,.track-info{display:flex;flex-direction:column;gap:5px}.source-copy span,.track-info span,.track-info small{font-size:12px;color:var(--el-text-color-secondary)}.setting-row p{margin:6px 0 0}.card-actions{display:flex;justify-content:flex-end;gap:10px;margin-top:20px}.now-playing-list{margin-top:22px;padding-top:18px;border-top:1px solid var(--app-border)}.now-playing-list-header{display:flex;align-items:center;justify-content:space-between;gap:12px;margin-bottom:12px}.now-playing-list-header>div{display:flex;flex-direction:column;gap:4px}.now-playing-list-header strong{font-size:15px}.now-playing-list-header span{font-size:12px;color:var(--el-text-color-secondary)}.now-playing-track-list{display:flex;flex-direction:column;gap:6px}.now-playing-track{display:flex;align-items:center;gap:12px;padding:10px 12px;border:1px solid transparent;border-radius:8px;background:var(--el-fill-color-lighter)}.now-playing-track-active{border-color:var(--app-accent);background:var(--app-bg-control)}.now-playing-track-index{width:28px;color:var(--el-text-color-secondary);font-size:12px;font-variant-numeric:tabular-nums;text-align:center}.now-playing-track-info{display:flex;flex:1;flex-direction:column;gap:3px;min-width:0}.now-playing-track-info strong,.now-playing-track-info span{overflow:hidden;text-overflow:ellipsis;white-space:nowrap}.now-playing-track-info strong{font-size:13px}.now-playing-track-info span{font-size:12px;color:var(--el-text-color-secondary)}.now-playing{display:flex;align-items:center;gap:16px}.track-cover{width:76px;height:76px;border-radius:10px;background:var(--app-bg-control);display:flex;align-items:center;justify-content:center;overflow:hidden;color:var(--el-text-color-secondary);font-size:30px}.track-cover img{width:100%;height:100%;object-fit:cover}.track-info strong{font-size:17px}.time-row{display:flex;justify-content:space-between;color:var(--el-text-color-secondary);font-size:12px;font-variant-numeric:tabular-nums}.player-controls{display:flex;flex-wrap:wrap;justify-content:center;gap:8px;margin:14px 0}.playback-order-row{display:flex;align-items:center;justify-content:center;gap:12px;margin:8px 0 14px}.playback-order-row span{font-size:13px;color:var(--el-text-color-secondary)}.volume-row{display:grid;grid-template-columns:50px 1fr;align-items:center;gap:12px}.volume-row span{font-size:13px;color:var(--el-text-color-secondary)}.hint{margin:12px 0 0}.search-row{display:flex;gap:10px}.lx-import-row{display:flex;gap:10px;align-items:center}.hidden-file-input{display:none}.lx-source-list{display:flex;flex-direction:column;margin-top:16px}.lx-source-row{display:flex;align-items:center;justify-content:space-between;gap:16px;padding:12px 0;border-top:1px solid var(--app-border)}.lx-source-row .source-copy{min-width:0}.lx-source-row .source-copy span{overflow-wrap:anywhere}.lx-search-source-settings{display:flex;flex-direction:column;gap:6px;margin-top:8px}.lx-search-source-checkboxes{display:flex;flex-wrap:wrap;gap:8px 12px}.lx-search-source-toggle{display:inline-flex;align-items:center;gap:7px;padding:4px 9px;border:1px solid var(--app-border);border-radius:6px;background:var(--el-bg-color);color:var(--el-text-color-regular);font:inherit;font-size:13px;cursor:pointer;transition:border-color .15s,background .15s,color .15s}.lx-search-source-toggle:hover{border-color:var(--app-accent)}.lx-search-source-toggle:has(.lx-search-source-checkbox:checked){border-color:var(--app-accent);background:var(--app-bg-control);color:var(--app-accent)}.lx-search-source-checkbox{width:15px;height:15px;margin:0;flex:0 0 15px;accent-color:var(--app-accent);cursor:pointer}.lx-search-priority-list{display:flex;flex-direction:column;align-items:stretch;gap:5px;margin-top:10px;padding:9px 10px;max-width:360px;border:1px solid var(--app-border);border-radius:7px;background:var(--el-fill-color-lighter)}.lx-search-priority-list>strong{font-size:13px}.lx-search-priority-empty{font-size:12px;color:var(--el-text-color-secondary)}.lx-search-priority-row{display:flex;align-items:center;justify-content:space-between;padding:5px 9px;border:1px solid var(--app-border);border-radius:6px;background:var(--el-bg-color)}.lx-search-priority-row strong{font-size:12px}.lx-search-priority-actions{display:flex;gap:6px}
.lx-proxy-fields{display:grid;grid-template-columns:minmax(0,1fr) 180px;gap:16px}.lx-proxy-fields label{display:flex;flex-direction:column;gap:8px;color:var(--el-text-color-regular);font-size:13px}.lx-source-actions{display:flex;align-items:center;gap:8px;flex-shrink:0}.lx-source-test-results{display:flex;flex-direction:column;gap:6px;width:100%;padding:8px 0 4px;border-top:1px dashed var(--app-border)}.lx-source-test-result{display:flex;align-items:center;gap:9px;flex-wrap:wrap;font-size:12px}.lx-source-test-result>span{color:var(--el-text-color-secondary)}.lx-source-test-result small{color:var(--el-text-color-secondary)}.search-summary{margin-top:12px;color:var(--el-text-color-secondary);font-size:12px}.search-pagination{display:flex;justify-content:center;margin-top:18px}.queue-current{padding:10px 12px;margin-bottom:12px;border-radius:8px;background:var(--app-bg-control);font-size:13px}.queue-list{display:flex;flex-direction:column;gap:8px}.queue-item{display:flex;align-items:center;gap:12px;padding:11px 12px;border:1px solid var(--app-border);border-radius:9px;background:var(--el-bg-color);transition:border-color .15s,opacity .15s}.queue-item:hover{border-color:var(--app-accent)}.queue-item-dragging{opacity:.45}.queue-drag-handle{display:flex;align-items:center;color:var(--el-text-color-secondary);cursor:grab;touch-action:none}.queue-drag-handle:active{cursor:grabbing}.queue-index{width:24px;color:var(--el-text-color-secondary);font-size:12px;text-align:center}.queue-track{display:flex;flex:1;flex-direction:column;gap:4px;min-width:0}.queue-track strong,.queue-track span{overflow:hidden;text-overflow:ellipsis;white-space:nowrap}.queue-track span,.queue-requester{color:var(--el-text-color-secondary);font-size:12px}.queue-requester{max-width:120px;overflow:hidden;text-overflow:ellipsis;white-space:nowrap}.setting-row p{margin:6px 0 0}@media(max-width:800px){.music-page{min-height:calc(100vh - 126px)}.mini-player{margin-left:-22px;margin-right:-22px;padding-left:22px;padding-right:22px}.source-row,.setting-row{align-items:flex-start;flex-direction:column}.search-row,.lx-import-row{flex-direction:column;align-items:stretch}.lx-source-row{align-items:flex-start}.mini-player{align-items:flex-start;flex-direction:column}.mini-player-actions{width:100%;justify-content:space-between}}
.replace-track-original,.replace-track-comparison{display:flex;flex-direction:column;gap:6px;padding:12px 14px;margin-bottom:14px;border:1px solid var(--app-border);border-radius:9px;background:var(--el-fill-color-lighter)}.replace-track-original small,.replace-track-comparison small{color:var(--el-text-color-secondary);font-size:12px}.replace-candidate-list{display:flex;flex-direction:column;align-items:stretch;width:100%;gap:4px}.replace-candidate{display:flex;align-items:flex-start;width:100%;height:auto;margin:0;padding:9px 10px;border-bottom:1px solid var(--app-border)}.replace-candidate-main{display:flex;flex-direction:column;gap:4px;min-width:0}.replace-candidate-main small{color:var(--el-text-color-secondary);font-size:12px}</style>