<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import * as VideoService from '../../bindings/github.com/dongguo4812/My-LiveHub/app/internal/desktop/videoservice.js'
import * as BilibiliVideoService from '../../bindings/github.com/dongguo4812/My-LiveHub/app/internal/desktop/bilibilivideoservice.js'

type PlaybackSnapshot = Awaited<ReturnType<typeof VideoService.GetPlaybackStatus>>
const bvid = ref('')
const url = ref('')
const title = ref('')
const playback = ref<PlaybackSnapshot | null>(null)
const volume = ref(80)
const seekValue = ref(0)
const isSeeking = ref(false)
const busy = ref(false)
const error = ref('')
let timer: ReturnType<typeof setInterval> | undefined

const progress = computed(() => {
  if (!playback.value || playback.value.duration_ms <= 0) return 0
  return Math.min(100, Math.max(0, playback.value.position_ms / playback.value.duration_ms * 100))
})
const isPlaying = computed(() => playback.value?.state === 'playing')
const isPaused = computed(() => playback.value?.state === 'paused')
const actualResolution = computed(() => {
  const width = playback.value?.video_width ?? 0
  const height = playback.value?.video_height ?? 0
  return width > 0 && height > 0 ? `${width} × ${height}` : '等待视频解码'
})
function formatTime(ms?: number) {
  if (!ms || ms < 0) return '00:00'
  const seconds = Math.floor(ms / 1000)
  return `${Math.floor(seconds / 60).toString().padStart(2, '0')}:${(seconds % 60).toString().padStart(2, '0')}`
}
async function refresh() {
  try {
    playback.value = await VideoService.GetPlaybackStatus()
    if (playback.value.volume >= 0) volume.value = playback.value.volume
    // Do not let the polling loop overwrite the user's in-progress seek.
    if (!isSeeking.value) seekValue.value = progress.value * 10
  } catch { /* The video process may not have been started yet. */ }
}
async function playBilibili() {
  if (!bvid.value.trim()) { error.value = '请输入 B 站 BV 号'; return }
  busy.value = true
  error.value = ''
  try {
    await BilibiliVideoService.PlayBilibiliVideo(bvid.value.trim())
    await refresh()
  } catch (reason) {
    error.value = reason instanceof Error ? reason.message : String(reason)
  } finally { busy.value = false }
}
async function play() {
  if (!url.value.trim()) { error.value = '请输入视频直链地址'; return }
  busy.value = true
  error.value = ''
  try {
    await VideoService.Play(url.value.trim(), title.value.trim())
    await refresh()
  } catch (reason) {
    error.value = reason instanceof Error ? reason.message : String(reason)
  } finally { busy.value = false }
}
async function pause() { try { await VideoService.Pause(); await refresh() } catch (reason) { error.value = String(reason) } }
async function resume() { try { await VideoService.Resume(); await refresh() } catch (reason) { error.value = String(reason) } }
async function stop() {
  try {
    await VideoService.Stop()
    isSeeking.value = false
    await refresh()
  } catch (reason) { error.value = String(reason) }
}
function updateSeekValue(value: number | number[]) {
  const next = Array.isArray(value) ? value[0] : value
  isSeeking.value = true
  seekValue.value = next
}
async function seek(value: number | number[]) {
  const next = Array.isArray(value) ? value[0] : value
  if (!playback.value || playback.value.duration_ms <= 0) {
    isSeeking.value = false
    return
  }
  seekValue.value = next
  try {
    await VideoService.Seek(Math.round(playback.value.duration_ms * next / 1000))
  } catch (reason) {
    error.value = String(reason)
  } finally {
    isSeeking.value = false
    await refresh()
  }
}
async function setVolume(value: number | number[]) {
  const next = Array.isArray(value) ? value[0] : value
  try { await VideoService.SetVolume(next); volume.value = next }
  catch (reason) { error.value = String(reason) }
}
onMounted(() => {
  void refresh()
  timer = setInterval(() => void refresh(), 700)
})
onUnmounted(() => { if (timer) clearInterval(timer) })
</script>

