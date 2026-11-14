<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { ArrowDown } from '@element-plus/icons-vue'
import * as MonitorService from '../../bindings/github.com/dongguo4812/My-LiveHub/app/internal/desktop/monitorservice.js'

type LogEntry = Awaited<ReturnType<typeof MonitorService.GetRecentLogs>>[number]
const entries = ref<LogEntry[]>([])
const availableDates = ref<string[]>([])
const selectedDate = ref('')
const availableDateSet = computed(() => new Set(availableDates.value))
function formatDate(date: Date) {
  const year = date.getFullYear()
  const month = String(date.getMonth() + 1).padStart(2, '0')
  const day = String(date.getDate()).padStart(2, '0')
  return `${year}-${month}-${day}`
}
function isDateUnavailable(date: Date) { return !availableDateSet.value.has(formatDate(date)) }
function dateCellClassName(date: Date) { return availableDateSet.value.has(formatDate(date)) ? 'has-log-date' : '' }
async function loadSelectedDate() {
  entries.value = selectedDate.value ? await MonitorService.GetLogsByDate(selectedDate.value) : []
}
const keyword = ref('')
const level = ref('ALL')
const loading = ref(false)
const error = ref('')
const autoRefresh = ref(true)
const selected = ref<LogEntry | null>(null)
let timer: ReturnType<typeof setInterval> | undefined

const filtered = computed(() => {
  const query = keyword.value.trim().toLocaleLowerCase()
  return entries.value.filter(item => {
    const levelMatch = level.value === 'ALL' || item.level === level.value
    const textMatch = !query || `${item.time} ${item.level} ${item.message}`.toLocaleLowerCase().includes(query)
    return levelMatch && textMatch
  })
})
const counts = computed(() => ({
  total: entries.value.length,
  error: entries.value.filter(item => item.level === 'ERROR').length,
  warn: entries.value.filter(item => item.level === 'WARN').length,
  info: entries.value.filter(item => item.level === 'INFO').length,
}))
const lastUpdated = ref<Date | null>(null)

async function refresh() {
  if (loading.value) return
  loading.value = true
  try {
    availableDates.value = await MonitorService.GetLogDates()
    if (!availableDates.value.includes(selectedDate.value)) {
      const today = formatDate(new Date())
      selectedDate.value = availableDates.value.includes(today) ? today : (availableDates.value[0] ?? '')
    }
    await loadSelectedDate()
    error.value = ''
    lastUpdated.value = new Date()
  } catch (reason) {
    error.value = reason instanceof Error ? reason.message : String(reason)
  } finally {
    loading.value = false
  }
}
function exportLogs(format: string) {
  const data = filtered.value
  const content = format === 'json'
    ? JSON.stringify(data, null, 2)
    : data.map(item => `[${item.time}] [${item.level}] ${item.message}`).join('\n')
  const blob = new Blob([content], { type: format === 'json' ? 'application/json;charset=utf-8' : 'text/plain;charset=utf-8' })
  const url = URL.createObjectURL(blob)
  const anchor = document.createElement('a')
  anchor.href = url
  anchor.download = `my-livehub-logs-${selectedDate.value || 'empty'}.${format}`
  anchor.click()
  URL.revokeObjectURL(url)
}
function handleLogRowClick(row: LogEntry) { selected.value = row }
function copySelected() {
  if (!selected.value) return
  void navigator.clipboard?.writeText('[' + selected.value.time + '] [' + selected.value.level + '] ' + selected.value.message)
}
function levelType(value: string) {
  return value === 'ERROR' ? 'danger' : value === 'WARN' ? 'warning' : value === 'DEBUG' ? 'info' : 'success'
}
async function onDateChange() {
  loading.value = true
  try { await loadSelectedDate(); error.value = ''; lastUpdated.value = new Date() }
  catch (reason) { error.value = reason instanceof Error ? reason.message : String(reason) }
  finally { loading.value = false }
}
function setAutoRefresh(value: string | number | boolean) {
  autoRefresh.value = Boolean(value)
  if (timer) clearInterval(timer)
  timer = value ? setInterval(() => void refresh(), 5000) : undefined
}
onMounted(() => { void refresh(); setAutoRefresh(true) })
onUnmounted(() => { if (timer) clearInterval(timer) })
</script>

