<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import * as VideoRequestService from '../../bindings/github.com/dongguo4812/My-LiveHub/app/internal/desktop/videorequestservice.js'
import { ElMessage } from 'element-plus'

type SearchResult = Awaited<ReturnType<typeof VideoRequestService.Search>>[number]
type QueueSnapshot = Awaited<ReturnType<typeof VideoRequestService.GetQueue>>
type PlaybackSnapshot = Awaited<ReturnType<typeof VideoRequestService.GetPlaybackStatus>>

const keyword = ref('')
const results = ref<SearchResult[]>([])
const queue = ref<QueueSnapshot>({ items: [], capacity: 100 })
const playback = ref<PlaybackSnapshot | null>(null)
const page = ref(1)
const searching = ref(false)
const addingBVID = ref('')
const error = ref('')
const volume = ref(80)
const seekValue = ref(0)
const seeking = ref(false)
let timer: ReturnType<typeof setInterval> | undefined

const isPlaying = computed(() => playback.value?.state === 'playing')
const isPaused = computed(() => playback.value?.state === 'paused')
const progress = computed(() => {
  if (seeking.value) return seekValue.value
  const duration = playback.value?.duration_ms ?? 0
  if (duration <= 0) return 0
  return Math.min(100, Math.max(0, (playback.value?.position_ms ?? 0) / duration * 100))
})
function formatTime(ms?: number) {
  if (!ms || ms < 0) return '00:00'
  const seconds = Math.floor(ms / 1000)
  return `${Math.floor(seconds / 60).toString().padStart(2, '0')}:${(seconds % 60).toString().padStart(2, '0')}`
}
function formatDuration(value: string) {
  return value || '--:--'
}
async function refresh() {
  try {
    queue.value = await VideoRequestService.GetQueue()
    playback.value = await VideoRequestService.GetPlaybackStatus()
    const lastError = await VideoRequestService.GetLastError()
    if (lastError) error.value = lastError
    if (!seeking.value && playback.value.duration_ms > 0) {
      seekValue.value = playback.value.position_ms / playback.value.duration_ms * 100
    }
  } catch (reason) {
    error.value = reason instanceof Error ? reason.message : String(reason)
  }
}
async function search(reset = true) {
  if (!keyword.value.trim()) { ElMessage.warning('请输入视频关键词'); return }
  if (reset) { page.value = 1; results.value = [] }
  searching.value = true
  error.value = ''
  try {
    const found = await VideoRequestService.Search(keyword.value.trim(), page.value)
    results.value = reset ? found : [...results.value, ...found]
    if (!found.length && reset) ElMessage.info('没有找到相关视频')
  } catch (reason) {
    error.value = reason instanceof Error ? reason.message : String(reason)
  } finally { searching.value = false }
}
async function addToQueue(item: SearchResult) {
  addingBVID.value = item.bvid
  try {
    await VideoRequestService.Enqueue(item.bvid, 1, '主播')
    ElMessage.success('已加入视频点播队列')
    await refresh()
  } catch (reason) {
    error.value = reason instanceof Error ? reason.message : String(reason)
  } finally { addingBVID.value = '' }
}
async function removeItem(id: string) {
  try { await VideoRequestService.Remove(id); await refresh() }
  catch (reason) { error.value = String(reason) }
}
async function clearQueue() {
  try { await VideoRequestService.Clear(); await refresh() }
  catch (reason) { error.value = String(reason) }
}
async function nextVideo() {
  try { await VideoRequestService.Next(); await refresh() }
  catch (reason) { error.value = String(reason) }
}
async function stopVideo() {
  try { await VideoRequestService.StopPlayback(); await refresh() }
  catch (reason) { error.value = String(reason) }
}
async function pauseVideo() {
  try { await VideoRequestService.Pause(); await refresh() }
  catch (reason) { error.value = String(reason) }
}
async function resumeVideo() {
  try { await VideoRequestService.Resume(); await refresh() }
  catch (reason) { error.value = String(reason) }
}
async function setVideoVolume(value: number) {
  volume.value = value
  try { await VideoRequestService.SetVolume(value) }
  catch (reason) { error.value = String(reason) }
}
function updateSeekPreview(value: number) { seeking.value = true; seekValue.value = value }
async function seekVideo(value: number) {
  seeking.value = false
  const duration = playback.value?.duration_ms ?? 0
  if (duration <= 0) return
  try { await VideoRequestService.Seek(Math.round(duration * value / 100)) }
  catch (reason) { error.value = String(reason) }
  await refresh()
}
function loadMore() {
  page.value += 1
  void search(false)
}
onMounted(() => {
  void refresh()
  timer = setInterval(() => void refresh(), 1000)
})
onUnmounted(() => { if (timer) clearInterval(timer) })
</script>

