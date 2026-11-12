<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { Events } from '@wailsio/runtime'
import * as QueueService from '../../bindings/github.com/dongguo4812/My-LiveHub/app/internal/desktop/videoqueueservice.js'
import * as WebViewService from '../../bindings/github.com/dongguo4812/My-LiveHub/app/internal/desktop/bilibiliwebviewservice.js'

interface QueueItem {
  id: number
  bvid: string
  title: string
  duration_seconds: number
  requester: string
  user_id: number
  added_at: number
}
interface PlaybackState {
  is_open: boolean
  title: string
  position_ms: number
  duration_ms: number
  is_playing: boolean
  is_paused: boolean
  is_ended: boolean
}
interface QueueSnapshot {
  enabled: boolean
  current?: QueueItem
  items: QueueItem[]
  queue_count: number
  playback: PlaybackState
}
interface VideoSettings {
  room_id: number
  video_requests_enabled: boolean
  video_request_cooldown_seconds: number
  video_queue_limit: number
  video_max_duration_seconds: number
  video_require_fan_medal: boolean
  video_queue_display_limit: number
  video_window_x: number
  video_window_y: number
  video_window_width: number
  video_window_height: number
  video_window_bounds_saved: boolean
}
const snapshot = ref<QueueSnapshot>({
  enabled: true, items: [], queue_count: 0,
  playback: { is_open: false, title: '', position_ms: 0, duration_ms: 0, is_playing: false, is_paused: false, is_ended: false },
})
const settings = ref<VideoSettings>({
  room_id: 0, video_requests_enabled: true, video_request_cooldown_seconds: 60,
  video_queue_limit: 30, video_max_duration_seconds: 300, video_require_fan_medal: false, video_queue_display_limit: 10,
  video_window_x: 0, video_window_y: 0, video_window_width: 1280, video_window_height: 800, video_window_bounds_saved: false,
})
const activeTab = ref<'queue'|'settings'>('queue')
const bvid = ref('')
const busy = ref(false)
const saving = ref(false)
const error = ref('')
const success = ref('')
let timer: ReturnType<typeof setInterval> | undefined
let unsubscribe: (() => void) | undefined
const visibleItems = computed(() => snapshot.value.items.slice(0, settings.value.video_queue_display_limit))
const statusText = computed(() => {
  if (!snapshot.value.playback.is_open) return snapshot.value.current ? '正在打开' : '未播放'
  if (snapshot.value.playback.is_ended) return '已结束'
  if (snapshot.value.playback.is_playing) return '正在播放'
  if (snapshot.value.playback.is_paused) return '已暂停'
  return '等待视频加载'
})
const progress = computed(() => {
  const p = snapshot.value.playback
  return p.duration_ms > 0 ? Math.min(100, Math.max(0, p.position_ms / p.duration_ms * 100)) : 0
})
function formatTime(seconds: number) {
  const value = Math.max(0, Math.floor(seconds || 0))
  return `${Math.floor(value / 60).toString().padStart(2, '0')}:${(value % 60).toString().padStart(2, '0')}`
}
function formatMS(ms: number) { return formatTime(Math.floor((ms || 0) / 1000)) }
async function refresh() {
  try { snapshot.value = await QueueService.GetSnapshot() as QueueSnapshot } catch {}
}
async function loadSettings() {
  try { settings.value = await QueueService.GetSettings() as VideoSettings } catch (reason) {
    error.value = reason instanceof Error ? reason.message : String(reason)
  }
}
async function addVideo() {
  if (!bvid.value.trim()) { error.value = '请输入 B 站 BV 号'; return }
  busy.value = true; error.value = ''; success.value = ''
  try {
    await QueueService.AddVideo(bvid.value.trim(), '主播', 0)
    bvid.value = ''
    success.value = '视频已加入点播队列'
    await refresh()
  } catch (reason) { error.value = reason instanceof Error ? reason.message : String(reason) }
  finally { busy.value = false }
}
async function skipCurrent() {
  try { await QueueService.SkipCurrent(); await refresh() }
  catch (reason) { error.value = reason instanceof Error ? reason.message : String(reason) }
}
async function removeItem(id: number) {
  try { await QueueService.RemoveQueued(id); await refresh() }
  catch (reason) { error.value = reason instanceof Error ? reason.message : String(reason) }
}
async function clearQueue() {
  try { await QueueService.ClearQueue(); await refresh(); success.value = '待播队列已清空' }
  catch (reason) { error.value = reason instanceof Error ? reason.message : String(reason) }
}
async function toggleEnabled(value: boolean) {
  settings.value.video_requests_enabled = value
  await saveSettings()
}
async function saveSettings() {
  saving.value = true; error.value = ''; success.value = ''
  try {
    settings.value = await QueueService.UpdateSettings(settings.value) as VideoSettings
    success.value = '视频点播规则已保存'
    await refresh()
  } catch (reason) { error.value = reason instanceof Error ? reason.message : String(reason) }
  finally { saving.value = false }
}
async function fullscreen() {
  try { await WebViewService.EnterWebFullscreen() }
  catch (reason) { error.value = reason instanceof Error ? reason.message : String(reason) }
}
async function replay() {
  try { await WebViewService.ReplayFromStart() }
  catch (reason) { error.value = reason instanceof Error ? reason.message : String(reason) }
}
async function closePlayer() {
  try { await WebViewService.CloseWindow(); await refresh() }
  catch (reason) { error.value = reason instanceof Error ? reason.message : String(reason) }
}
onMounted(() => {
  unsubscribe = Events.On('bilibili-player-state', () => void refresh())
  void refresh(); void loadSettings()
  timer = setInterval(() => void refresh(), 1000)
})
onUnmounted(() => { unsubscribe?.(); if (timer) clearInterval(timer) })
</script>

