<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { Headset, VideoPause, VideoPlay } from '@element-plus/icons-vue'
import * as MusicService from '../../bindings/github.com/dongguo4812/My-LiveHub/app/internal/desktop/musicservice.js'

type MusicConfig = Awaited<ReturnType<typeof MusicService.GetConfig>>
type Track = Awaited<ReturnType<typeof MusicService.Search>>['tracks'][number]
type QueueSnapshot = Awaited<ReturnType<typeof MusicService.GetQueue>>
type PlaybackSnapshot = Awaited<ReturnType<typeof MusicService.GetPlaybackStatus>>

const config = ref<MusicConfig | null>(null)
type MusicTab = 'now-playing' | 'host-playlist' | 'visitor-requests' | 'search' | 'settings'
const activeTab = ref<MusicTab>('now-playing')
const favorites = ref<Track[]>([])
const hostPlayMode = ref('list')
type LXSource = Awaited<ReturnType<typeof MusicService.GetLXUserSources>>[number]
type LXValidation = Awaited<ReturnType<typeof MusicService.ValidateLXUserSource>>
type LXSearchSourceOption = Awaited<ReturnType<typeof MusicService.GetLXSearchSourceOptions>>[number]
const lxSources = ref<LXSource[]>([])
const lxValidation = ref<Record<string, LXValidation>>({})
const lxSearchSourceOptions = ref<Record<string, LXSearchSourceOption[]>>({})
const activeLXSourceID = ref('')
const validatingLXSourceID = ref('')
const lxSourceURL = ref('')
const importingLXSource = ref(false)
const sourceFileInput = ref<HTMLInputElement | null>(null)
const queue = ref<QueueSnapshot | null>(null)
const draggedQueueItemID = ref('')
const playback = ref<PlaybackSnapshot | null>(null)
const results = ref<Track[]>([])
const searchErrors = ref<Record<string, string>>({})
const qualitySelections = ref<Record<string, string>>({})
const query = ref('')
const selectedSource = ref('all')
const searchableSources = computed(() => (lxSearchSourceOptions.value[activeLXSourceID.value] ?? []).filter(item => item.enabled).map(item => ({ id: item.id, name: item.name })))
const volume = ref(80)
const loading = ref(false)
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
  if (!value || value < 0) return '--:--'
  const seconds = Math.floor(value / 1000)
  return `${Math.floor(seconds / 60).toString().padStart(2, '0')}:${(seconds % 60).toString().padStart(2, '0')}`
}
function sourceName(id?: string) {
  const names: Record<string, string> = { kw: '酷我音乐', kg: '酷狗音乐', tx: 'QQ 音乐', wy: '网易云音乐', mg: '咪咕音乐' }
  return id ? (names[id] ?? id) : '洛雪音源'
}
function qualityNames(track: Track) {
  return (track.qualities ?? []).map(item => item.type).join(' / ') || '未知'
}
function trackRowKey(row: Track) { return `${row.source_id}:${row.id}` }
function isFavorite(track: Track) { return favorites.value.some(item => trackRowKey(item) === trackRowKey(track)) }
async function loadFavorites() {
  try {
    let playlist = await MusicService.GetHostPlaylist()
    if (!(playlist.tracks ?? []).length) {
      try {
        const legacy = JSON.parse(localStorage.getItem('my-livehub.music.host-favorites') ?? '[]') as Track[]
        if (Array.isArray(legacy)) {
          for (const track of legacy) {
            if (track && typeof track.id === 'string' && typeof track.source_id === 'string' && typeof track.title === 'string') {
              await MusicService.AddHostTrack(track)
            }
          }
          playlist = await MusicService.GetHostPlaylist()
        }
      } catch { /* Ignore malformed legacy browser data. */ }
    }
    favorites.value = playlist.tracks ?? []
    hostPlayMode.value = playlist.mode ?? 'list'
  } catch (reason) {
    error.value = reason instanceof Error ? reason.message : String(reason)
    favorites.value = []
  }
}
async function toggleFavorite(track: Track) {
  error.value = ''
  try {
    if (isFavorite(track)) await MusicService.RemoveHostTrack(track.source_id, track.id)
    else await MusicService.AddHostTrack(track)
    await loadFavorites()
    success.value = isFavorite(track) ? '已加入主播歌单' : '已从主播歌单移除'
  } catch (reason) {
    error.value = reason instanceof Error ? reason.message : String(reason)
  }
}
async function removeFavorite(track: Track) {
  try {
    await MusicService.RemoveHostTrack(track.source_id, track.id)
    await loadFavorites()
  } catch (reason) { error.value = reason instanceof Error ? reason.message : String(reason) }
}
async function playFavorite(track: Track) {
  const index = favorites.value.findIndex(item => trackRowKey(item) === trackRowKey(track))
  if (index < 0) return
  try { await MusicService.PlayHostTrack(index); await refreshPlayback(); success.value = '开始播放主播歌单' }
  catch (reason) { error.value = reason instanceof Error ? reason.message : String(reason) }
}
async function addHostPlaylistToQueue() {
  try { await MusicService.PlayHostPlaylist(); await refreshPlayback(); success.value = '已开始播放主播歌单' }
  catch (reason) { error.value = reason instanceof Error ? reason.message : String(reason) }
}
async function moveFavorite(index: number, offset: number) {
  const target = index + offset
  if (target < 0 || target >= favorites.value.length) return
  try { await MusicService.MoveHostTrack(index, target); await loadFavorites() }
  catch (reason) { error.value = reason instanceof Error ? reason.message : String(reason) }
}
async function changeHostPlayMode(mode: string) {
  try { await MusicService.SetHostPlayMode(mode); hostPlayMode.value = mode }
  catch (reason) { error.value = reason instanceof Error ? reason.message : String(reason) }
}
async function loadConfig() {
  loading.value = true
  error.value = ''
  try {
    config.value = await MusicService.GetConfig()
      await refreshQueue()
    await refreshLXSources()
  } catch (reason) {
    error.value = reason instanceof Error ? reason.message : String(reason)
  } finally {
    loading.value = false
  }
}
async function refreshLXSources() {
  try {
    lxSources.value = await MusicService.GetLXUserSources()
    const optionEntries = await Promise.all(lxSources.value.map(async source => [source.id, await MusicService.GetLXSearchSourceOptions(source.id)] as const))
    lxSearchSourceOptions.value = Object.fromEntries(optionEntries)
    // Restore each source's persisted validation result into the view state.
    // The backend returns this only for the exact script version that passed.
    lxValidation.value = Object.fromEntries(
      lxSources.value.flatMap(source => {
        const persisted = (source as LXSource & { validation?: LXValidation | null }).validation
        return persisted?.initialized ? [[source.id, persisted]] : []
      }),
    )
    activeLXSourceID.value = await MusicService.GetActiveLXUserSourceID()
    if (selectedSource.value !== 'all' && !searchableSources.value.some(source => source.id === selectedSource.value)) {
      selectedSource.value = 'all'
    }
  } catch (reason) {
    error.value = reason instanceof Error ? reason.message : String(reason)
  }
}
function chooseLXSourceFile() { sourceFileInput.value?.click() }
async function importLXSourceFile(event: Event) {
  const input = event.target as HTMLInputElement
  const file = input.files?.[0]
  if (!file) return
  importingLXSource.value = true
  error.value = ''
  success.value = ''
  try {
    if (lxSources.value.length > 20) throw new Error('最多只能导入 21 个自定义音源')
    const script = await file.text()
    if (script.length > 9_000_000) throw new Error('音源脚本不能超过 9 MB')
    const metadata = await MusicService.ImportLXSource(script)
    success.value = `已导入音源：${metadata.name}；可运行基础初始化验证`
    await refreshLXSources()
  } catch (reason) {
    error.value = reason instanceof Error ? reason.message : String(reason)
  } finally {
    importingLXSource.value = false
    input.value = ''
  }
}
async function importLXSourceURL() {
  if (!lxSourceURL.value.trim()) { error.value = '请输入音源脚本 URL'; return }
  importingLXSource.value = true
  error.value = ''
  success.value = ''
  try {
    const metadata = await MusicService.ImportLXSourceURL(lxSourceURL.value.trim())
    success.value = `已下载并导入音源：${metadata.name}；可运行基础初始化验证`
    lxSourceURL.value = ''
    await refreshLXSources()
  } catch (reason) {
    error.value = reason instanceof Error ? reason.message : String(reason)
  } finally {
    importingLXSource.value = false
  }
}
async function validateLXSource(source: LXSource) {
  validatingLXSourceID.value = source.id
  error.value = ''
  success.value = ''
  try {
    const result = await MusicService.ValidateLXUserSource(source.id)
    lxValidation.value = { ...lxValidation.value, [source.id]: result }
    const options = await MusicService.GetLXSearchSourceOptions(source.id)
    lxSearchSourceOptions.value = { ...lxSearchSourceOptions.value, [source.id]: options }
    success.value = `音源初始化通过：${source.name}；已声明来源：${result.platforms.join('、')}。实际搜索与播放解析待洛雪 SDK 接管。`
  } catch (reason) {
    delete lxValidation.value[source.id]
    error.value = reason instanceof Error ? reason.message : String(reason)
  } finally {
    validatingLXSourceID.value = ''
  }
}
async function updateLXSource(source: LXSource) {
  importingLXSource.value = true
  error.value = ''
  success.value = ''
  try {
    const metadata = await MusicService.UpdateLXUserSource(source.id)
    success.value = `已更新音源：${metadata.name}；请重新运行基础初始化验证`
    delete lxValidation.value[source.id]
    await refreshLXSources()
  } catch (reason) {
    error.value = reason instanceof Error ? reason.message : String(reason)
  } finally {
    importingLXSource.value = false
  }
}