<template>
  <section class="logs-page">
    <header class="page-heading">
      <div>
        <p class="eyebrow">APPLICATION LOGS & DIAGNOSTICS</p>
        <h2>日志诊断</h2>
        <p class="description">检索应用运行日志，定位启动失败、平台连接异常和模块错误。此页面只读取日志，不会修改运行状态。</p>
      </div>
      <div class="toolbar">
        <el-date-picker v-model="selectedDate" type="date" value-format="YYYY-MM-DD" placeholder="选择日志日期" :disabled-date="isDateUnavailable" :cell-class-name="dateCellClassName" :clearable="false" @change="onDateChange" />
        <span class="updated">最近更新：{{ lastUpdated?.toLocaleTimeString() ?? '—' }}</span>
        <el-button :loading="loading" @click="refresh">刷新日志</el-button>
      </div>
    </header>

    <el-alert v-if="error" :title="`日志读取失败：${error}`" type="error" :closable="false" show-icon />
    <div class="summary-grid">
      <el-card shadow="never" class="summary"><span>已加载日志</span><strong>{{ counts.total }}</strong><small>{{ selectedDate || '暂无日志日期' }} · 当日完整记录</small></el-card>
      <el-card shadow="never" class="summary"><span>错误</span><strong class="danger">{{ counts.error }}</strong><small>ERROR</small></el-card>
      <el-card shadow="never" class="summary"><span>警告</span><strong class="warning">{{ counts.warn }}</strong><small>WARN</small></el-card>
      <el-card shadow="never" class="summary"><span>普通信息</span><strong>{{ counts.info }}</strong><small>INFO</small></el-card>
    </div>

    <el-card shadow="never">
      <div class="filters">
        <el-input v-model="keyword" clearable placeholder="搜索日志内容、时间或关键字" class="search" />
        <el-select v-model="level" style="width: 150px">
          <el-option label="全部级别" value="ALL" />
          <el-option label="错误 ERROR" value="ERROR" />
          <el-option label="警告 WARN" value="WARN" />
          <el-option label="信息 INFO" value="INFO" />
          <el-option label="调试 DEBUG" value="DEBUG" />
        </el-select>
        <el-switch :model-value="autoRefresh" active-text="每 5 秒自动刷新" @change="setAutoRefresh" />
        <el-dropdown @command="exportLogs">
          <el-button>导出日志<el-icon class="el-icon--right"><ArrowDown /></el-icon></el-button>
          <template #dropdown><el-dropdown-menu><el-dropdown-item command="txt">导出为 TXT</el-dropdown-item><el-dropdown-item command="json">导出为 JSON</el-dropdown-item></el-dropdown-menu></template>
        </el-dropdown>
      </div>
      <div class="table-meta"><span>显示 {{ filtered.length }} / {{ entries.length }} 条</span><span>当前日期 {{ selectedDate || '—' }} · 已加载该日期全部轮转日志</span></div>
      <el-table :data="filtered" size="small" stripe height="520" empty-text="没有匹配的日志记录" @row-click="handleLogRowClick">
        <el-table-column prop="time" label="时间" width="180" />
        <el-table-column label="级别" width="105">
          <template #default="{ row }"><el-tag size="small" :type="levelType(row.level)">{{ row.level }}</el-tag></template>
        </el-table-column>
        <el-table-column prop="message" label="日志内容" min-width="320" show-overflow-tooltip />
      </el-table>
      <p class="footnote">点击任意日志行可查看完整内容。日历仅允许选择存在日志的日期；同一天的轮转文件会合并并按时间顺序展示。保留期限可在「系统设置」中调整。</p>
    </el-card>

    <el-drawer :model-value="!!selected" title="日志详情" size="min(680px, 92vw)" @close="selected = null">
      <template v-if="selected">
        <div class="detail-meta"><el-tag :type="levelType(selected.level)">{{ selected.level }}</el-tag><span>{{ selected.time || '时间未知' }}</span></div>
        <pre class="detail-message">{{ selected.message }}</pre>
        <el-button @click="copySelected">复制日志内容</el-button>
      </template>
    </el-drawer>
  </section>
</template>

<style scoped>
:deep(.has-log-date .el-date-table-cell__text){font-weight:700;color:var(--el-color-primary)}.logs-page{display:flex;flex-direction:column;gap:18px;min-width:0}.page-heading{display:flex;justify-content:space-between;align-items:flex-start;gap:20px}.page-heading h2{margin:0;font-size:25px}.eyebrow{margin:0 0 8px;color:var(--app-accent);font-size:11px;letter-spacing:.15em}.description,.updated,.footnote,.table-meta{color:var(--el-text-color-secondary);font-size:13px}.toolbar,.filters,.table-meta,.detail-meta{display:flex;align-items:center;gap:12px}.toolbar{flex-wrap:wrap;justify-content:flex-end}.summary-grid{display:grid;grid-template-columns:repeat(4,minmax(0,1fr));gap:12px}.summary{min-width:0}.summary span,.summary small{display:block;color:var(--el-text-color-secondary);font-size:12px}.summary strong{display:block;margin:8px 0;font-size:26px;font-variant-numeric:tabular-nums}.summary .danger{color:var(--el-color-danger)}.summary .warning{color:var(--el-color-warning)}.filters{flex-wrap:wrap;margin-bottom:14px}.search{max-width:360px;flex:1}.table-meta{justify-content:space-between;margin:4px 0 10px}.footnote{margin:12px 0 0}.detail-meta{margin-bottom:18px}.detail-message{white-space:pre-wrap;overflow-wrap:anywhere;padding:14px;border:1px solid var(--app-border);border-radius:8px;background:var(--app-bg-control);font:13px/1.7 ui-monospace,Consolas,monospace;max-height:60vh;overflow:auto}@media(max-width:900px){.summary-grid{grid-template-columns:repeat(2,minmax(0,1fr))}.page-heading{flex-direction:column}.toolbar{justify-content:flex-start}}
</style>
