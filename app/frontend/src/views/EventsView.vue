<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import * as PlatformService from '../../bindings/github.com/dongguo4812/My-LiveHub/app/internal/desktop/platformservice.js'

type EventItem = Awaited<ReturnType<typeof PlatformService.GetEvents>>[number]
type PlatformItem = Awaited<ReturnType<typeof PlatformService.GetPlatforms>>[number]

const events = ref<EventItem[]>([])
const platforms = ref<PlatformItem[]>([])
const platformFilter = ref('')
const typeFilter = ref('')
const sourceFilter = ref('')
const keyword = ref('')
const selectedEvent = ref<EventItem | null>(null)
const detailVisible = ref(false)
const loading = ref(false)
const error = ref('')
let timer: ReturnType<typeof setInterval> | undefined
let requestPending = false

const eventTypes = [
  { label: '弹幕', value: 'danmaku' },
  { label: '礼物', value: 'gift' },
  { label: '醒目留言（SC）', value: 'super_chat' },
  { label: '舰队', value: 'guard' },
  { label: '关注', value: 'follow' },
  { label: '点赞', value: 'like' },
  { label: '进入直播间', value: 'welcome' },
  { label: '分享', value: 'share' },
]
const typeLabels = Object.fromEntries(eventTypes.map(item => [item.value, item.label])) as Record<string, string>
const filteredEvents = computed(() => {
  const query = keyword.value.trim().toLocaleLowerCase()
  return [...events.value]
    .filter(item => !platformFilter.value || item.platform_id === platformFilter.value)
    .filter(item => !typeFilter.value || item.type === typeFilter.value)
    .filter(item => !sourceFilter.value || (sourceFilter.value === 'simulation' ? item.is_simulation : !item.is_simulation))
    .filter(item => !query || [item.user_name, String(item.user_id), item.content, item.gift_name, item.platform_id, item.type].some(value => String(value ?? '').toLocaleLowerCase().includes(query)))
    .sort((a, b) => Number(b.timestamp) - Number(a.timestamp))
})
const simulationCount = computed(() => events.value.filter(item => item.is_simulation).length)
const liveCount = computed(() => events.value.length - simulationCount.value)

function typeLabel(type: string) { return typeLabels[type] ?? type }
function formatDate(timestamp: number) {
  const date = new Date(timestamp)
  return Number.isNaN(date.getTime()) ? '—' : date.toLocaleString()
}
function eventSummary(item: EventItem) {
  if (item.type === 'gift') return `${item.gift_name || '礼物'} × ${item.gift_count || 1} · ¥${Number(item.total_price || 0).toFixed(2)}`
  if (item.type === 'super_chat') return `¥${Number(item.amount || 0).toFixed(2)} · ${item.content}`
  if (item.type === 'guard') return `${item.content} · ¥${Number(item.amount || 0).toFixed(2)}`
  return item.content || '—'
}
async function refreshEvents(showLoading = false) {
  if (requestPending) return
  requestPending = true
  if (showLoading) loading.value = true
  try {
    const [eventItems, platformItems] = await Promise.all([PlatformService.GetEvents(), PlatformService.GetPlatforms()])
    events.value = eventItems
    platforms.value = platformItems
    error.value = ''
  } catch (cause) {
    error.value = cause instanceof Error ? cause.message : String(cause)
  } finally {
    requestPending = false
    loading.value = false
  }
}
function clearFilters() {
  platformFilter.value = ''
  typeFilter.value = ''
  sourceFilter.value = ''
  keyword.value = ''
}
onMounted(() => {
  void refreshEvents(true)
  timer = setInterval(() => void refreshEvents(), 1000)
})
onUnmounted(() => { if (timer) clearInterval(timer) })
</script>

