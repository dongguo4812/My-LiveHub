<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import * as CoreService from '../../bindings/github.com/dongguo4812/My-LiveHub/app/internal/desktop/coreservice.js'
import * as PlatformService from '../../bindings/github.com/dongguo4812/My-LiveHub/app/internal/desktop/platformservice.js'
import * as MusicService from '../../bindings/github.com/dongguo4812/My-LiveHub/app/internal/desktop/musicservice.js'
import * as VideoQueueService from '../../bindings/github.com/dongguo4812/My-LiveHub/app/internal/desktop/videoqueueservice.js'
import * as LOLHexService from '../../bindings/github.com/dongguo4812/My-LiveHub/app/internal/desktop/lolhexservice.js'
import * as MonitorService from '../../bindings/github.com/dongguo4812/My-LiveHub/app/internal/desktop/monitorservice.js'

type CoreStatus = Awaited<ReturnType<typeof CoreService.GetStatus>>
type ModuleStatus = Awaited<ReturnType<typeof CoreService.GetModules>>[number]
type PlatformStatus = Awaited<ReturnType<typeof PlatformService.GetPlatforms>>[number]
type LiveEvent = Awaited<ReturnType<typeof PlatformService.GetEvents>>[number]
type MusicQueue = Awaited<ReturnType<typeof MusicService.GetQueue>>
type MusicPlayback = Awaited<ReturnType<typeof MusicService.GetPlaybackStatus>>
type VideoSnapshot = Awaited<ReturnType<typeof VideoQueueService.GetSnapshot>>
type HexStatus = Awaited<ReturnType<typeof LOLHexService.GetStatus>>
type Metrics = Awaited<ReturnType<typeof MonitorService.GetSystemMetrics>>
type LogEntry = Awaited<ReturnType<typeof MonitorService.GetRecentLogs>>[number]

const core = ref<CoreStatus | null>(null)
const modules = ref<ModuleStatus[]>([])
const platforms = ref<PlatformStatus[]>([])
const events = ref<LiveEvent[]>([])
const musicQueue = ref<MusicQueue | null>(null)
const musicPlayback = ref<MusicPlayback | null>(null)
const video = ref<VideoSnapshot | null>(null)
const hex = ref<HexStatus | null>(null)
const metrics = ref<Metrics | null>(null)
const logs = ref<LogEntry[]>([])
const errors = ref<Record<string, string>>({})
const refreshing = ref(false)
const lastUpdated = ref<Date | null>(null)
let timer: ReturnType<typeof setInterval> | undefined
let logTimer: ReturnType<typeof setInterval> | undefined

const connectedPlatforms = computed(() => platforms.value.filter(item => item.status === 'connected').length)
const runningModules = computed(() => modules.value.filter(item => item.status === 'running').length)
const failedModules = computed(() => modules.value.filter(item => item.status === 'failed').length)
const eventCounts = computed(() => {
  const counts: Record<string, number> = {}
  for (const item of events.value) counts[item.type] = (counts[item.type] ?? 0) + 1
  return counts
})
const recentEvents = computed(() => [...events.value].sort((a, b) => Number(b.timestamp) - Number(a.timestamp)).slice(0, 30))
const currentTrack = computed(() => musicPlayback.value?.track ?? musicQueue.value?.current?.track ?? null)
const musicState = computed(() => musicPlayback.value?.state ?? 'unavailable')
const videoState = computed(() => {
  if (!video.value?.current) return '空闲'
  if (video.value.playback?.is_playing) return '正在播放'
  if (video.value.playback?.is_paused) return '已暂停'
  if (video.value.playback?.is_open) return '播放器已打开'
  return '正在准备'
})
const coreStatusText = computed(() => {
  const map: Record<string, string> = { created: '待启动', starting: '启动中', running: '运行中', stopping: '停止中', stopped: '已停止', failed: '启动失败' }
  return core.value ? map[core.value.status] ?? core.value.status : '读取中'
})
const formatBytes = (value?: number) => {
  const n = Number(value ?? 0)
  if (!n) return '0 B'
  const units = ['B', 'KB', 'MB', 'GB']
  const index = Math.min(Math.floor(Math.log(n) / Math.log(1024)), units.length - 1)
  return `${(n / Math.pow(1024, index)).toFixed(index === 0 ? 0 : 1)} ${units[index]}`
}
const formatDuration = (seconds?: number) => {
  let value = Math.max(0, Math.floor(Number(seconds ?? 0)))
  const hours = Math.floor(value / 3600); value %= 3600
  const minutes = Math.floor(value / 60); const rest = value % 60
  return hours ? `${hours}小时${minutes}分` : minutes ? `${minutes}分${rest}秒` : `${rest}秒`
}
const formatTime = (timestamp: number | string) => {
  const date = new Date(typeof timestamp === 'number' ? timestamp : timestamp)
  return Number.isNaN(date.getTime()) ? '—' : date.toLocaleTimeString()
}
const moduleLabel = (id: string) => ({ 'platform-connections': '直播平台连接', voice: '语音播报', music: '音乐播放', video: '视频点播', live2d: 'Live2D', 'lol-hex-display': 'LOL Hex Display' } as Record<string, string>)[id] ?? id
const eventLabel = (type: string) => ({ danmaku: '弹幕', gift: '礼物', super_chat: '醒目留言', guard: '舰队', like: '点赞', entry: '进入直播间' } as Record<string, string>)[type] ?? type
const statusType = (status: string) => status === 'running' || status === 'connected' ? 'success' : status === 'failed' ? 'danger' : status === 'starting' || status === 'connecting' ? 'warning' : 'info'