async function deleteLXSource(source: LXSource) {
  if (!window.confirm(`确定删除音源“${source.name}”吗？`)) return
  try {
    await MusicService.DeleteLXUserSource(source.id)
    success.value = `已删除音源：${source.name}`
    delete lxValidation.value[source.id]
    await refreshLXSources()
  } catch (reason) {
    error.value = reason instanceof Error ? reason.message : String(reason)
  }
}

async function setActiveLXSource(source: LXSource) {
  error.value = ''
  try {
    await MusicService.SetActiveLXUserSource(source.id)
    activeLXSourceID.value = source.id
    if (selectedSource.value !== 'all' && !searchableSources.value.some(item => item.id === selectedSource.value)) selectedSource.value = 'all'
    success.value = `已启用音源：${source.name}`
  } catch (reason) {
    error.value = reason instanceof Error ? reason.message : String(reason)
  }
}
async function deactivateLXSource() {
  error.value = ''
  try {
    await MusicService.SetActiveLXUserSource('')
    activeLXSourceID.value = ''
    selectedSource.value = 'all'
    results.value = []
    success.value = '已停用自定义音源'
  } catch (reason) {
    error.value = reason instanceof Error ? reason.message : String(reason)
  }
}
async function setLXSearchSources(source: LXSource, enabled: (string | number | boolean)[]) {
  try {
    await MusicService.SetLXSearchSources(source.id, enabled.map(String))
    lxSearchSourceOptions.value = { ...lxSearchSourceOptions.value, [source.id]: await MusicService.GetLXSearchSourceOptions(source.id) }
    if (activeLXSourceID.value === source.id && selectedSource.value !== 'all' && !searchableSources.value.some(item => item.id === selectedSource.value)) selectedSource.value = 'all'
    results.value = []
    success.value = '已保存音源的搜索来源选择'
  } catch (reason) {
    error.value = reason instanceof Error ? reason.message : String(reason)
    await refreshLXSources()
  }
}
async function setLXSourceUpdateAlert(source: LXSource, enabled: boolean | string | number) {
  try {
    await MusicService.SetLXUserSourceAllowUpdateAlert(source.id, Boolean(enabled))
    source.allow_show_update_alert = Boolean(enabled)
  } catch (reason) {
    error.value = reason instanceof Error ? reason.message : String(reason)
    await refreshLXSources()
  }
}
let pollingLXUpdates = false
async function pollLXSourceUpdates() {
  if (pollingLXUpdates) return
  pollingLXUpdates = true
  try {
    const updates = await MusicService.GetLXUserSourceUpdates()
    for (const update of updates) {
      const message = `${update.name || '洛雪音源'} 发布了更新提醒：\\n\\n${update.log}`
      if (update.update_url && window.confirm(`${message}\\n\\n是否打开更新地址？\\n${update.update_url}`)) {
        window.open(update.update_url, '_blank', 'noopener')
      } else if (!update.update_url) {
        window.alert(message)
      }
    }
  } catch {
    // Update notifications are best-effort and must not interrupt playback polling.
  } finally {
    pollingLXUpdates = false
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
    MusicService.ClearQueue()
    await refreshQueue()
    success.value = '观众点播待播队列已清空'
  } catch (reason) {
    error.value = reason instanceof Error ? reason.message : String(reason)
  }
}
async function refreshPlayback() {
  try {
    playback.value = await MusicService.GetPlaybackStatus()
    if (playback.value.volume >= 0) volume.value = playback.value.volume
  } catch { /* The engine is not started until the first play request. */ }
  await refreshQueue()
  await pollLXSourceUpdates()
}
async function saveConfig() {
  if (!config.value) return
  saving.value = true
  error.value = ''
  success.value = ''
  try {
    await MusicService.SaveConfig(config.value)
    success.value = '音乐设置已保存'
    config.value = await MusicService.GetConfig()
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
    await MusicService.Enqueue(queuedTrack, '')
    success.value = `已加入队列：${track.title}${selectedQuality ? `（${selectedQuality}）` : '（自动选择最佳音质）'}`
    await refreshQueue()
  } catch (reason) {
    error.value = reason instanceof Error ? reason.message : String(reason)
  }
}
async function playNext() {
  error.value = ''
  try {
    await MusicService.PlayNext()
    await refreshPlayback()
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
async function stop() { try { await MusicService.Stop(); await refreshPlayback() } catch (reason) { error.value = String(reason) } }
async function setVolume(value: number | number[]) {
  const next = Array.isArray(value) ? value[0] : value
  try { await MusicService.SetVolume(next); volume.value = next } catch (reason) { error.value = String(reason) }
}
async function seekTo(value: number | number[]) {
  if (!playback.value || playback.value.duration_ms <= 0) return
  const percent = Array.isArray(value) ? value[0] : value
  try { await MusicService.Seek(Math.round(playback.value.duration_ms * percent / 100)); await refreshPlayback() } catch (reason) { error.value = String(reason) }
}

onMounted(() => {
  void loadFavorites()
  void loadConfig()
  statusTimer = setInterval(() => void refreshPlayback(), 1500)
})
onUnmounted(() => { if (statusTimer) clearInterval(statusTimer) })
</script>

<template>
  <section class="music-page">
    <header class="page-heading">
      <div>
        <p class="eyebrow">MUSIC PLAYER</p>
        <h2>音乐中心</h2>
        <p class="description">管理主播常驻歌单、观众点播与直播音乐播放。</p>
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
      <button :class="{ active: activeTab === 'host-playlist' }" @click="activeTab = 'host-playlist'">主播歌单 <span>{{ favorites.length }}</span></button>
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
          <strong>{{ playback?.track?.title ?? '尚未播放歌曲' }}</strong>
          <span>{{ playback?.track?.artist ?? '搜索歌曲并加入播放队列' }}</span>
          <small v-if="playback?.track">{{ sourceName(playback.track.source_id) }}</small>
          <el-tag v-if="queue?.current?.requested_by === '__my_livehub_host_playlist__'" type="success" size="small">主播歌单</el-tag>
          <el-tag v-else-if="queue?.current" type="warning" size="small">观众点播</el-tag>
        </div>
      </div>
      <el-slider :model-value="progress" :disabled="!playback?.track || !playback.duration_ms" @change="seekTo" />
      <div class="time-row"><span>{{ formatDuration(playback?.position_ms) }}</span><span>{{ formatDuration(playback?.duration_ms) }}</span></div>
      <div class="player-controls">
        <el-button type="primary" @click="addHostPlaylistToQueue">播放</el-button>
        <el-button :disabled="playback?.state !== 'playing'" @click="pause">暂停</el-button>
        <el-button :disabled="playback?.state !== 'paused'" @click="resume">继续</el-button>
        <el-button @click="previousTrack">上一首</el-button>
        <el-button @click="nextTrack">下一首</el-button>
        <el-button @click="stop">停止</el-button>
      </div>
      <div class="playback-order-row">
        <span>播放顺序</span>
        <el-select v-model="hostPlayMode" style="width:150px" @change="changeHostPlayMode">
          <el-option label="列表循环" value="list" />
          <el-option label="单曲循环" value="single" />
          <el-option label="随机播放" value="shuffle" />
        </el-select>
      </div>
      <div class="volume-row"><span>音量</span><el-slider v-model="volume" :min="0" :max="100" @change="setVolume" /></div>
      <p class="hint">首次播放前需要在 Windows 中安装 mpv，并确保 mpv.exe 已加入 PATH。</p>
    </el-card>

    <el-card v-if="activeTab === 'settings'" shadow="never">
      <template #header><div class="card-heading"><strong>洛雪自定义音源</strong><el-tag type="success" effect="plain">动态来源注册</el-tag></div></template>
      <p class="section-description">每个 JS 脚本可以声明一个或多个洛雪来源。导入后可查看脚本实际声明的来源键、动作和音质；播放时只调用声明了对应来源键及 musicUrl 动作的脚本，不再把脚本固定绑定到四个平台。</p>
      <div class="lx-import-row">
        <el-input v-model="lxSourceURL" placeholder="粘贴 .js 音源脚本 URL" clearable @keyup.enter="importLXSourceURL" />
        <el-button type="primary" :loading="importingLXSource" @click="importLXSourceURL">从 URL 导入</el-button>
        <el-button :loading="importingLXSource" @click="chooseLXSourceFile">导入本地 JS</el-button>
        <input ref="sourceFileInput" class="hidden-file-input" type="file" accept=".js,.txt,text/javascript" @change="importLXSourceFile" />
      </div>
      <div v-if="lxSources.length" class="lx-source-list">
        <div v-for="source in lxSources" :key="source.id" class="lx-source-row">
          <div class="source-copy">
            <strong>{{ source.name }} <el-tag v-if="activeLXSourceID === source.id" size="small" type="success">当前使用</el-tag> <el-tag size="small" :type="lxValidation[source.id] ? 'success' : 'info'">{{ lxValidation[source.id] ? '基础初始化通过' : '尚未验证' }}</el-tag></strong>
            <span>{{ source.author || '未知作者' }} · {{ source.version || '未标注版本' }} · SHA256 {{ source.sha256.slice(0, 12) }}</span>
            <span v-if="lxValidation[source.id]">已声明来源：{{ lxValidation[source.id].declared_sources.map(item => (item.name || item.key) + '（' + item.key + '）').join('、') }} · 支持动作：{{ [...new Set(lxValidation[source.id].declared_sources.flatMap(item => item.actions || []))].join('、') || '无' }} · 音质：{{ [...new Set(lxValidation[source.id].declared_sources.flatMap(item => item.qualitys || []))].join('、') || '未声明' }}</span>
            <span v-if="lxValidation[source.id]">已注册事件：{{ lxValidation[source.id].handlers.join('、') || '无' }}；仅声明 musicUrl 的来源可参与实际播放解析</span>
            <div v-if="lxSearchSourceOptions[source.id]?.length" class="lx-search-source-settings">
              <span>允许参与搜索的来源（脚本和搜索引擎均支持）：</span>
              <el-checkbox-group :model-value="lxSearchSourceOptions[source.id].filter(item => item.enabled).map(item => item.id)" @change="setLXSearchSources(source, $event)">
                <el-checkbox v-for="option in lxSearchSourceOptions[source.id]" :key="option.id" :label="option.id">{{ option.name }}</el-checkbox>
              </el-checkbox-group>
            </div>
            <span v-else-if="lxValidation[source.id]">该脚本没有可供当前搜索引擎使用的来源</span>
            <span v-if="source.source_url">{{ source.source_url }}</span>
          </div>
          <div class="lx-source-actions">
            <el-button link type="primary" @click="setActiveLXSource(source)">{{ activeLXSourceID === source.id ? '重新加载' : '启用' }}</el-button>
            <el-button v-if="activeLXSourceID === source.id" link @click="deactivateLXSource">停用</el-button>
            <el-checkbox :model-value="source.allow_show_update_alert" @change="setLXSourceUpdateAlert(source, $event)">允许更新提醒</el-checkbox>
            <el-button link type="primary" :loading="validatingLXSourceID === source.id" :disabled="!!validatingLXSourceID || importingLXSource" @click="validateLXSource(source)">验证脚本</el-button>
            <el-button v-if="source.source_url" link type="primary" :loading="importingLXSource" @click="updateLXSource(source)">检查更新</el-button>
            <el-button link type="danger" @click="deleteLXSource(source)">删除</el-button>
          </div>
        </div>
      </div>
      <el-empty v-else description="尚未导入自定义音源" :image-size="64" />
    </el-card>

    <el-card v-if="activeTab === 'search'" shadow="never">
      <template #header><strong>搜索歌曲</strong></template>
      <p class="section-description">这里只显示当前启用且已验证的音源脚本所声明、并具备 musicUrl 能力的平台。</p>
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
        <el-table-column label="操作" width="170" fixed="right"><template #default="scope"><el-button link :type="isFavorite(scope.row) ? 'danger' : 'primary'" @click="toggleFavorite(scope.row)">{{ isFavorite(scope.row) ? '取消收藏' : '收藏' }}</el-button><el-button link type="primary" @click="addToQueue(scope.row)">加入队列</el-button></template></el-table-column>
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

    <el-card v-if="activeTab === 'host-playlist'" shadow="never">
      <template #header><div class="card-heading"><strong>主播常驻歌单</strong><el-tag effect="plain">{{ favorites.length }} 首收藏</el-tag></div></template>
      <p class="section-description">这里保存你经常播放的歌曲，作为直播期间的默认背景音乐。观众点播会暂时打断当前歌曲，点播队列结束后自动恢复原歌曲和进度。</p>
      <div class="card-actions">
        <el-button type="primary" :disabled="favorites.length === 0" @click="addHostPlaylistToQueue">播放</el-button>
      </div>
      <el-table v-if="favorites.length" :data="favorites" :row-key="trackRowKey" style="width:100%">
        <el-table-column type="index" label="#" width="55" />
        <el-table-column prop="title" label="歌曲" min-width="180" />
        <el-table-column prop="artist" label="歌手" min-width="140" />
        <el-table-column prop="album" label="专辑" min-width="140" />
        <el-table-column label="音源" width="130"><template #default="scope">{{ sourceName(scope.row.source_id) }}</template></el-table-column>
        <el-table-column label="操作" width="220" fixed="right"><template #default="scope"><el-button link type="primary" @click="playFavorite(scope.row)">播放</el-button><el-button link :disabled="scope.$index === 0" @click="moveFavorite(scope.$index, -1)">上移</el-button><el-button link :disabled="scope.$index === favorites.length - 1" @click="moveFavorite(scope.$index, 1)">下移</el-button><el-button link type="danger" @click="removeFavorite(scope.row)">移除</el-button></template></el-table-column>
      </el-table>
      <el-empty v-else description="还没有收藏歌曲。可以在歌曲搜索中点击「收藏」，将常听歌曲加入主播歌单。" :image-size="76" />
    </el-card>

    <el-card v-if="activeTab === 'visitor-requests'" shadow="never">
      <template #header>
        <div class="card-heading">
          <div class="card-heading"><strong>观众点播队列</strong><el-tag effect="plain">{{ queue?.items.length ?? 0 }} 首待播</el-tag></div>
          <el-button type="danger" plain size="small" :disabled="!queue?.items.length" @click="clearQueue">清空待播</el-button>
        </div>
      </template>
      <div v-if="queue?.current" class="queue-current">当前播放：{{ queue.current.track.title }} · {{ queue.current.track.artist }}（{{ queue.current.requested_by || '观众点播' }}）</div>
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
          <span class="queue-drag-handle" title="拖动调整顺序"><el-icon><Headset /></el-icon></span>
          <span class="queue-index">{{ index + 1 }}</span>
          <div class="queue-track">
            <strong>{{ item.track.title }}</strong>
            <span>{{ item.track.artist }} · {{ sourceName(item.track.source_id) }}</span>
          </div>
          <span class="queue-requester">{{ item.requested_by || '观众' }}</span>
          <el-button link type="danger" @click.stop="removeQueueItem(item.id)">删除</el-button>
        </div>
      </div>
      <el-empty v-else description="队列暂时为空" :image-size="76" />
      <p v-if="queue?.items.length" class="hint">拖动歌曲左侧的拖动图标即可调整待播顺序；当前播放歌曲不受影响。</p>
    </el-card>

    <el-card v-if="activeTab === 'settings'" shadow="never">
      <template #header><strong>点歌设置</strong></template>
      <div v-if="config" class="setting-list">
        <div class="setting-row"><div><strong>启用音乐模块</strong><p>关闭后无法加入歌曲队列或播放歌曲，也不会处理弹幕点歌；MV 视频播放不受影响。</p></div><el-switch v-model="config.enabled" /></div>
        <el-divider />
        <div class="setting-row"><div><strong>允许弹幕点歌</strong><p>收到点歌指令后搜索已启用的音源并加入统一队列。</p></div><el-switch v-model="config.command_enabled" /></div>
        <el-divider />
        <div class="setting-row"><div><strong>点歌指令前缀</strong><p>例如：点歌 稻香 周杰伦。</p></div><el-input v-model="config.command_prefix" maxlength="16" style="width:220px" /></div>
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
          <strong>{{ playback?.track?.title ?? '尚未播放歌曲' }}</strong>
          <span>{{ playback?.track?.artist ?? '从主播歌单或歌曲搜索开始播放' }}</span>
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
.music-tabs{display:flex;align-items:center;gap:6px;overflow-x:auto;padding:0 2px 8px;border-bottom:1px solid var(--app-border)}
.music-tabs button{display:inline-flex;align-items:center;gap:7px;white-space:nowrap;border:0;border-bottom:2px solid transparent;background:transparent;color:var(--el-text-color-secondary);padding:11px 14px;margin-bottom:-1px;font:inherit;font-size:14px;cursor:pointer;transition:color .15s,border-color .15s}
.music-tabs button:hover{color:var(--el-text-color-primary)}
.music-tabs button.active{color:var(--app-accent);border-bottom-color:var(--app-accent);font-weight:600}
.music-tabs button span{font-size:11px;padding:1px 6px;border-radius:10px;background:var(--app-bg-control)}
.mini-player{position:sticky;bottom:-28px;z-index:30;display:flex;align-items:center;justify-content:space-between;gap:16px;flex-shrink:0;margin-top:auto;margin-left:-32px;margin-right:-32px;margin-bottom:-28px;padding:12px 32px calc(12px + env(safe-area-inset-bottom));min-height:68px;border:1px solid var(--app-border);border-bottom:0;border-radius:12px 12px 0 0;background:var(--el-bg-color);box-shadow:0 -8px 28px #00000018}
.mini-player-track,.mini-player-actions{display:flex;align-items:center;gap:12px;min-width:0}
.mini-player-track img{width:42px;height:42px;object-fit:cover;border-radius:7px}.mini-player-track>.el-icon{font-size:28px;color:var(--el-text-color-secondary)}
.mini-player-track>div{display:flex;flex-direction:column;gap:3px;min-width:0}
.mini-player-track strong,.mini-player-track span{overflow:hidden;text-overflow:ellipsis;white-space:nowrap;max-width:280px}
.mini-player-track span,.mini-player-actions>span{font-size:12px;color:var(--el-text-color-secondary)}
.music-page{display:flex;flex-direction:column;gap:20px;min-height:calc(100vh - 146px)}.page-heading{display:flex;align-items:flex-start;justify-content:space-between;gap:20px}.page-heading h2{margin:0;font-size:25px}.eyebrow{margin:0 0 9px;color:var(--app-accent);font-size:11px;letter-spacing:.15em}.description,.section-description,.source-copy span,.setting-row p,.hint{color:var(--el-text-color-secondary);font-size:13px}.section-description{margin:0 0 16px}.card-heading{display:flex;align-items:center;justify-content:space-between}.source-list,.setting-list{display:flex;flex-direction:column}.source-row,.setting-row{display:flex;align-items:center;justify-content:space-between;gap:24px;padding:14px 0;border-bottom:1px solid var(--app-border)}.source-row:last-child{border-bottom:0}.source-copy,.track-info{display:flex;flex-direction:column;gap:5px}.source-copy span,.track-info span,.track-info small{font-size:12px;color:var(--el-text-color-secondary)}.setting-row p{margin:6px 0 0}.card-actions{display:flex;justify-content:flex-end;gap:10px;margin-top:20px}.now-playing{display:flex;align-items:center;gap:16px}.track-cover{width:76px;height:76px;border-radius:10px;background:var(--app-bg-control);display:flex;align-items:center;justify-content:center;overflow:hidden;color:var(--el-text-color-secondary);font-size:30px}.track-cover img{width:100%;height:100%;object-fit:cover}.track-info strong{font-size:17px}.time-row{display:flex;justify-content:space-between;color:var(--el-text-color-secondary);font-size:12px;font-variant-numeric:tabular-nums}.player-controls{display:flex;flex-wrap:wrap;justify-content:center;gap:8px;margin:14px 0}.playback-order-row{display:flex;align-items:center;justify-content:center;gap:12px;margin:8px 0 14px}.playback-order-row span{font-size:13px;color:var(--el-text-color-secondary)}.volume-row{display:grid;grid-template-columns:50px 1fr;align-items:center;gap:12px}.volume-row span{font-size:13px;color:var(--el-text-color-secondary)}.hint{margin:12px 0 0}.search-row{display:flex;gap:10px}.lx-import-row{display:flex;gap:10px;align-items:center}.hidden-file-input{display:none}.lx-source-list{display:flex;flex-direction:column;margin-top:16px}.lx-source-row{display:flex;align-items:center;justify-content:space-between;gap:16px;padding:12px 0;border-top:1px solid var(--app-border)}.lx-source-row .source-copy{min-width:0}.lx-source-row .source-copy span{overflow-wrap:anywhere}.lx-search-source-settings{display:flex;flex-direction:column;gap:6px;margin-top:8px}.lx-search-source-settings :deep(.el-checkbox-group){display:flex;flex-wrap:wrap;gap:4px 14px}
.lx-source-actions{display:flex;align-items:center;gap:8px;flex-shrink:0}.search-summary{margin-top:12px;color:var(--el-text-color-secondary);font-size:12px}.search-pagination{display:flex;justify-content:center;margin-top:18px}.queue-current{padding:10px 12px;margin-bottom:12px;border-radius:8px;background:var(--app-bg-control);font-size:13px}..queue-list{display:flex;flex-direction:column;gap:8px}.queue-item{display:flex;align-items:center;gap:12px;padding:11px 12px;border:1px solid var(--app-border);border-radius:9px;background:var(--el-bg-color);transition:border-color .15s,opacity .15s}.queue-item:hover{border-color:var(--app-accent)}.queue-item-dragging{opacity:.45}.queue-drag-handle{display:flex;align-items:center;color:var(--el-text-color-secondary);cursor:grab;touch-action:none}.queue-drag-handle:active{cursor:grabbing}.queue-index{width:24px;color:var(--el-text-color-secondary);font-size:12px;text-align:center}.queue-track{display:flex;flex:1;flex-direction:column;gap:4px;min-width:0}.queue-track strong,.queue-track span{overflow:hidden;text-overflow:ellipsis;white-space:nowrap}.queue-track span,.queue-requester{color:var(--el-text-color-secondary);font-size:12px}.queue-requester{max-width:120px;overflow:hidden;text-overflow:ellipsis;white-space:nowrap}.setting-row p{margin:6px 0 0}@media(max-width:800px){.music-page{min-height:calc(100vh - 126px)}.mini-player{margin-left:-22px;margin-right:-22px;padding-left:22px;padding-right:22px}.source-row,.setting-row{align-items:flex-start;flex-direction:column}.search-row,.lx-import-row{flex-direction:column;align-items:stretch}.lx-source-row{align-items:flex-start}.mini-player{align-items:flex-start;flex-direction:column}.mini-player-actions{width:100%;justify-content:space-between}}
</style>