<template>
  <section class="events-page">
    <header class="page-heading">
      <div>
        <p class="eyebrow">UNIFIED LIVE EVENT STREAM</p>
        <h2>事件中心</h2>
        <p class="muted">集中查看各直播平台接入的标准化互动事件。此处只查看和筛选事件，不执行事件操作。</p>
      </div>
      <el-button :loading="loading" @click="refreshEvents(true)">立即刷新</el-button>
    </header>

    <el-alert v-if="error" :title="`事件读取失败：${error}`" type="error" :closable="false" show-icon />
    <div class="summary-grid">
      <el-card shadow="never" class="summary-card"><span>缓存事件</span><strong>{{ events.length }}</strong><small>最多保留最近 200 条</small></el-card>
      <el-card shadow="never" class="summary-card"><span>真实事件</span><strong>{{ liveCount }}</strong><small>来自直播平台</small></el-card>
      <el-card shadow="never" class="summary-card"><span>模拟事件</span><strong>{{ simulationCount }}</strong><small>来自互动模拟器</small></el-card>
      <el-card shadow="never" class="summary-card"><span>当前筛选结果</span><strong>{{ filteredEvents.length }}</strong><small>符合当前筛选条件</small></el-card>
    </div>

    <el-card shadow="never">
      <div class="filters">
        <el-select v-model="platformFilter" placeholder="全部平台" clearable>
          <el-option v-for="item in platforms" :key="item.platform_id" :label="item.platform_id" :value="item.platform_id" />
        </el-select>
        <el-select v-model="typeFilter" placeholder="全部事件类型" clearable>
          <el-option v-for="item in eventTypes" :key="item.value" :label="item.label" :value="item.value" />
        </el-select>
        <el-select v-model="sourceFilter" placeholder="全部来源" clearable>
          <el-option label="真实事件" value="live" />
          <el-option label="模拟事件" value="simulation" />
        </el-select>
        <el-input v-model="keyword" placeholder="搜索用户、UID、事件内容" clearable />
        <el-button @click="clearFilters">清空筛选</el-button>
      </div>
      <div class="table-meta">
        <span>实时更新 · 每秒刷新</span>
        <span>当前显示 {{ filteredEvents.length }} / {{ events.length }} 条</span>
      </div>
      <el-table :data="filteredEvents" v-loading="loading" row-key="id" max-height="620" empty-text="暂无符合条件的事件">
        <el-table-column label="时间" min-width="165">
          <template #default="{ row }">{{ formatDate(row.timestamp) }}</template>
        </el-table-column>
        <el-table-column label="平台" prop="platform_id" width="110" />
        <el-table-column label="事件类型" width="130">
          <template #default="{ row }">{{ typeLabel(row.type) }}</template>
        </el-table-column>
        <el-table-column label="用户" min-width="150">
          <template #default="{ row }">
            <div class="user-cell">
              <el-avatar v-if="row.avatar" :src="row.avatar" :size="28" />
              <el-avatar v-else :size="28">{{ (row.user_name || '?').slice(0, 1) }}</el-avatar>
              <span>{{ row.user_name || '未知用户' }}</span>
            </div>
            <small class="uid">UID：{{ row.user_id }}</small>
          </template>
        </el-table-column>
        <el-table-column label="事件内容" min-width="230" show-overflow-tooltip>
          <template #default="{ row }">{{ eventSummary(row) }}</template>
        </el-table-column>
        <el-table-column label="来源" width="90">
          <template #default="{ row }">
            <el-tag :type="row.is_simulation ? 'warning' : 'success'" size="small">{{ row.is_simulation ? '模拟' : '真实' }}</el-tag>
          </template>
        </el-table-column>
        <el-table-column label="详情" width="85" fixed="right">
          <template #default="{ row }"><el-button link type="primary" @click="selectedEvent = row; detailVisible = true">查看</el-button></template>
        </el-table-column>
      </el-table>
    </el-card>

    <el-dialog v-model="detailVisible" title="事件详情" width="min(680px, 92vw)" destroy-on-close>
      <template v-if="selectedEvent">
        <el-descriptions :column="2" border>
          <el-descriptions-item label="事件 ID">{{ selectedEvent.id }}</el-descriptions-item>
          <el-descriptions-item label="事件类型">{{ typeLabel(selectedEvent.type) }}</el-descriptions-item>
          <el-descriptions-item label="平台">{{ selectedEvent.platform_id }}</el-descriptions-item>
          <el-descriptions-item label="来源">{{ selectedEvent.is_simulation ? '模拟事件' : '真实事件' }}</el-descriptions-item>
          <el-descriptions-item label="发生时间" :span="2">{{ formatDate(selectedEvent.timestamp) }}</el-descriptions-item>
          <el-descriptions-item label="用户昵称">{{ selectedEvent.user_name || '—' }}</el-descriptions-item>
          <el-descriptions-item label="用户 UID">{{ selectedEvent.user_id }}</el-descriptions-item>
          <el-descriptions-item label="直播间 ID">{{ selectedEvent.room_id || '—' }}</el-descriptions-item>
          <el-descriptions-item label="用户等级">{{ selectedEvent.user_level || '—' }}</el-descriptions-item>
          <el-descriptions-item label="房管">{{ selectedEvent.is_moderator ? '是' : '否' }}</el-descriptions-item>
          <el-descriptions-item label="主播">{{ selectedEvent.is_room_owner ? '是' : '否' }}</el-descriptions-item>
          <el-descriptions-item label="粉丝牌">{{ selectedEvent.fan_medal_name || '—' }}</el-descriptions-item>
          <el-descriptions-item label="粉丝牌等级">{{ selectedEvent.fan_medal_level || '—' }}</el-descriptions-item>
          <el-descriptions-item v-if="selectedEvent.type === 'gift'" label="礼物">{{ selectedEvent.gift_name }} × {{ selectedEvent.gift_count }}</el-descriptions-item>
          <el-descriptions-item v-if="selectedEvent.type === 'gift'" label="礼物总价">¥{{ Number(selectedEvent.total_price || 0).toFixed(2) }}</el-descriptions-item>
          <el-descriptions-item v-if="selectedEvent.type === 'super_chat' || selectedEvent.type === 'guard'" label="金额">¥{{ Number(selectedEvent.amount || 0).toFixed(2) }}</el-descriptions-item>
          <el-descriptions-item v-if="selectedEvent.type === 'super_chat'" label="展示时长">{{ selectedEvent.duration }} 秒</el-descriptions-item>
          <el-descriptions-item v-if="selectedEvent.type === 'guard'" label="开通月份">{{ selectedEvent.months }} 个月</el-descriptions-item>
          <el-descriptions-item label="事件内容" :span="2">{{ selectedEvent.content || '—' }}</el-descriptions-item>
        </el-descriptions>
      </template>
    </el-dialog>
  </section>