async function refreshAll() {
  if (refreshing.value) return
  refreshing.value = true
  const tasks = [
    ['核心服务', async () => { core.value = await CoreService.GetStatus(); modules.value = await CoreService.GetModules() }],
    ['平台连接', async () => { platforms.value = await PlatformService.GetPlatforms() }],
    ['事件流', async () => { events.value = await PlatformService.GetEvents() }],
    ['音乐播放', async () => { musicQueue.value = await MusicService.GetQueue(); try { musicPlayback.value = await MusicService.GetPlaybackStatus() } catch { musicPlayback.value = null } }],
    ['视频点播', async () => { video.value = await VideoQueueService.GetSnapshot() }],
    ['LOL Hex', async () => { hex.value = await LOLHexService.GetStatus() }],
    ['系统资源', async () => { metrics.value = await MonitorService.GetSystemMetrics() }],
  ] as const
  const results = await Promise.allSettled(tasks.map(([, run]) => run()))
  const nextErrors: Record<string, string> = {}
  results.forEach((result, index) => { if (result.status === 'rejected') nextErrors[tasks[index][0]] = result.reason instanceof Error ? result.reason.message : String(result.reason) })
  errors.value = nextErrors
  lastUpdated.value = new Date()
  refreshing.value = false
}
async function refreshLogs() {
  try { logs.value = await MonitorService.GetRecentLogs(100) }
  catch (error) { errors.value = { ...errors.value, 日志: error instanceof Error ? error.message : String(error) } }
}
onMounted(() => {
  void refreshAll()
  void refreshLogs()
  timer = setInterval(() => void refreshAll(), 2000)
  logTimer = setInterval(() => void refreshLogs(), 5000)
})
onUnmounted(() => {
  if (timer) clearInterval(timer)
  if (logTimer) clearInterval(logTimer)
})
</script>

