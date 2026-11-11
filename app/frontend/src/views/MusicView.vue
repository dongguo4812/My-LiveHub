<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { Headset } from '@element-plus/icons-vue'
import * as MusicService from '../../bindings/github.com/dongguo4812/My-LiveHub/app/internal/desktop/musicservice.js'

type MusicConfig = Awaited<ReturnType<typeof MusicService.GetConfig>>
type Track = Awaited<ReturnType<typeof MusicService.Search>>['tracks'][number]
type QueueSnapshot = Awaited<ReturnType<typeof MusicService.GetQueue>>
type PlaybackSnapshot = Awaited<ReturnType<typeof MusicService.GetPlaybackStatus>>

const config = ref<MusicConfig | null>(null)
const availableSourceIDs = ref<string[]>([])
const queue = ref<QueueSnapshot | null>(null)
const playback = ref<PlaybackSnapshot | null>(null)
const results = ref<Track[]>([])
const searchErrors = ref<Record<string, string>>({})
const query = ref('')
const volume = ref(80)
const loading = ref(false)
const searching = ref(false)
const currentPage = ref(1)
const maxPage = ref(0)
const total = ref(0)
const pageSize = ref(20)
let searchRequestID = 0
const saving = ref(false)
const error = ref('')
const success = ref('')
let statusTimer: ReturnType<typeof setInterval> | undefined

const sourceNames: Record<string, string> = {
  netease: '网易云音乐',
  qq_music: 'QQ 音乐',
  kugou: '酷狗音乐',
  kuwo: '酷我音乐',
}
const sources = computed(() => Object.entries(config.value?.sources ?? {}).map(([id, enabled]) => ({
  id, name: sourceNames[id] ?? id, enabled,
  status: availableSourceIDs.value.includes(id) ? '适配器已接入' : '适配器尚未接入',
})))
const enabledCount = computed(() => sources.value.filter(source => source.enabled).length)
const canSave = computed(() => enabledCount.value > 0)
const progress = computed(() => {
  if (!playback.value || playback.value.duration_ms <= 0) return 0
  return Math.min(100, Math.max(0, playback.value.position_ms / playback.value.duration_ms * 100))
})
const searchErrorText = computed(() => Object.entries(searchErrors.value)
  .map(([id, message]) => `${sourceNames[id] ?? id}：${message}`).join('；'))

function formatDuration(value?: number) {
  if (!value || value < 0) return '--:--'
  const seconds = Math.floor(value / 1000)
  return `${Math.floor(seconds / 60).toString().padStart(2, '0')}:${(seconds % 60).toString().padStart(2, '0')}`
}
function sourceName(id?: string) { return sourceNames[id ?? ''] ?? id ?? '未知音源' }
function trackRowKey(row: Track) { return `${row.source_id}:${row.id}` }