<template>
  <section class="video-request-page">
    <header class="page-heading">
      <div><p class="eyebrow">BILIBILI VIDEO REQUEST</p><h2>B 站视频点播</h2><p class="description">搜索 B 站视频、管理点播队列，并通过独立视频窗口播放。</p></div>
      <el-tag :type="isPlaying ? 'success' : isPaused ? 'warning' : 'info'">{{ isPlaying ? '正在播放' : isPaused ? '已暂停' : '未播放' }}</el-tag>
    </header>

    <el-alert v-if="error" :title="error" type="error" show-icon :closable="true" @close="error = ''" />

    <el-card shadow="never">
      <template #header><strong>搜索 B 站视频</strong></template>
      <div class="search-row">
        <el-input v-model="keyword" placeholder="输入视频标题、UP 主或关键词" clearable @keyup.enter="search()" />
        <el-button type="primary" :loading="searching" @click="search()">搜索</el-button>
      </div>
      <el-empty v-if="!results.length && !searching" description="搜索结果会显示在这里" :image-size="76" />
      <div v-else class="results-list">
        <article v-for="item in results" :key="item.bvid" class="video-result">
          <img :src="item.cover" :alt="item.title" loading="lazy" referrerpolicy="no-referrer" />
          <div class="result-info">
            <strong>{{ item.title }}</strong>
            <span>{{ item.author }} · {{ formatDuration(item.duration) }}</span>
            <small>{{ item.bvid }} · 播放 {{ item.play_count.toLocaleString() }} · 弹幕 {{ item.danmaku_count.toLocaleString() }}</small>
          </div>
          <el-button type="primary" plain :loading="addingBVID === item.bvid" @click="addToQueue(item)">加入队列</el-button>
        </article>
      </div>
      <div v-if="results.length" class="load-more"><el-button :loading="searching" @click="loadMore">加载更多</el-button></div>
    </el-card>

    <el-card shadow="never">
      <template #header><div class="card-heading"><strong>当前播放</strong><el-tag effect="plain">{{ queue.current ? '1 条正在播放' : '暂无视频' }}</el-tag></div></template>
      <div v-if="queue.current" class="current-video">
        <img :src="queue.current.cover" :alt="queue.current.title" referrerpolicy="no-referrer" />
        <div class="current-info"><strong>{{ queue.current.title }}</strong><span>{{ queue.current.part_title || 'P' + queue.current.page }} · {{ queue.current.author }}</span><small>点播人：{{ queue.current.requested_by }}</small></div>
      </div>
      <el-empty v-else description="当前没有正在播放的视频" :image-size="64" />
      <div class="time-row"><span>{{ formatTime(playback?.position_ms) }}</span><span>{{ formatTime(playback?.duration_ms) }}</span></div>
      <el-slider :model-value="progress" :max="100" :disabled="!playback || playback.duration_ms <= 0" @input="updateSeekPreview" @change="seekVideo" />
      <div class="control-row">
        <el-button type="primary" :disabled="!isPaused" @click="resumeVideo">继续</el-button>
        <el-button :disabled="!isPlaying" @click="pauseVideo">暂停</el-button>
        <el-button @click="nextVideo">下一条</el-button>
        <el-button type="danger" plain @click="stopVideo">停止</el-button>
        <span class="volume-label">音量</span><el-slider class="volume-slider" v-model="volume" :min="0" :max="100" @change="setVideoVolume" />
      </div>
    </el-card>

    <el-card shadow="never">
      <template #header><div class="card-heading"><strong>点播队列</strong><div><el-tag effect="plain">{{ queue.items.length }} / {{ queue.capacity }}</el-tag><el-button link type="danger" :disabled="!queue.items.length" @click="clearQueue">清空队列</el-button></div></div></template>
      <el-empty v-if="!queue.items.length" description="还没有观众点播的视频" :image-size="64" />
      <div v-else class="queue-list">
        <article v-for="(item, index) in queue.items" :key="item.id" class="queue-item">
          <span class="queue-index">{{ index + 1 }}</span>
          <img :src="item.cover" :alt="item.title" referrerpolicy="no-referrer" />
          <div class="queue-info"><strong>{{ item.title }}</strong><span>{{ item.part_title || 'P' + item.page }} · {{ item.author }} · {{ item.requested_by }}</span></div>
          <el-button link type="danger" @click="removeItem(item.id)">移除</el-button>
        </article>
      </div>
    </el-card>

    <el-card shadow="never" class="command-card">
      <strong>弹幕点播指令</strong>
      <p>观众发送 <code>点播 BV号</code>、B 站视频链接，或 <code>点播 视频关键词</code>，系统会自动搜索并将匹配视频加入队列。</p>
      <p>主播或房管发送 <code>切视频</code> 可跳过当前视频。队列中的视频会自动接续播放。</p>
      <p class="muted">需要先在「平台管理」完成 B 站账号登录，并连接直播间。720P 及以上画质受 B 站账号权限限制。</p>
    </el-card>
  </section>