<template>
  <section class="monitor-page">
    <header class="page-heading">
      <div>
        <p class="eyebrow">LIVE SYSTEM OBSERVABILITY</p>
        <h2>实时监控</h2>
        <p class="muted">只读查看服务、直播连接、互动事件、点播状态和运行日志，不在此页面执行控制操作。</p>
      </div>
      <div class="refresh-meta">
        <span>自动刷新：状态 2 秒 / 日志 5 秒</span>
        <span>最近更新：{{ lastUpdated?.toLocaleTimeString() ?? '—' }}</span>
        <el-button :loading="refreshing" @click="refreshAll">立即刷新</el-button>
      </div>
    </header>

    <el-alert v-for="(message, name) in errors" :key="name" :title="`${name}读取失败：${message}`" type="warning" :closable="false" show-icon />

    <div class="summary-grid">
      <el-card shadow="never" class="metric-card"><span>核心服务</span><strong>{{ coreStatusText }}</strong><small>配置版本 {{ core?.config_version ?? '—' }}</small></el-card>
      <el-card shadow="never" class="metric-card"><span>直播平台</span><strong>{{ connectedPlatforms }} / {{ platforms.length }}</strong><small>已连接 / 已注册</small></el-card>
      <el-card shadow="never" class="metric-card"><span>功能模块</span><strong>{{ runningModules }} / {{ modules.length }}</strong><small>{{ failedModules }} 个模块异常</small></el-card>
      <el-card shadow="never" class="metric-card"><span>事件缓存</span><strong>{{ events.length }}</strong><small>弹幕 {{ eventCounts.danmaku ?? 0 }} · 礼物 {{ eventCounts.gift ?? 0 }} · SC {{ eventCounts.super_chat ?? 0 }}</small></el-card>
    </div>

    <div class="content-grid">
      <el-card shadow="never">
        <template #header><div class="card-heading"><strong>直播平台连接</strong><el-tag size="small">{{ platforms.length }} 个适配器</el-tag></div></template>
        <el-table :data="platforms" size="small" empty-text="暂无已注册的平台">
          <el-table-column prop="platform_id" label="平台" min-width="110" />
          <el-table-column label="连接状态" width="110"><template #default="{ row }"><el-tag :type="statusType(row.status)" size="small">{{ row.status }}</el-tag></template></el-table-column>
          <el-table-column prop="live_status" label="直播状态" width="110" />
          <el-table-column prop="error" label="异常信息" min-width="150" show-overflow-tooltip />
        </el-table>
      </el-card>

      <el-card shadow="never">
        <template #header><div class="card-heading"><strong>功能模块状态</strong><el-tag size="small">{{ modules.length }} 个模块</el-tag></div></template>
        <el-table :data="modules" size="small" empty-text="暂无模块数据">
          <el-table-column label="模块" min-width="130"><template #default="{ row }">{{ moduleLabel(row.name) }}</template></el-table-column>
          <el-table-column label="状态" width="100"><template #default="{ row }"><el-tag :type="statusType(row.status)" size="small">{{ row.status }}</el-tag></template></el-table-column>
          <el-table-column prop="error" label="错误信息" min-width="150" show-overflow-tooltip />
        </el-table>
      </el-card>
    </div>

    <div class="content-grid">
      <el-card shadow="never">
        <template #header><div class="card-heading"><strong>音乐点播</strong><el-tag :type="musicState === 'playing' ? 'success' : 'info'" size="small">{{ musicState }}</el-tag></div></template>
        <div v-if="currentTrack" class="now-playing"><strong>{{ currentTrack.title }}</strong><span>{{ currentTrack.artist || '未知歌手' }}</span></div>
        <el-empty v-else description="当前没有可读取的播放歌曲" :image-size="56" />
        <div class="queue-meta"><span>待播歌曲</span><strong>{{ musicQueue?.items?.length ?? 0 }}</strong></div>
      </el-card>
      <el-card shadow="never">
        <template #header><div class="card-heading"><strong>视频点播</strong><el-tag :type="video?.playback?.is_playing ? 'success' : 'info'" size="small">{{ videoState }}</el-tag></div></template>
        <div v-if="video?.current" class="now-playing"><strong>{{ video.current.title || video.current.bvid }}</strong><span>{{ video.current.bvid }} · 点播人：{{ video.current.requester }}</span></div>
        <el-empty v-else description="当前没有正在播放的视频" :image-size="56" />
        <div class="queue-meta"><span>待播视频</span><strong>{{ video?.queue_count ?? 0 }}</strong></div>
      </el-card>
      <el-card shadow="never">
        <template #header><div class="card-heading"><strong>LOL Hex 扩展</strong><el-tag :type="hex?.running ? 'success' : 'info'" size="small">{{ hex?.state ?? '读取中' }}</el-tag></div></template>
        <div class="hex-details"><span>服务端口：{{ hex?.port ?? '—' }}</span><span>进程 PID：{{ hex?.pid ?? '—' }}</span><span>运行归属：{{ hex?.owned ? 'My-LiveHub 托管' : hex?.external ? '外部启动' : '未运行' }}</span></div>
        <p v-if="hex?.error" class="error-text">{{ hex.error }}</p>
      </el-card>
    </div>

    <el-card shadow="never">
      <template #header><div class="card-heading"><strong>系统资源与运行时间</strong><el-tag size="small" effect="plain">Go 运行时数据</el-tag></div></template>
      <div class="system-grid">
        <div><span>程序运行时间</span><strong>{{ formatDuration(metrics?.uptime_seconds) }}</strong></div>
        <div><span>Go 堆内存使用</span><strong>{{ formatBytes(metrics?.go_heap_alloc_bytes) }}</strong></div>
        <div><span>Go 堆内存保留</span><strong>{{ formatBytes(metrics?.go_heap_sys_bytes) }}</strong></div>
        <div><span>累计内存分配</span><strong>{{ formatBytes(metrics?.go_total_alloc_bytes) }}</strong></div>
        <div><span>运行中的 Goroutine</span><strong>{{ metrics?.goroutines ?? '—' }}</strong></div>
        <div><span>逻辑 CPU 数</span><strong>{{ metrics?.cpu_count ?? '—' }}</strong></div>
      </div>
      <p class="hint">内存数据为 Go 运行时堆内存，不等同于 Windows 进程总内存；当前版本不展示未经采集的 CPU 使用率。</p>
    </el-card>

    <div class="content-grid">
      <el-card shadow="never">
        <template #header><div class="card-heading"><strong>实时互动事件</strong><el-tag size="small">{{ recentEvents.length }} 条</el-tag></div></template>
        <el-table :data="recentEvents" size="small" max-height="360" empty-text="等待直播间事件">
          <el-table-column label="时间" width="100"><template #default="{ row }">{{ formatTime(row.timestamp) }}</template></el-table-column>
          <el-table-column label="类型" width="90"><template #default="{ row }">{{ eventLabel(row.type) }}</template></el-table-column>
          <el-table-column prop="user_name" label="用户" width="120" show-overflow-tooltip />
          <el-table-column label="内容" min-width="180" show-overflow-tooltip><template #default="{ row }">{{ row.content || row.gift_name || '—' }}</template></el-table-column>
          <el-table-column prop="platform_id" label="平台" width="90" />
        </el-table>
      </el-card>
      <el-card shadow="never">
        <template #header><div class="card-heading"><strong>运行日志</strong><el-tag size="small">{{ logs.length }} 条</el-tag></div></template>
        <div v-if="logs.length" class="log-list">
          <div v-for="(entry, index) in logs" :key="index" class="log-entry">
            <span class="log-time">{{ entry.time }}</span><el-tag size="small" :type="entry.level === 'ERROR' ? 'danger' : entry.level === 'WARN' ? 'warning' : 'info'">{{ entry.level }}</el-tag><span class="log-message">{{ entry.message }}</span>
          </div>
        </div>
        <el-empty v-else description="暂无日志或日志文件尚未生成" :image-size="56" />
      </el-card>
    </div>
  </section>