<template>
  <section class="video-page">
    <header class="page-heading">
      <div>
        <p class="eyebrow">BILIBILI VIDEO REQUEST</p>
        <h2>视频点播</h2>
        <p class="description">使用 B 站原生网页播放器，观众通过直播间弹幕提交 BV 号。</p>
      </div>
      <div class="heading-actions">
        <el-tag :type="snapshot.playback.is_playing ? 'success' : snapshot.current ? 'warning' : 'info'">
          {{ statusText }}
        </el-tag>
        <el-button :type="activeTab === 'queue' ? 'primary' : 'default'" @click="activeTab = 'queue'">点播队列</el-button>
        <el-button :type="activeTab === 'settings' ? 'primary' : 'default'" @click="activeTab = 'settings'">设置</el-button>
      </div>
    </header>

    <el-alert v-if="error" :title="error" type="error" :closable="true" show-icon @close="error = ''" />
    <el-alert v-if="success" :title="success" type="success" :closable="true" show-icon @close="success = ''" />

    <template v-if="activeTab === 'queue'">
      <el-card shadow="never">
        <template #header>
          <div class="card-heading"><strong>当前播放</strong><el-tag :type="snapshot.current ? 'success' : 'info'">{{ snapshot.current ? '播放中' : '空闲' }}</el-tag></div>
        </template>
        <template v-if="snapshot.current">
          <div class="current-title">{{ snapshot.current.title || snapshot.current.bvid }}</div>
          <div class="current-meta">
            <span>{{ snapshot.current.bvid }}</span><span>点播人：{{ snapshot.current.requester }}</span><span>{{ formatTime(snapshot.current.duration_seconds) }}</span>
          </div>
          <div class="time-row"><span>{{ formatMS(snapshot.playback.position_ms) }}</span><span>{{ formatMS(snapshot.playback.duration_ms) }}</span></div>
          <el-progress :percentage="progress" :show-text="false" :stroke-width="8" />
          <div class="actions player-actions">
            <el-button type="primary" @click="fullscreen">B站网页全屏</el-button>
            <el-button @click="replay">从头播放</el-button>
            <el-button @click="skipCurrent">跳过当前</el-button>
            <el-button type="danger" plain @click="closePlayer">结束点播</el-button>
          </div>
        </template>
        <el-empty v-else description="当前没有正在播放的视频" :image-size="72" />
      </el-card>

      <el-card shadow="never">
        <template #header>
          <div class="card-heading"><strong>待播队列</strong><div class="queue-actions"><el-tag>{{ snapshot.queue_count }} / {{ settings.video_queue_limit }}</el-tag><el-button size="small" type="danger" plain :disabled="snapshot.queue_count === 0" @click="clearQueue">清空队列</el-button></div></div>
        </template>
        <div class="manual-add">
          <el-input v-model="bvid" placeholder="输入 BV 号，主播手动加入队列" clearable @keyup.enter="addVideo" />
          <el-button type="primary" :loading="busy" @click="addVideo">加入队列</el-button>
        </div>
        <el-table v-if="visibleItems.length" :data="visibleItems" row-key="id" style="width:100%">
          <el-table-column type="index" label="#" width="56" />
          <el-table-column label="视频" min-width="220">
            <template #default="{row}"><div class="video-name">{{ row.title }}</div><div class="video-sub">{{ row.bvid }}</div></template>
          </el-table-column>
          <el-table-column prop="requester" label="点播人" width="120" />
          <el-table-column label="时长" width="90"><template #default="{row}">{{ formatTime(row.duration_seconds) }}</template></el-table-column>
          <el-table-column label="操作" width="90" align="right"><template #default="{row}"><el-button link type="danger" @click="removeItem(row.id)">删除</el-button></template></el-table-column>
        </el-table>
        <el-empty v-else description="队列暂无待播视频" :image-size="72" />
        <p v-if="snapshot.queue_count > visibleItems.length" class="hint">还有 {{ snapshot.queue_count - visibleItems.length }} 条视频未显示，可在设置中调整展示数量。</p>
      </el-card>
    </template>

    <template v-else>
      <el-card shadow="never">
        <template #header><strong>视频点播规则</strong></template>
        <el-form label-position="top" class="settings-form">
          <el-form-item label="允许观众点播">
            <el-switch v-model="settings.video_requests_enabled" active-text="开启" inactive-text="关闭" />
          </el-form-item>
          <el-form-item label="普通观众点播冷却时间（秒）">
            <el-input-number v-model="settings.video_request_cooldown_seconds" :min="0" :max="3600" />
            <div class="field-hint">0 表示不限制；主播和房管不受冷却限制。</div>
          </el-form-item>
          <el-form-item label="待播队列上限">
            <el-input-number v-model="settings.video_queue_limit" :min="1" :max="1000" />
          </el-form-item>
          <el-form-item label="普通观众单个视频最长时长（秒）">
            <el-input-number v-model="settings.video_max_duration_seconds" :min="1" :max="3600" />
            <div class="field-hint">主播和房管不受时长限制。</div>
          </el-form-item>
          <el-form-item label="粉丝勋章要求">
            <el-switch v-model="settings.video_require_fan_medal" active-text="要求佩戴粉丝勋章" inactive-text="不要求" />
          </el-form-item>
          <el-form-item label="队列页面显示数量">
            <el-input-number v-model="settings.video_queue_display_limit" :min="1" :max="100" />
          </el-form-item>
        </el-form>
        <div class="actions"><el-button type="primary" :loading="saving" @click="saveSettings">保存设置</el-button></div>
        <p class="hint">弹幕指令：发送「点播 BV号」加入队列；主播或房管发送「下一个」跳过当前视频，发送「清空视频队列」清空待播列表。</p>
      </el-card>
    </template>
  </section>