</template>

<style scoped>
.events-page{display:flex;flex-direction:column;gap:18px;min-width:0}.page-heading{display:flex;align-items:flex-start;justify-content:space-between;gap:18px}.eyebrow{margin:0 0 8px;color:var(--el-color-primary);font-size:11px;letter-spacing:.15em}.page-heading h2{margin:0;font-size:25px}.muted{margin:8px 0 0;color:var(--el-text-color-secondary);font-size:13px}.summary-grid{display:grid;grid-template-columns:repeat(4,minmax(0,1fr));gap:12px}.summary-card span,.summary-card small{display:block;color:var(--el-text-color-secondary);font-size:12px}.summary-card strong{display:block;margin:10px 0 5px;font-size:24px;font-variant-numeric:tabular-nums}.filters{display:grid;grid-template-columns:repeat(4,minmax(130px,1fr)) auto;gap:10px;margin-bottom:14px}.table-meta{display:flex;justify-content:space-between;margin-bottom:10px;color:var(--el-text-color-secondary);font-size:12px}.user-cell{display:flex;align-items:center;gap:8px}.uid{display:block;margin:3px 0 0 36px;color:var(--el-text-color-secondary);font-size:11px}
@media(max-width:1050px){.filters{grid-template-columns:repeat(2,minmax(0,1fr))}.summary-grid{grid-template-columns:repeat(2,minmax(0,1fr))}}
@media(max-width:600px){.page-heading{flex-direction:column}.filters{grid-template-columns:1fr}.summary-grid{grid-template-columns:repeat(2,minmax(0,1fr))}.table-meta{flex-direction:column;gap:5px}}
</style>