<template>
  <section class="video-page">
    <header class="page-heading">
      <div>
        <p class="eyebrow">VIDEO OUTPUT</p>
        <h2>MV 播放器</h2>
        <p class="description">使用独立 mpv 视频进程输出纯画面窗口，可在 OBS 中通过窗口采集捕捉。</p>
      </div>
      <el-tag :type="isPlaying ? 'success' : isPaused ? 'warning' : 'info'">
        {{ isPlaying ? '正在播放' : isPaused ? '已暂停' : '未播放' }}
      </el-tag>
    </header>

    <el-alert v-if="error" :title="error" type="error" :closable="true" show-icon @close="error = ''" />

    <el-card shadow="never">
      <template #header><strong>播放 MV</strong></template>
      <el-form label-position="top">
        <el-form-item label="B 站 BV 号（使用当前已登录账号）">
          <el-input v-model="bvid" placeholder="例如 BV1xx411c7mD" clearable />
        </el-form-item>
        <div class="actions bilibili-actions">
          <el-button type="primary" :loading="busy" @click="playBilibili">解析并用 mpv 播放 B 站视频</el-button>
        </div>
        <el-form-item label="视频直链地址">
          <el-input v-model="url" placeholder="https://.../video.mp4 或 file:///..." clearable />
        </el-form-item>
        <el-form-item label="窗口标题（可选）">
          <el-input v-model="title" placeholder="默认显示 MV" maxlength="120" />
        </el-form-item>
      </el-form>
      <div class="actions">
        <el-button type="primary" :loading="busy" @click="play">播放并打开纯视频窗口</el-button>
        <el-button :disabled="!isPlaying && !isPaused" @click="pause">暂停</el-button>
        <el-button :disabled="!isPaused" @click="resume">继续</el-button>
        <el-button type="danger" plain @click="stop">停止并关闭窗口</el-button>
      </div>
      <p class="hint">MV 开始时会自动暂停正在播放的音乐；MV 结束或停止后，仅恢复此前正在播放的音乐。</p>
    </el-card>

    <el-card shadow="never">
      <template #header><strong>当前视频信息</strong></template>
      <div class="video-info-grid">
        <div class="video-info-item">
          <span>实际解码分辨率</span>
          <strong>{{ actualResolution }}</strong>
        </div>
        <div class="video-info-item">
          <span>视频编码</span>
          <strong>{{ playback?.video_codec || '读取中' }}</strong>
        </div>
        <div class="video-info-item">
          <span>音频编码</span>
          <strong>{{ playback?.audio_codec || '读取中' }}</strong>
        </div>
      </div>
      <p class="hint">分辨率来自 mpv 当前解码后的视频参数，不是 B 站接口请求的画质等级；视频尚未开始解码时会显示等待状态。</p>
    </el-card>

    <el-card shadow="never">
      <template #header><strong>播放控制</strong></template>
      <div class="time-row"><span>{{ formatTime(playback?.position_ms) }}</span><span>{{ formatTime(playback?.duration_ms) }}</span></div>
      <el-slider
        :model-value="seekValue"
        :max="1000"
        :disabled="!playback || playback.duration_ms <= 0"
        @input="updateSeekValue"
        @change="seek"
      />
      <div class="volume-row"><span>MV 音量</span><el-slider v-model="volume" :min="0" :max="100" @change="setVolume" /></div>
      <p class="hint">视频窗口标题固定为 My-LiveHub MV，窗口本身不显示标题栏、菜单、OSD 或播放控制条。</p>
    </el-card>
  </section>
</template>

<style scoped>
.video-page{display:flex;flex-direction:column;gap:20px}.page-heading{display:flex;align-items:flex-start;justify-content:space-between;gap:20px}.page-heading h2{margin:0;font-size:25px}.eyebrow{margin:0 0 9px;color:var(--app-accent);font-size:11px;letter-spacing:.15em}.description,.hint{color:var(--el-text-color-secondary);font-size:13px}.actions{display:flex;flex-wrap:wrap;gap:9px}.bilibili-actions{margin:0 0 14px}.hint{margin:14px 0 0}.time-row{display:flex;justify-content:space-between;color:var(--el-text-color-secondary);font-size:12px;font-variant-numeric:tabular-nums}.volume-row{display:grid;grid-template-columns:90px 1fr;align-items:center;gap:12px;margin-top:18px}.volume-row span{font-size:13px;color:var(--el-text-color-secondary)}.video-info-grid{display:grid;grid-template-columns:repeat(auto-fit,minmax(160px,1fr));gap:16px}.video-info-item{display:flex;flex-direction:column;gap:7px}.video-info-item span{font-size:12px;color:var(--el-text-color-secondary)}.video-info-item strong{font-size:16px;font-weight:600;font-variant-numeric:tabular-nums}
</style>