</template>

<style scoped>
.monitor-page{display:flex;flex-direction:column;gap:18px}.page-heading{display:flex;justify-content:space-between;align-items:flex-start;gap:20px}.eyebrow{margin:0 0 8px;color:var(--el-color-primary);font-size:11px;letter-spacing:.15em}.page-heading h2{margin:0;font-size:25px}.muted,.hint{color:var(--el-text-color-secondary);font-size:13px}.muted{margin:8px 0 0}.refresh-meta{display:flex;align-items:flex-end;flex-direction:column;gap:7px;color:var(--el-text-color-secondary);font-size:12px}.summary-grid{display:grid;grid-template-columns:repeat(4,minmax(0,1fr));gap:12px}.metric-card{min-width:0}.metric-card span,.system-grid span{display:block;color:var(--el-text-color-secondary);font-size:12px}.metric-card strong{display:block;margin:13px 0 6px;font-size:22px}.metric-card small{color:var(--el-text-color-secondary);font-size:11px}.content-grid{display:grid;grid-template-columns:repeat(2,minmax(0,1fr));gap:14px}.card-heading{display:flex;align-items:center;justify-content:space-between}.now-playing{display:flex;flex-direction:column;gap:6px;min-height:58px}.now-playing span{color:var(--el-text-color-secondary);font-size:12px}.queue-meta{display:flex;justify-content:space-between;align-items:center;padding-top:12px;margin-top:12px;border-top:1px solid var(--el-border-color-lighter);font-size:13px}.hex-details{display:flex;flex-direction:column;gap:9px;font-size:13px}.error-text{color:var(--el-color-danger);font-size:12px}.system-grid{display:grid;grid-template-columns:repeat(3,minmax(0,1fr));gap:18px}.system-grid strong{display:block;margin-top:7px;font-size:18px;font-variant-numeric:tabular-nums}.hint{margin:16px 0 0;font-size:12px}.log-list{max-height:360px;overflow:auto}.log-entry{display:grid;grid-template-columns:145px 62px minmax(0,1fr);align-items:start;gap:8px;padding:7px 0;border-bottom:1px solid var(--el-border-color-lighter);font-size:12px}.log-time{color:var(--el-text-color-secondary);font-variant-numeric:tabular-nums}.log-message{overflow-wrap:anywhere;white-space:pre-wrap}.refresh-meta span{white-space:nowrap}
@media(max-width:1100px){.summary-grid{grid-template-columns:repeat(2,minmax(0,1fr))}.content-grid{grid-template-columns:1fr}.system-grid{grid-template-columns:repeat(2,minmax(0,1fr))}}
@media(max-width:700px){.page-heading{flex-direction:column}.refresh-meta{align-items:flex-start}.summary-grid{grid-template-columns:1fr 1fr}.system-grid{grid-template-columns:1fr 1fr}.log-entry{grid-template-columns:1fr}.log-time{display:none}}
</style>