</template>

<style scoped>
.video-page{display:flex;flex-direction:column;gap:18px}.page-heading{display:flex;align-items:flex-start;justify-content:space-between;gap:18px}.page-heading h2{margin:0;font-size:25px}.eyebrow{margin:0 0 9px;color:var(--app-accent);font-size:11px;letter-spacing:.15em}.description,.hint,.field-hint{color:var(--el-text-color-secondary);font-size:13px}.heading-actions,.actions,.card-heading,.queue-actions{display:flex;align-items:center;gap:9px;flex-wrap:wrap}.card-heading{justify-content:space-between}.current-title{font-size:18px;font-weight:650;margin-bottom:9px}.current-meta{display:flex;gap:16px;flex-wrap:wrap;color:var(--el-text-color-secondary);font-size:12px;margin-bottom:16px}.time-row{display:flex;justify-content:space-between;color:var(--el-text-color-secondary);font-size:12px;font-variant-numeric:tabular-nums;margin:14px 0 8px}.player-actions{margin-top:16px}.manual-add{display:flex;gap:10px;margin-bottom:14px}.manual-add .el-input{max-width:460px}.video-name{font-weight:600}.video-sub{font-size:12px;color:var(--el-text-color-secondary);margin-top:4px}.hint{margin:12px 0 0}.settings-form{max-width:560px}.field-hint{margin-top:4px}.settings-form :deep(.el-input-number){width:180px}
@media(max-width:760px){.page-heading{flex-direction:column}.manual-add{flex-direction:column}.manual-add .el-input{max-width:none}}
</style>
