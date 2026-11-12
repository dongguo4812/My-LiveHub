<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { Headset, VideoPause, VideoPlay } from '@element-plus/icons-vue'
import * as MusicService from '../../bindings/github.com/dongguo4812/My-LiveHub/app/internal/desktop/musicservice.js'

type MusicConfig = Awaited<ReturnType<typeof MusicService.GetConfig>>
type Track = Awaited<ReturnType<typeof MusicService.Search>>['tracks'][number]
type QueueSnapshot = Awaited<ReturnType<typeof MusicService.GetQueue>>
type PlaybackSnapshot = Awaited<ReturnType<typeof MusicService.GetPlaybackStatus>>

const config = ref<MusicConfig | null>(null)
type LXUserSourceProxy = Awaited<ReturnType<typeof MusicService.GetLXUserSourceProxy>>
const lxProxy = ref<LXUserSourceProxy>({ enabled: false, host: '', port: '' })
const lxProxyBusy = ref(false)
type MusicTab = 'now-playing' | 'host-playlist' | 'visitor-requests' | 'search' | 'settings'
const activeTab = ref<MusicTab>('now-playing')
const favorites = ref<Track[]>([])
type HostTrackSourceResults = { source_id: string; name: string; tracks: Track[]; error?: string }
const replaceDialogVisible = ref(false)
const replacingTrack = ref<Track | null>(null)
const replacementGroups = ref<HostTrackSourceResults[]>([])
const replacementSourceID = ref('')
const selectedReplacement = ref<Track | null>(null)
const replacementSearching = ref(false)
const replacementSaving = ref(false)

const hostPlayMode = ref('list')
type HostPlaylistSummary = { id: string; name: string; track_count: number; mode: string }
const hostPlaylists = ref<HostPlaylistSummary[]>([])
const activeHostPlaylistID = ref('')
const playlistFileInput = ref<HTMLInputElement | null>(null)
const importingPlaylist = ref(false)
type LXSource = Awaited<ReturnType<typeof MusicService.GetLXUserSources>>[number]
type LXValidation = Awaited<ReturnType<typeof MusicService.ValidateLXUserSource>>
type LXSearchSourceOption = Awaited<ReturnType<typeof MusicService.GetLXSearchSourceOptions>>[number]
const lxSources = ref<LXSource[]>([])
const lxValidation = ref<Record<string, LXValidation>>({})
const lxSearchSourceOptions = ref<Record<string, LXSearchSourceOption[]>>({})
// Serialize writes per LX source and ignore stale read-backs from earlier UI changes.
const lxSearchSourceSaveQueues = new Map<string, Promise<void>>()
const lxSearchSourceRevisions = new Map<string, number>()
let lxSourcesRefreshRevision = 0
const activeLXSourceID = ref('')
const validatingLXSourceID = ref('')
const lxSourceURL = ref('')
const importingLXSource = ref(false)
const sourceFileInput = ref<HTMLInputElement | null>(null)
const queue = ref<QueueSnapshot | null>(null)
const draggedQueueItemID = ref('')
const playback = ref<PlaybackSnapshot | null>(null)
const syncedLyrics = ref<Array<{ time: number; text: string }>>([])
const lyricsTrackKey = ref('')
const lyricsStatus = ref<'idle' | 'loading' | 'ready' | 'empty' | 'error'>('idle')
let lyricsRequestID = 0
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