</template>

<style scoped>
.video-request-page{display:flex;flex-direction:column;gap:18px}.page-heading{display:flex;justify-content:space-between;align-items:flex-start;gap:20px}.page-heading h2{margin:0;font-size:25px}.eyebrow{margin:0 0 8px;color:var(--el-color-primary);font-size:11px;letter-spacing:.15em}.description,.muted{color:var(--el-text-color-secondary);font-size:13px}.search-row{display:flex;gap:10px}.results-list,.queue-list{display:flex;flex-direction:column;gap:10px;margin-top:16px}.video-result,.queue-item,.current-video{display:flex;align-items:center;gap:14px;padding:10px;border:1px solid var(--el-border-color-lighter);border-radius:10px}.video-result>img,.queue-item>img,.current-video>img{width:144px;height:82px;object-fit:cover;border-radius:7px;background:var(--el-fill-color)}.result-info,.queue-info,.current-info{display:flex;flex:1;min-width:0;flex-direction:column;gap:5px}.result-info strong,.queue-info strong,.current-info strong{font-size:14px;overflow:hidden;text-overflow:ellipsis;white-space:nowrap}.result-info span,.queue-info span,.current-info span,.result-info small,.queue-info small,.current-info small{color:var(--el-text-color-secondary);font-size:12px}.load-more{text-align:center;margin-top:14px}.card-heading{display:flex;align-items:center;justify-content:space-between}.card-heading>div{display:flex;align-items:center;gap:10px}.current-video{margin-bottom:16px}.current-video>img{width:180px;height:102px}.time-row{display:flex;justify-content:space-between;color:var(--el-text-color-secondary);font-size:12px;font-variant-numeric:tabular-nums}.control-row{display:flex;align-items:center;gap:8px;flex-wrap:wrap}.volume-label{margin-left:auto;color:var(--el-text-color-secondary);font-size:13px}.volume-slider{width:140px}.queue-index{width:22px;text-align:center;color:var(--el-text-color-secondary);font-variant-numeric:tabular-nums}.queue-item>img{width:112px;height:64px}.command-card p{margin:8px 0;color:var(--el-text-color-secondary);font-size:13px;line-height:1.6}.command-card code{color:var(--el-color-primary);font-weight:600}
</style>