async function loadConfig() {
  loading.value = true
  error.value = ''
  try {
    config.value = await MusicService.GetConfig()
    availableSourceIDs.value = (await MusicService.GetSources()).map(source => source.id)
    await refreshQueue()
  } catch (reason) {
    error.value = reason instanceof Error ? reason.message : String(reason)
  } finally {
    loading.value = false
  }
}
async function refreshQueue() {
  try { queue.value = await MusicService.GetQueue() } catch { /* Keep the last known queue. */ }
}
async function refreshPlayback() {
  try {
    playback.value = await MusicService.GetPlaybackStatus()
    if (playback.value.volume >= 0) volume.value = playback.value.volume
  } catch { /* The engine is not started until the first play request. */ }
  await refreshQueue()
}
async function saveConfig() {
  if (!config.value) return
  if (!canSave.value) { error.value = '至少需要启用一个音乐音源'; return }
  saving.value = true
  error.value = ''
  success.value = ''
  try {
    await MusicService.SaveConfig(config.value)
    success.value = '音乐设置已保存'
    config.value = await MusicService.GetConfig()
    availableSourceIDs.value = (await MusicService.GetSources()).map(source => source.id)
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
    const response = await MusicService.SearchPage(query.value, page)
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
    await MusicService.Enqueue(track, '')
    success.value = `已加入队列：${track.title}`
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
        <h2>音乐播放器</h2>
        <p class="description">一个 My-LiveHub 播放器，多个可同时启用的音乐音源。搜索结果标注来源，实际播放统一交给 My-LiveHub 的 mpv 播放引擎。</p>
        <el-alert v-if="config && !config.enabled" title="音乐模块当前已关闭：无法加入歌曲队列或播放。请在下方「点歌设置」中启用音乐模块并保存全部设置。" type="warning" :closable="false" show-icon style="margin-top:12px" />
      </div>
      <el-tag type="info" effect="plain">首期开发中</el-tag>
    </header>

    <el-alert v-if="error" :title="error" type="error" :closable="true" show-icon @close="error = ''" />
    <el-alert v-if="success" :title="success" type="success" :closable="true" show-icon @close="success = ''" />

    <el-card shadow="never">
      <template #header><div class="card-heading"><strong>正在播放</strong><el-tag :type="playback?.state === 'playing' ? 'success' : 'info'">{{ playback?.state ?? '播放器未启动' }}</el-tag></div></template>
      <div class="now-playing">
        <div class="track-cover">
          <img v-if="playback?.track?.cover_url" :src="playback.track.cover_url" alt="歌曲封面" />
          <el-icon v-else><Headset /></el-icon>
        </div>
        <div class="track-info">
          <strong>{{ playback?.track?.title ?? '尚未播放歌曲' }}</strong>
          <span>{{ playback?.track?.artist ?? '搜索歌曲并加入播放队列' }}</span>
          <small v-if="playback?.track">{{ sourceName(playback.track.source_id) }}</small>
        </div>
      </div>
      <el-slider :model-value="progress" :disabled="!playback?.track || !playback.duration_ms" @change="seekTo" />
      <div class="time-row"><span>{{ formatDuration(playback?.position_ms) }}</span><span>{{ formatDuration(playback?.duration_ms) }}</span></div>
      <div class="player-controls">
        <el-button @click="playNext">播放队列</el-button>
        <el-button :disabled="playback?.state !== 'playing'" @click="pause">暂停</el-button>
        <el-button :disabled="playback?.state !== 'paused'" @click="resume">继续</el-button>
        <el-button @click="previousTrack">上一首</el-button>
        <el-button @click="nextTrack">下一首</el-button>
        <el-button @click="stop">停止</el-button>
      </div>
      <div class="volume-row"><span>音量</span><el-slider v-model="volume" :min="0" :max="100" @change="setVolume" /></div>
      <p class="hint">首次播放前需要在 Windows 中安装 mpv，并确保 mpv.exe 已加入 PATH。</p>
    </el-card>

    <el-card shadow="never" v-loading="loading">
      <template #header><div class="card-heading"><strong>音源管理</strong><el-tag :type="enabledCount > 0 ? 'success' : 'danger'" effect="plain">已启用 {{ enabledCount }} 个</el-tag></div></template>
      <p class="section-description">每个音源独立启停，可以同时勾选多个。搜索会并行查询所有已启用且已接入的音源。</p>
      <div v-if="config" class="source-list">
        <div v-for="source in sources" :key="source.id" class="source-row">
          <div class="source-copy"><strong>{{ source.name }}</strong><span>{{ source.status }}</span></div>
          <el-checkbox v-model="config.sources[source.id]">启用</el-checkbox>
        </div>
      </div>
      <div class="card-actions"><el-button type="primary" :loading="saving" :disabled="!canSave" @click="saveConfig">保存音源设置</el-button></div>
    </el-card>

    <el-card shadow="never">
      <template #header><strong>搜索歌曲</strong></template>
      <div class="search-row">
        <el-input v-model="query" placeholder="输入歌曲名或歌手，例如：稻香 周杰伦" clearable @keyup.enter="search" />
        <el-button type="primary" :loading="searching" :disabled="enabledCount === 0" @click="search">搜索</el-button>
      </div>
      <el-alert v-if="searchErrorText" :title="searchErrorText" type="warning" :closable="false" show-icon />
      <div v-if="total > 0" class="search-summary">共 {{ total }} 条结果 · 第 {{ currentPage }} / {{ maxPage || 1 }} 页</div>
      <el-table v-if="results.length" :data="results" :row-key="trackRowKey" style="width:100%;margin-top:14px">
        <el-table-column prop="title" label="歌曲" min-width="180" />
        <el-table-column prop="artist" label="歌手" min-width="140" />
        <el-table-column prop="album" label="专辑" min-width="140" />
        <el-table-column label="音源" width="130"><template #default="scope">{{ sourceName(scope.row.source_id) }}</template></el-table-column>
        <el-table-column label="时长" width="90"><template #default="scope">{{ formatDuration(scope.row.duration_ms) }}</template></el-table-column>
        <el-table-column label="操作" width="110" fixed="right"><template #default="scope"><el-button link type="primary" @click="addToQueue(scope.row)">加入队列</el-button></template></el-table-column>
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

    <el-card shadow="never">
      <template #header><div class="card-heading"><strong>播放队列</strong><el-tag effect="plain">{{ queue?.items.length ?? 0 }} 首待播</el-tag></div></template>
      <div v-if="queue?.current" class="queue-current">当前：{{ queue.current.track.title }} · {{ queue.current.track.artist }}</div>
      <el-table v-if="queue?.items.length" :data="queue.items" row-key="id" style="width:100%">
        <el-table-column type="index" label="#" width="55" />
        <el-table-column prop="track.title" label="歌曲" min-width="180" />
        <el-table-column prop="track.artist" label="歌手" min-width="140" />
        <el-table-column label="音源" width="130"><template #default="scope">{{ sourceName(scope.row.track.source_id) }}</template></el-table-column>
        <el-table-column prop="requested_by" label="点歌用户" width="130" />
      </el-table>
      <el-empty v-else description="队列暂时为空" :image-size="76" />
    </el-card>

    <el-card shadow="never">
      <template #header><strong>点歌设置</strong></template>
      <div v-if="config" class="setting-list">
        <div class="setting-row"><div><strong>启用音乐模块</strong><p>关闭后无法加入歌曲队列或播放歌曲，也不会处理弹幕点歌；MV 视频播放不受影响。</p></div><el-switch v-model="config.enabled" /></div>
        <el-divider />
        <div class="setting-row"><div><strong>允许弹幕点歌</strong><p>收到点歌指令后搜索已启用的音源并加入统一队列。</p></div><el-switch v-model="config.command_enabled" /></div>
        <el-divider />
        <div class="setting-row"><div><strong>点歌指令前缀</strong><p>例如：点歌 稻香 周杰伦。</p></div><el-input v-model="config.command_prefix" maxlength="16" style="width:220px" /></div>
        <el-divider />
        <div class="setting-row"><div><strong>播放队列容量</strong><p>统一队列允许等待的歌曲数量。</p></div><el-input-number v-model="config.queue_capacity" :min="1" :max="1000" /></div>
        <el-divider />
        <div class="setting-row"><div><strong>每个音源搜索数量</strong><p>各音源单次搜索的候选数量上限。</p></div><el-input-number v-model="config.search_limit" :min="1" :max="50" /></div>
      </div>
      <div class="card-actions"><el-button type="primary" :loading="saving" :disabled="!config || !canSave" @click="saveConfig">保存全部设置</el-button></div>
    </el-card>
  </section>
</template>


<style scoped>
.music-page{display:flex;flex-direction:column;gap:20px}.page-heading{display:flex;align-items:flex-start;justify-content:space-between;gap:20px}.page-heading h2{margin:0;font-size:25px}.eyebrow{margin:0 0 9px;color:var(--app-accent);font-size:11px;letter-spacing:.15em}.description,.section-description,.source-copy span,.setting-row p,.hint{color:var(--el-text-color-secondary);font-size:13px}.section-description{margin:0 0 16px}.card-heading{display:flex;align-items:center;justify-content:space-between}.source-list,.setting-list{display:flex;flex-direction:column}.source-row,.setting-row{display:flex;align-items:center;justify-content:space-between;gap:24px;padding:14px 0;border-bottom:1px solid var(--app-border)}.source-row:last-child{border-bottom:0}.source-copy,.track-info{display:flex;flex-direction:column;gap:5px}.source-copy span,.track-info span,.track-info small{font-size:12px;color:var(--el-text-color-secondary)}.setting-row p{margin:6px 0 0}.card-actions{display:flex;justify-content:flex-end;gap:10px;margin-top:20px}.now-playing{display:flex;align-items:center;gap:16px}.track-cover{width:76px;height:76px;border-radius:10px;background:var(--app-bg-control);display:flex;align-items:center;justify-content:center;overflow:hidden;color:var(--el-text-color-secondary);font-size:30px}.track-cover img{width:100%;height:100%;object-fit:cover}.track-info strong{font-size:17px}.time-row{display:flex;justify-content:space-between;color:var(--el-text-color-secondary);font-size:12px;font-variant-numeric:tabular-nums}.player-controls{display:flex;flex-wrap:wrap;justify-content:center;gap:8px;margin:14px 0}.volume-row{display:grid;grid-template-columns:50px 1fr;align-items:center;gap:12px}.volume-row span{font-size:13px;color:var(--el-text-color-secondary)}.hint{margin:12px 0 0}.search-row{display:flex;gap:10px}.search-summary{margin-top:12px;color:var(--el-text-color-secondary);font-size:12px}.search-pagination{display:flex;justify-content:center;margin-top:18px}.queue-current{padding:10px 12px;margin-bottom:12px;border-radius:8px;background:var(--app-bg-control);font-size:13px}.setting-row p{margin:6px 0 0}@media(max-width:800px){.source-row,.setting-row{align-items:flex-start;flex-direction:column}.search-row{flex-direction:column}}
</style>