const seeking = ref(false)
const seekPreview = ref(0)
const progress = computed(() => {
  if (seeking.value) return seekPreview.value
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
function parseSyncedLyrics(lrc: string) {
  const lines: Array<{ time: number; text: string }> = []
  for (const line of lrc.split(/\r?\n/)) {
    const match = line.match(/^\[(\d{1,2}):(\d{2})(?:[.:](\d{1,3}))?\](.*)$/)
    if (!match) continue
    const fraction = match[3] ?? '0'
    const milliseconds = Number(match[1]) * 60000 + Number(match[2]) * 1000 + Number(fraction.padEnd(3, '0').slice(0, 3))
    lines.push({ time: milliseconds, text: match[4].trim() })
  }
  return lines.filter(line => line.text).sort((a, b) => a.time - b.time)
}
const currentLyric = computed(() => {
  if (lyricsStatus.value === 'loading' || lyricsStatus.value === 'idle') return '歌词加载中…'
  if (lyricsStatus.value === 'error') return '歌词获取失败，稍后重试'
  if (lyricsStatus.value === 'empty' || lyricsStatus.value === 'ready' && syncedLyrics.value.length === 0) return '暂无歌词'
  const position = playback.value?.position_ms ?? 0
  let active = ''
  for (const line of syncedLyrics.value) {
    if (line.time > position) break
    active = line.text
  }
  return active || syncedLyrics.value[0]?.text || '歌词加载中…'
})
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
  return id ? (names[id] ?? id) : '洛雪音源'
}
function qualityNames(track: Track) {
  return (track.qualities ?? []).map(item => item.type).join(' / ') || '未知'
}
function trackRowKey(row: Track) { return `${row.source_id}:${row.id}` }
function isCurrentHostTrack(track: Track) {
  return queue.value?.current?.requested_by === '__my_livehub_host_playlist__'
    && !!playback.value?.track
    && trackRowKey(track) === trackRowKey(playback.value.track)
}
function isFavorite(track: Track) { return favorites.value.some(item => trackRowKey(item) === trackRowKey(track)) }
async function refreshHostPlaylists(preferredID = activeHostPlaylistID.value) {
  const api = MusicService as any
  const result = await api.GetHostPlaylists() as [HostPlaylistSummary[], string]
  hostPlaylists.value = result[0] ?? []
  const nextID = hostPlaylists.value.some(item => item.id === preferredID) ? preferredID : (result[1] || hostPlaylists.value[0]?.id || '')
  activeHostPlaylistID.value = nextID
  if (nextID && nextID !== result[1]) await api.SelectHostPlaylist(nextID)
  if (nextID) {
    const playlist = await api.GetHostPlaylistByID(nextID)
    favorites.value = playlist.tracks ?? []
    hostPlayMode.value = playlist.mode ?? 'list'
  } else {
    favorites.value = []
    hostPlayMode.value = 'list'
  }
}
async function loadFavorites() {
  try { await refreshHostPlaylists() }
  catch (reason) {
    error.value = reason instanceof Error ? reason.message : String(reason)
    favorites.value = []
  }
}
async function selectHostPlaylist(id: string) {
  if (!id) return
  try {
    await (MusicService as any).SelectHostPlaylist(id)
    await refreshHostPlaylists(id)
    success.value = '已切换歌单；当前正在播放的歌曲不会被打断'
  } catch (reason) { error.value = reason instanceof Error ? reason.message : String(reason) }
}
async function createHostPlaylist() {
  const name = window.prompt('请输入新歌单名称')
  if (!name?.trim()) return
  try {
    const created = await (MusicService as any).CreateHostPlaylist(name.trim())
    await refreshHostPlaylists(created.id)
    success.value = `已创建歌单：${created.name}`
  } catch (reason) { error.value = reason instanceof Error ? reason.message : String(reason) }
}
async function renameHostPlaylist() {
  const current = hostPlaylists.value.find(item => item.id === activeHostPlaylistID.value)
  if (!current) return
  const name = window.prompt('修改歌单名称', current.name)
  if (!name?.trim() || name.trim() === current.name) return
  try {
    await (MusicService as any).RenameHostPlaylist(current.id, name.trim())
    await refreshHostPlaylists(current.id)
    success.value = '歌单名称已修改'
  } catch (reason) { error.value = reason instanceof Error ? reason.message : String(reason) }
}
async function deleteHostPlaylist() {
  const current = hostPlaylists.value.find(item => item.id === activeHostPlaylistID.value)
  if (!current || !window.confirm(`确定删除歌单“${current.name}”吗？歌单内歌曲也会一并删除。`)) return
  try {
    await (MusicService as any).DeleteHostPlaylist(current.id)
    await refreshHostPlaylists()
    success.value = '歌单已删除'
  } catch (reason) { error.value = reason instanceof Error ? reason.message : String(reason) }
}
function choosePlaylistFile() { playlistFileInput.value?.click() }
async function importLXPlaylist(event: Event) {
  const input = event.target as HTMLInputElement
  const file = input.files?.[0]
  if (!file) return
  importingPlaylist.value = true
  error.value = ''
  success.value = ''
  try {
    if (file.size > 64 * 1024 * 1024) throw new Error('歌单备份不能超过 64 MB')
    const bytes = new Uint8Array(await file.arrayBuffer())
    let binary = ''
    for (let offset = 0; offset < bytes.length; offset += 0x8000) binary += String.fromCharCode(...bytes.subarray(offset, offset + 0x8000))
    const imported = await (MusicService as any).ImportLXPlaylistFile(file.name, btoa(binary)) as HostPlaylistSummary[]
    await refreshHostPlaylists(imported?.[0]?.id)
    success.value = `成功导入 ${imported.length} 个洛雪歌单${imported.length ? '：' + imported.map(item => item.name + '（' + item.track_count + '首）').join('、') : ''}`
  } catch (reason) { error.value = reason instanceof Error ? reason.message : String(reason) }
  finally { importingPlaylist.value = false; input.value = '' }
}

async function toggleFavorite(track: Track) {
  error.value = ''
  try {
    if (isFavorite(track)) await (MusicService as any).RemoveHostTrackFromPlaylist(activeHostPlaylistID.value, track.source_id, track.id)
    else await (MusicService as any).AddHostTrackToPlaylist(activeHostPlaylistID.value, track)
    await loadFavorites()
    success.value = isFavorite(track) ? '已加入当前歌单' : '已从当前歌单移除'
  } catch (reason) { error.value = reason instanceof Error ? reason.message : String(reason) }
}
async function removeFavorite(track: Track) {
  try {
    await (MusicService as any).RemoveHostTrackFromPlaylist(activeHostPlaylistID.value, track.source_id, track.id)
    await loadFavorites()
  } catch (reason) { error.value = reason instanceof Error ? reason.message : String(reason) }
}
async function moveTrackToPlaylist(track: Track, targetPlaylistID: string) {
  if (!targetPlaylistID || targetPlaylistID === activeHostPlaylistID.value) return
  const target = hostPlaylists.value.find(item => item.id === targetPlaylistID)
  if (!target) return
  try {
    await (MusicService as any).MoveHostTrackBetweenPlaylists(activeHostPlaylistID.value, targetPlaylistID, track.source_id, track.id)
    await refreshHostPlaylists(activeHostPlaylistID.value)
    await loadFavorites()
    success.value = `已将「${track.title}」移动到「${target.name}」`
  } catch (reason) { error.value = reason instanceof Error ? reason.message : String(reason) }
}
async function playFavorite(track: Track) {
  const index = favorites.value.findIndex(item => trackRowKey(item) === trackRowKey(track))
  if (index < 0) return
  try { await (MusicService as any).PlayHostTrackFromPlaylist(activeHostPlaylistID.value, index); await refreshPlayback(); success.value = '开始播放当前歌单' }
  catch (reason) { error.value = reason instanceof Error ? reason.message : String(reason) }
}
async function openReplaceTrack(track: Track) {
  replacingTrack.value = track
  selectedReplacement.value = null
  replacementGroups.value = []
  replacementSourceID.value = ''
  replaceDialogVisible.value = true
  replacementSearching.value = true
  error.value = ''
  try {
    const groups = await (MusicService as any).SearchHostTrackAlternatives(track) as HostTrackSourceResults[]
    replacementGroups.value = groups ?? []
    replacementSourceID.value = replacementGroups.value[0]?.source_id ?? ''
  } catch (reason) {
    error.value = reason instanceof Error ? reason.message : String(reason)
  } finally {
    replacementSearching.value = false
  }
}
async function confirmReplaceTrack() {
  const original = replacingTrack.value
  const replacement = selectedReplacement.value
  if (!original || !replacement || !activeHostPlaylistID.value) return
  replacementSaving.value = true
  error.value = ''
  try {
    await (MusicService as any).ReplaceHostTrackInPlaylist(activeHostPlaylistID.value, original.source_id, original.id, replacement)
    await refreshHostPlaylists(activeHostPlaylistID.value)
    await refreshPlayback()
    replaceDialogVisible.value = false
    success.value = '已将「' + original.title + '」换源为「' + replacement.title + '」'
  } catch (reason) {
    error.value = reason instanceof Error ? reason.message : String(reason)
  } finally {
    replacementSaving.value = false
  }
}
async function addHostPlaylistToQueue() {
  try { await (MusicService as any).PlayHostPlaylistByID(activeHostPlaylistID.value); await refreshPlayback(); success.value = '已开始播放当前歌单' }
  catch (reason) { error.value = reason instanceof Error ? reason.message : String(reason) }
}
async function moveFavorite(index: number, offset: number) {
  const target = index + offset
  if (target < 0 || target >= favorites.value.length) return
  try { await (MusicService as any).MoveHostTrackInPlaylist(activeHostPlaylistID.value, index, target); await loadFavorites() }
  catch (reason) { error.value = reason instanceof Error ? reason.message : String(reason) }
}
async function changeHostPlayMode(mode: string) {
  try {
    await (MusicService as any).SetHostPlaylistPlayMode(activeHostPlaylistID.value, mode)
    hostPlayMode.value = mode
    await refreshHostPlaylists(activeHostPlaylistID.value)
  } catch (reason) { error.value = reason instanceof Error ? reason.message : String(reason) }
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
  const refreshRevision = ++lxSourcesRefreshRevision
  try {
    const sources = await MusicService.GetLXUserSources()
    if (refreshRevision !== lxSourcesRefreshRevision) return

    // Take a per-source snapshot before reading persisted options. A refresh
    // must never replace optimistic checkbox state while a save is in flight,
    // nor may an older response overwrite a newer user interaction.
    const optionEntries = await Promise.all(sources.map(async source => {
      const revision = lxSearchSourceRevisions.get(source.id) ?? 0
      const saveInProgress = lxSearchSourceSaveQueues.has(source.id)
      const options = await MusicService.GetLXSearchSourceOptions(source.id)
      return { id: source.id, options, revision, saveInProgress }
    }))
    if (refreshRevision !== lxSourcesRefreshRevision) return

    lxSources.value = sources
    const nextOptions: Record<string, LXSearchSourceOption[]> = {}
    for (const entry of optionEntries) {
      const revisionUnchanged = (lxSearchSourceRevisions.get(entry.id) ?? 0) === entry.revision
      const current = lxSearchSourceOptions.value[entry.id]
      nextOptions[entry.id] = !entry.saveInProgress && revisionUnchanged
        ? entry.options
        : (current ?? entry.options)
    }
    lxSearchSourceOptions.value = nextOptions

    // Restore each source's persisted validation result into the view state.
    // The backend returns this only for the exact script version that passed.
    lxValidation.value = Object.fromEntries(
      sources.flatMap(source => {
        const persisted = (source as LXSource & { validation?: LXValidation | null }).validation
        return persisted?.initialized ? [[source.id, persisted]] : []
      }),
    )
    const activeSourceID = await MusicService.GetActiveLXUserSourceID()
    if (refreshRevision !== lxSourcesRefreshRevision) return
    activeLXSourceID.value = activeSourceID
    if (selectedSource.value !== 'all' && !searchableSources.value.some(source => source.id === selectedSource.value)) {
      selectedSource.value = 'all'
    }
  } catch (reason) {
    if (refreshRevision === lxSourcesRefreshRevision) {
      error.value = reason instanceof Error ? reason.message : String(reason)
    }
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
  // Validation performs multiple asynchronous backend calls. Keep the search
  // selection revision from before validation so its read-back cannot replace
  // checkbox changes made while the script is initializing.
  const searchRevision = lxSearchSourceRevisions.get(source.id) ?? 0
  const saveInProgress = lxSearchSourceSaveQueues.has(source.id)
  try {
    const result = await MusicService.ValidateLXUserSource(source.id)
    lxValidation.value = { ...lxValidation.value, [source.id]: result }
    const options = await MusicService.GetLXSearchSourceOptions(source.id)
    const revisionUnchanged = (lxSearchSourceRevisions.get(source.id) ?? 0) === searchRevision
    const noSaveInProgress = !saveInProgress && !lxSearchSourceSaveQueues.has(source.id)
    if (revisionUnchanged && noSaveInProgress) {
      lxSearchSourceOptions.value = { ...lxSearchSourceOptions.value, [source.id]: options }
    }
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
async function persistLXSearchPriority(source: LXSource, options: LXSearchSourceOption[]) {
  const revision = lxSearchSourceRevisions.get(source.id) ?? 0
  const enabled = options.filter(item => item.enabled).map(item => item.id)
  const previous = lxSearchSourceSaveQueues.get(source.id) ?? Promise.resolve()
  const save = previous.catch(() => undefined).then(async () => {
    await MusicService.SetLXSearchSources(source.id, enabled)
    const persisted = await MusicService.GetLXSearchSourceOptions(source.id)
    const persistedEnabled = persisted.filter(item => item.enabled).map(item => item.id)
    if (persistedEnabled.length !== enabled.length || persistedEnabled.some((id, index) => id !== enabled[index])) {
      throw new Error('搜索来源保存结果与当前选择不一致，请重试')
    }
    if (lxSearchSourceRevisions.get(source.id) === revision) {
      lxSearchSourceOptions.value = { ...lxSearchSourceOptions.value, [source.id]: persisted.map(item => ({ ...item })) }
      if (activeLXSourceID.value === source.id && selectedSource.value !== 'all' && !searchableSources.value.some(item => item.id === selectedSource.value)) selectedSource.value = 'all'
      results.value = []
    }
  })
  lxSearchSourceSaveQueues.set(source.id, save)
  try { await save }
  finally { if (lxSearchSourceSaveQueues.get(source.id) === save) lxSearchSourceSaveQueues.delete(source.id) }
}
function onLXSearchSourceChange(source: LXSource) {
  // v-model has already updated the reactive option. Read the complete checked
  // list from that single source of truth so the priority list and saved value
  // cannot diverge from the checkbox state.
  const selected = (lxSearchSourceOptions.value[source.id] ?? [])
    .filter(item => item.enabled)
    .map(item => item.id)
  void setLXSearchSources(source, selected)
}
async function setLXSearchSources(source: LXSource, selected: string[]) {
  const current = lxSearchSourceOptions.value[source.id] ?? []
  const selectedSet = new Set(selected.map(String))
  const ordered = current.filter(item => item.enabled && selectedSet.has(item.id))
  for (const item of current) if (selectedSet.has(item.id) && !ordered.some(existing => existing.id === item.id)) ordered.push(item)
  const updated = current.map(item => ({ ...item, enabled: selectedSet.has(item.id) }))
  const reordered = [...ordered, ...updated.filter(item => !item.enabled)]
  const revision = (lxSearchSourceRevisions.get(source.id) ?? 0) + 1
  lxSearchSourceRevisions.set(source.id, revision)
  lxSearchSourceOptions.value = { ...lxSearchSourceOptions.value, [source.id]: reordered }
  try {
    await persistLXSearchPriority(source, reordered)
    success.value = '已保存搜索来源及优先级'
  } catch (reason) {
    error.value = reason instanceof Error ? reason.message : String(reason)
    if (lxSearchSourceRevisions.get(source.id) === revision && !lxSearchSourceSaveQueues.has(source.id)) {
      try {
        const persisted = await MusicService.GetLXSearchSourceOptions(source.id)
        if (lxSearchSourceRevisions.get(source.id) === revision) lxSearchSourceOptions.value = { ...lxSearchSourceOptions.value, [source.id]: persisted }
      } catch { /* Keep the last known state and show the original save error. */ }
    }
  }
}
async function moveLXSearchPriority(source: LXSource, index: number, direction: -1 | 1) {
  const current = [...(lxSearchSourceOptions.value[source.id] ?? [])]
  const enabled = current.filter(item => item.enabled)
  const target = index + direction
  if (target < 0 || target >= enabled.length) return
  const [moved] = enabled.splice(index, 1)
  enabled.splice(target, 0, moved)
  const disabled = current.filter(item => !item.enabled)
  const reordered = [...enabled, ...disabled]
  const revision = (lxSearchSourceRevisions.get(source.id) ?? 0) + 1
  lxSearchSourceRevisions.set(source.id, revision)
  lxSearchSourceOptions.value = { ...lxSearchSourceOptions.value, [source.id]: reordered }
  try {
    await persistLXSearchPriority(source, reordered)
    success.value = '已调整音源搜索优先级'
  } catch (reason) {
    error.value = reason instanceof Error ? reason.message : String(reason)
    if (lxSearchSourceRevisions.get(source.id) === revision && !lxSearchSourceSaveQueues.has(source.id)) {
      try {
        const persisted = await MusicService.GetLXSearchSourceOptions(source.id)
        if (lxSearchSourceRevisions.get(source.id) === revision) lxSearchSourceOptions.value = { ...lxSearchSourceOptions.value, [source.id]: persisted }
      } catch { /* Keep the last known state and show the original save error. */ }
    }
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
    void loadTrackLyrics()
    if (playback.value.volume >= 0) volume.value = playback.value.volume
  } catch { /* The engine is not started until the first play request. */ }
  await refreshQueue()
  await pollLXSourceUpdates()
}
async function loadLXProxy() {
  try { lxProxy.value = await MusicService.GetLXUserSourceProxy() }
  catch (reason) { error.value = reason instanceof Error ? reason.message : String(reason) }
}
async function saveLXProxy() {
  lxProxyBusy.value = true
  error.value = ''
  success.value = ''
  try {
    lxProxy.value = await MusicService.UpdateLXUserSourceProxy(lxProxy.value.enabled, lxProxy.value.host, lxProxy.value.port)
    success.value = '自定义音源网络代理设置已保存并应用'
  } catch (reason) {
    error.value = reason instanceof Error ? reason.message : String(reason)
  } finally {
    lxProxyBusy.value = false
  }
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
function previewSeek(value: number | number[]) {
  const percent = Array.isArray(value) ? value[0] : value
  seekPreview.value = Math.min(100, Math.max(0, Number(percent) || 0))
  seeking.value = true
}
async function seekTo(value: number | number[]) {
  if (!playback.value || playback.value.duration_ms <= 0) {
    seeking.value = false
    return
  }
  const percent = Math.min(100, Math.max(0, Number(Array.isArray(value) ? value[0] : value) || 0))
  const targetMS = Math.round(playback.value.duration_ms * percent / 100)
  try {
    await MusicService.Seek(targetMS)
    await refreshPlayback()
  } catch (reason) {
    error.value = reason instanceof Error ? reason.message : String(reason)
  } finally {
    seeking.value = false
  }
}

onMounted(() => {
  void loadFavorites()
  void loadConfig()
  void loadLXProxy()
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
          <strong>{{ playback?.track ? `${playback.track.title} - ${playback.track.artist || '未知歌手'}` : '尚未播放歌曲' }}</strong>
          <span class="current-lyric">{{ playback?.track ? currentLyric : '搜索歌曲并加入播放队列' }}</span>
          <small v-if="playback?.track">{{ sourceName(playback.track.source_id) }}</small>
          <el-tag v-if="queue?.current?.requested_by === '__my_livehub_host_playlist__'" type="success" size="small">主播歌单</el-tag>
          <el-tag v-else-if="queue?.current && queue.current.requested_by !== '__my_livehub_search_play__'" type="warning" size="small">观众点播</el-tag>
        </div>
      </div>
      <el-slider :model-value="progress" :min="0" :max="100" :step="0.1" :format-tooltip="formatSeekTooltip" :disabled="!playback?.track || !playback.duration_ms" @input="previewSeek" @change="seekTo" />
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
      <div class="now-playing-list">
        <div class="now-playing-list-header">
          <div>
            <strong>当前播放列表</strong>
            <span>{{ hostPlaylists.find(item => item.id === activeHostPlaylistID)?.name || '未选择歌单' }} · {{ favorites.length }} 首歌曲</span>
          </div>
          <el-button link type="primary" @click="activeTab = 'host-playlist'">管理歌单</el-button>
        </div>
        <div v-if="favorites.length" class="now-playing-track-list">
          <div
            v-for="(track, index) in favorites"
            :key="trackRowKey(track)"
            class="now-playing-track"
            :class="{ 'now-playing-track-active': isCurrentHostTrack(track) }"
          >
            <span class="now-playing-track-index">{{ String(index + 1).padStart(2, '0') }}</span>
            <div class="now-playing-track-info">
              <strong>{{ track.title }}</strong>
              <span>{{ track.artist || '未知歌手' }} · {{ sourceName(track.source_id) }}</span>
            </div>
            <el-tag v-if="isCurrentHostTrack(track)" type="success" size="small">正在播放</el-tag>
            <el-button v-else link type="primary" @click="playFavorite(track)">播放</el-button>
          </div>
        </div>
        <el-empty v-else description="当前歌单还没有歌曲，可前往主播歌单添加或导入歌曲。" :image-size="64">
          <el-button type="primary" plain @click="activeTab = 'host-playlist'">前往主播歌单</el-button>
        </el-empty>
      </div>
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
              <div class="lx-search-source-checkboxes">
                <label v-for="option in lxSearchSourceOptions[source.id]" :key="option.id" class="lx-search-source-toggle">
                  <input class="lx-search-source-checkbox" type="checkbox" v-model="option.enabled" @change="onLXSearchSourceChange(source)">
                  <span>{{ option.name }}</span>
                </label>
              </div>
              <div class="lx-search-priority-list">
                <strong>搜索优先级（从上到下）</strong>
                <template v-if="lxSearchSourceOptions[source.id].some(item => item.enabled)">
                  <div v-for="(option, index) in lxSearchSourceOptions[source.id].filter(item => item.enabled)" :key="option.id" class="lx-search-priority-row">
                    <strong>{{ index + 1 }}. {{ option.name }}</strong>
                    <span class="lx-search-priority-actions">
                      <el-button link size="small" :disabled="index === 0" @click="moveLXSearchPriority(source, index, -1)">上移</el-button>
                      <el-button link size="small" :disabled="index === lxSearchSourceOptions[source.id].filter(item => item.enabled).length - 1" @click="moveLXSearchPriority(source, index, 1)">下移</el-button>
                    </span>
                  </div>
                </template>
                <span v-else class="lx-search-priority-empty">尚未勾选搜索来源</span>
              </div>
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
        <el-table-column label="操作" width="230" fixed="right"><template #default="scope"><el-button link type="success" @click="playSearchTrackNow(scope.row)">播放</el-button><el-button link :type="isFavorite(scope.row) ? 'danger' : 'primary'" @click="toggleFavorite(scope.row)">{{ isFavorite(scope.row) ? '取消收藏' : '收藏' }}</el-button><el-button link type="primary" @click="addToQueue(scope.row)">加入队列</el-button></template></el-table-column>
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
      <template #header>
        <div class="playlist-header">
          <div><strong>主播歌单</strong><p class="section-description">管理多组直播背景音乐。观众点播结束后，会继续播放被打断的歌曲。</p></div>
          <div class="playlist-header-actions">
            <el-button @click="createHostPlaylist">新建歌单</el-button>
            <el-button type="primary" plain :loading="importingPlaylist" @click="choosePlaylistFile">导入洛雪歌单</el-button>
            <input ref="playlistFileInput" class="hidden-file-input" type="file" accept=".lxmc,.json,application/json" @change="importLXPlaylist" />
          </div>
        </div>
      </template>
      <div class="playlist-toolbar">
        <el-select v-model="activeHostPlaylistID" style="width:min(320px, 100%)" placeholder="选择歌单" @change="selectHostPlaylist">
          <el-option v-for="item in hostPlaylists" :key="item.id" :label="`${item.name} · ${item.track_count} 首`" :value="item.id" />
        </el-select>
        <el-button :disabled="!activeHostPlaylistID" @click="renameHostPlaylist">重命名</el-button>
        <el-button :disabled="hostPlaylists.length <= 1" type="danger" plain @click="deleteHostPlaylist">删除歌单</el-button>
        <el-tag effect="plain">{{ favorites.length }} 首歌曲</el-tag>
        <el-button type="primary" :disabled="favorites.length === 0" @click="addHostPlaylistToQueue">播放当前歌单</el-button>
      </div>
      <div v-if="hostPlaylists.length" class="playlist-cards">
        <button v-for="item in hostPlaylists" :key="item.id" class="playlist-card" :class="{ active: item.id === activeHostPlaylistID }" @click="selectHostPlaylist(item.id)">
          <span class="playlist-card-icon">♫</span>
          <span class="playlist-card-name">{{ item.name }}</span>
          <span class="playlist-card-meta">{{ item.track_count }} 首歌曲 · {{ item.mode === 'single' ? '单曲循环' : item.mode === 'shuffle' ? '随机播放' : '列表循环' }}</span>
        </button>
      </div>
      <el-table v-if="favorites.length" :data="favorites" :row-key="trackRowKey" style="width:100%">
        <el-table-column type="index" label="#" width="55" />
        <el-table-column prop="title" label="歌曲" min-width="180" />
        <el-table-column prop="artist" label="歌手" min-width="140" />
        <el-table-column prop="album" label="专辑" min-width="140" />
        <el-table-column label="音源" width="130"><template #default="scope">{{ sourceName(scope.row.source_id) }}</template></el-table-column>
        <el-table-column label="操作" width="220" fixed="right"><template #default="scope"><el-button link type="primary" @click="playFavorite(scope.row)">播放</el-button><el-button link type="primary" @click="openReplaceTrack(scope.row)">换源</el-button><el-button link :disabled="scope.$index === 0" @click="moveFavorite(scope.$index, -1)">上移</el-button><el-button link :disabled="scope.$index === favorites.length - 1" @click="moveFavorite(scope.$index, 1)">下移</el-button><el-dropdown :disabled="hostPlaylists.length <= 1" @command="(targetID: string) => moveTrackToPlaylist(scope.row, targetID)">
            <el-button link type="primary" :disabled="hostPlaylists.length <= 1">移动到</el-button>
            <template #dropdown><el-dropdown-menu>
              <el-dropdown-item v-for="item in hostPlaylists.filter(item => item.id !== activeHostPlaylistID)" :key="item.id" :command="item.id">{{ item.name }}</el-dropdown-item>
            </el-dropdown-menu></template>
          </el-dropdown><el-button link type="danger" @click="removeFavorite(scope.row)">移除</el-button></template></el-table-column>
      </el-table>
      <el-empty v-else description="这个歌单还没有歌曲。可以从搜索结果收藏歌曲，或导入洛雪歌单。" :image-size="76" />
    </el-card>

    <el-dialog v-model="replaceDialogVisible" title="歌曲换源" width="min(820px, 94vw)" destroy-on-close>
      <div v-if="replacingTrack" class="replace-track-original">
        <strong>原歌曲</strong>
        <div>{{ replacingTrack.title }} · {{ replacingTrack.artist || '未知歌手' }}</div>
        <small>{{ sourceName(replacingTrack.source_id) }} · {{ replacingTrack.album || '未知专辑' }} · {{ formatDuration(replacingTrack.duration_ms) }}</small>
      </div>
      <el-skeleton v-if="replacementSearching" :rows="5" animated />
      <el-empty v-else-if="replacementGroups.length === 0" description="当前启用的音源没有可搜索的平台，请检查音乐音源设置。" />
      <template v-else>
        <el-tabs v-model="replacementSourceID">
          <el-tab-pane v-for="group in replacementGroups" :key="group.source_id" :name="group.source_id">
            <template #label>{{ group.name }} <el-tag size="small" effect="plain">{{ group.tracks?.length ?? 0 }}</el-tag></template>
            <el-alert v-if="group.error" :title="group.error" type="warning" :closable="false" show-icon />
            <el-empty v-else-if="!group.tracks?.length" description="这个平台没有搜索到匹配歌曲" :image-size="64" />
            <el-radio-group v-else v-model="selectedReplacement" class="replace-candidate-list">
              <el-radio v-for="candidate in group.tracks" :key="trackRowKey(candidate)" :value="candidate" class="replace-candidate">
                <span class="replace-candidate-main">
                  <strong>{{ candidate.title }}</strong>
                  <small>{{ candidate.artist || '未知歌手' }} · {{ candidate.album || '未知专辑' }} · {{ formatDuration(candidate.duration_ms) }}</small>
                </span>
              </el-radio>
            </el-radio-group>
          </el-tab-pane>
        </el-tabs>
      </template>
      <div v-if="selectedReplacement" class="replace-track-comparison">
        <strong>将替换为</strong>
        <div>{{ selectedReplacement.title }} · {{ selectedReplacement.artist || '未知歌手' }}</div>
        <small>{{ sourceName(selectedReplacement.source_id) }} · {{ selectedReplacement.album || '未知专辑' }} · {{ formatDuration(selectedReplacement.duration_ms) }}</small>
      </div>
      <template #footer>
        <el-button @click="replaceDialogVisible = false">取消</el-button>
        <el-button type="primary" :disabled="!selectedReplacement" :loading="replacementSaving" @click="confirmReplaceTrack">确认换源</el-button>
      </template>
    </el-dialog>

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
          <span class="queue-drag-handle" title="拖动调整顺序">⠿</span>
          <span class="queue-index">{{ index + 1 }}</span>
          <div class="queue-track">
            <strong>{{ item.track.title }}</strong>
            <span>{{ item.track.artist }} · {{ sourceName(item.track.source_id) }}</span>
          </div>
          <span v-if="item.requested_by !== '__my_livehub_search_play__'" class="queue-requester">{{ item.requested_by || '观众' }}</span>
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
        <div class="setting-row"><div><strong>允许主播和房管切歌</strong><p>只有当前直播间主播本人或房管发送切歌命令时，才会播放下一首；普通观众无效。</p></div><el-switch v-model="config.skip_command_enabled" /></div>
        <el-divider />
        <div class="setting-row"><div><strong>切歌命令</strong><p>发送完整命令即可切换下一首，例如：切歌。</p></div><el-input v-model="config.skip_command" maxlength="16" style="width:220px" /></div>
        <el-divider />
        <div class="setting-row"><div><strong>播放队列容量</strong><p>统一队列允许等待的歌曲数量。</p></div><el-input-number v-model="config.queue_capacity" :min="1" :max="1000" /></div>
      </div>
      <div class="card-actions"><el-button type="primary" :loading="saving" :disabled="!config" @click="saveConfig">保存全部设置</el-button></div>
    </el-card>
    <el-card v-if="activeTab === 'settings'" shadow="never">
      <template #header><strong>自定义音源网络代理</strong></template>
      <p class="section-description">为导入的洛雪自定义音源指定 HTTP 代理。关闭时使用系统环境代理配置；未配置系统代理则直连。保存后立即应用到当前运行中的音源。</p>
      <div class="setting-row"><div><strong>启用自定义代理</strong><p>开启后，音源脚本发出的网络请求将通过下方代理地址。</p></div><el-switch v-model="lxProxy.enabled" /></div>
      <el-divider />
      <div class="lx-proxy-fields">
        <label>代理主机地址<el-input v-model="lxProxy.host" placeholder="127.0.0.1" /></label>
        <label>代理端口<el-input v-model="lxProxy.port" placeholder="7890" /></label>
      </div>
      <div class="card-actions"><el-button type="primary" :loading="lxProxyBusy" @click="saveLXProxy">保存并立即应用</el-button></div>
    </el-card>
    <div v-if="activeTab !== 'now-playing'" class="mini-player">
      <div class="mini-player-track">
        <img v-if="playback?.track?.cover_url" :src="playback.track.cover_url" alt="当前歌曲封面" />
        <el-icon v-else><Headset /></el-icon>
        <div>
          <strong>{{ playback?.track ? `${playback.track.title} - ${playback.track.artist || '未知歌手'}` : '尚未播放歌曲' }}</strong>
          <span class="current-lyric">{{ playback?.track ? currentLyric : '从主播歌单或歌曲搜索开始播放' }}</span>
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
.current-lyric{display:block;overflow:hidden;text-overflow:ellipsis;white-space:nowrap;color:var(--el-text-color-secondary);font-size:12px;line-height:1.5}.mini-player-track>div{min-width:0}.mini-player-track .current-lyric{max-width:min(52vw,560px)}.track-info .current-lyric{max-width:100%}.mini-player{position:sticky;bottom:-28px;z-index:30;display:flex;align-items:center;justify-content:space-between;gap:16px;flex-shrink:0;margin-top:auto;margin-left:-32px;margin-right:-32px;margin-bottom:-28px;padding:12px 32px calc(12px + env(safe-area-inset-bottom));min-height:68px;border:1px solid var(--app-border);border-bottom:0;border-radius:12px 12px 0 0;background:var(--el-bg-color);box-shadow:0 -8px 28px #00000018}
.mini-player-track,.mini-player-actions{display:flex;align-items:center;gap:12px;min-width:0}
.mini-player-track img{width:42px;height:42px;object-fit:cover;border-radius:7px}.mini-player-track>.el-icon{font-size:28px;color:var(--el-text-color-secondary)}
.mini-player-track>div{display:flex;flex-direction:column;gap:3px;min-width:0}
.mini-player-track strong,.mini-player-track span{overflow:hidden;text-overflow:ellipsis;white-space:nowrap;max-width:280px}
.mini-player-track span,.mini-player-actions>span{font-size:12px;color:var(--el-text-color-secondary)}
.music-page{display:flex;flex-direction:column;gap:20px;min-height:calc(100vh - 146px)}.page-heading{display:flex;align-items:flex-start;justify-content:space-between;gap:20px}.page-heading h2{margin:0;font-size:25px}.eyebrow{margin:0 0 9px;color:var(--app-accent);font-size:11px;letter-spacing:.15em}.description,.section-description,.source-copy span,.setting-row p,.hint{color:var(--el-text-color-secondary);font-size:13px}.section-description{margin:0 0 16px}.card-heading{display:flex;align-items:center;justify-content:space-between}.source-list,.setting-list{display:flex;flex-direction:column}.source-row,.setting-row{display:flex;align-items:center;justify-content:space-between;gap:24px;padding:14px 0;border-bottom:1px solid var(--app-border)}.source-row:last-child{border-bottom:0}.source-copy,.track-info{display:flex;flex-direction:column;gap:5px}.source-copy span,.track-info span,.track-info small{font-size:12px;color:var(--el-text-color-secondary)}.setting-row p{margin:6px 0 0}.card-actions{display:flex;justify-content:flex-end;gap:10px;margin-top:20px}.now-playing-list{margin-top:22px;padding-top:18px;border-top:1px solid var(--app-border)}.now-playing-list-header{display:flex;align-items:center;justify-content:space-between;gap:12px;margin-bottom:12px}.now-playing-list-header>div{display:flex;flex-direction:column;gap:4px}.now-playing-list-header strong{font-size:15px}.now-playing-list-header span{font-size:12px;color:var(--el-text-color-secondary)}.now-playing-track-list{display:flex;flex-direction:column;gap:6px}.now-playing-track{display:flex;align-items:center;gap:12px;padding:10px 12px;border:1px solid transparent;border-radius:8px;background:var(--el-fill-color-lighter)}.now-playing-track-active{border-color:var(--app-accent);background:var(--app-bg-control)}.now-playing-track-index{width:28px;color:var(--el-text-color-secondary);font-size:12px;font-variant-numeric:tabular-nums;text-align:center}.now-playing-track-info{display:flex;flex:1;flex-direction:column;gap:3px;min-width:0}.now-playing-track-info strong,.now-playing-track-info span{overflow:hidden;text-overflow:ellipsis;white-space:nowrap}.now-playing-track-info strong{font-size:13px}.now-playing-track-info span{font-size:12px;color:var(--el-text-color-secondary)}.now-playing{display:flex;align-items:center;gap:16px}.track-cover{width:76px;height:76px;border-radius:10px;background:var(--app-bg-control);display:flex;align-items:center;justify-content:center;overflow:hidden;color:var(--el-text-color-secondary);font-size:30px}.track-cover img{width:100%;height:100%;object-fit:cover}.track-info strong{font-size:17px}.time-row{display:flex;justify-content:space-between;color:var(--el-text-color-secondary);font-size:12px;font-variant-numeric:tabular-nums}.player-controls{display:flex;flex-wrap:wrap;justify-content:center;gap:8px;margin:14px 0}.playback-order-row{display:flex;align-items:center;justify-content:center;gap:12px;margin:8px 0 14px}.playback-order-row span{font-size:13px;color:var(--el-text-color-secondary)}.volume-row{display:grid;grid-template-columns:50px 1fr;align-items:center;gap:12px}.volume-row span{font-size:13px;color:var(--el-text-color-secondary)}.hint{margin:12px 0 0}.search-row{display:flex;gap:10px}.lx-import-row{display:flex;gap:10px;align-items:center}.hidden-file-input{display:none}.lx-source-list{display:flex;flex-direction:column;margin-top:16px}.lx-source-row{display:flex;align-items:center;justify-content:space-between;gap:16px;padding:12px 0;border-top:1px solid var(--app-border)}.lx-source-row .source-copy{min-width:0}.lx-source-row .source-copy span{overflow-wrap:anywhere}.lx-search-source-settings{display:flex;flex-direction:column;gap:6px;margin-top:8px}.lx-search-source-checkboxes{display:flex;flex-wrap:wrap;gap:8px 12px}.lx-search-source-toggle{display:inline-flex;align-items:center;gap:7px;padding:4px 9px;border:1px solid var(--app-border);border-radius:6px;background:var(--el-bg-color);color:var(--el-text-color-regular);font:inherit;font-size:13px;cursor:pointer;transition:border-color .15s,background .15s,color .15s}.lx-search-source-toggle:hover{border-color:var(--app-accent)}.lx-search-source-toggle:has(.lx-search-source-checkbox:checked){border-color:var(--app-accent);background:var(--app-bg-control);color:var(--app-accent)}.lx-search-source-checkbox{width:15px;height:15px;margin:0;flex:0 0 15px;accent-color:var(--app-accent);cursor:pointer}.lx-search-priority-list{display:flex;flex-direction:column;align-items:stretch;gap:5px;margin-top:10px;padding:9px 10px;max-width:360px;border:1px solid var(--app-border);border-radius:7px;background:var(--el-fill-color-lighter)}.lx-search-priority-list>strong{font-size:13px}.lx-search-priority-empty{font-size:12px;color:var(--el-text-color-secondary)}.lx-search-priority-row{display:flex;align-items:center;justify-content:space-between;padding:5px 9px;border:1px solid var(--app-border);border-radius:6px;background:var(--el-bg-color)}.lx-search-priority-row strong{font-size:12px}.lx-search-priority-actions{display:flex;gap:6px}
.lx-proxy-fields{display:grid;grid-template-columns:minmax(0,1fr) 180px;gap:16px}.lx-proxy-fields label{display:flex;flex-direction:column;gap:8px;color:var(--el-text-color-regular);font-size:13px}.lx-source-actions{display:flex;align-items:center;gap:8px;flex-shrink:0}.search-summary{margin-top:12px;color:var(--el-text-color-secondary);font-size:12px}.search-pagination{display:flex;justify-content:center;margin-top:18px}.queue-current{padding:10px 12px;margin-bottom:12px;border-radius:8px;background:var(--app-bg-control);font-size:13px}.queue-list{display:flex;flex-direction:column;gap:8px}.queue-item{display:flex;align-items:center;gap:12px;padding:11px 12px;border:1px solid var(--app-border);border-radius:9px;background:var(--el-bg-color);transition:border-color .15s,opacity .15s}.queue-item:hover{border-color:var(--app-accent)}.queue-item-dragging{opacity:.45}.queue-drag-handle{display:flex;align-items:center;color:var(--el-text-color-secondary);cursor:grab;touch-action:none}.queue-drag-handle:active{cursor:grabbing}.queue-index{width:24px;color:var(--el-text-color-secondary);font-size:12px;text-align:center}.queue-track{display:flex;flex:1;flex-direction:column;gap:4px;min-width:0}.queue-track strong,.queue-track span{overflow:hidden;text-overflow:ellipsis;white-space:nowrap}.queue-track span,.queue-requester{color:var(--el-text-color-secondary);font-size:12px}.queue-requester{max-width:120px;overflow:hidden;text-overflow:ellipsis;white-space:nowrap}.setting-row p{margin:6px 0 0}@media(max-width:800px){.music-page{min-height:calc(100vh - 126px)}.mini-player{margin-left:-22px;margin-right:-22px;padding-left:22px;padding-right:22px}.source-row,.setting-row{align-items:flex-start;flex-direction:column}.search-row,.lx-import-row{flex-direction:column;align-items:stretch}.lx-source-row{align-items:flex-start}.mini-player{align-items:flex-start;flex-direction:column}.mini-player-actions{width:100%;justify-content:space-between}}
.replace-track-original,.replace-track-comparison{display:flex;flex-direction:column;gap:6px;padding:12px 14px;margin-bottom:14px;border:1px solid var(--app-border);border-radius:9px;background:var(--el-fill-color-lighter)}.replace-track-original small,.replace-track-comparison small{color:var(--el-text-color-secondary);font-size:12px}.replace-candidate-list{display:flex;flex-direction:column;align-items:stretch;width:100%;gap:4px}.replace-candidate{display:flex;align-items:flex-start;width:100%;height:auto;margin:0;padding:9px 10px;border-bottom:1px solid var(--app-border)}.replace-candidate-main{display:flex;flex-direction:column;gap:4px;min-width:0}.replace-candidate-main small{color:var(--el-text-color-secondary);font-size:12px}</style>