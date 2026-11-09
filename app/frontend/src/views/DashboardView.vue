<script setup lang="ts">
import { computed, onMounted } from 'vue'
import { Connection, Monitor, Operation, ChatDotRound } from '@element-plus/icons-vue'
import { coreModules, coreStatus, coreStatusError, refreshCoreStatus } from '../services/coreStatus'

const coreLabel = computed(() => {
  const labels: Record<string, string> = {
    created: '待启动',
    starting: '启动中',
    running: '运行中',
    stopping: '停止中',
    stopped: '已停止',
    failed: '启动失败',
  }
  return coreStatus.value ? labels[coreStatus.value.status] ?? '未知' : '获取中'
})
const runningModules = computed(() => coreModules.value.filter(item => item.status === 'running').length)
const failedModules = computed(() => coreModules.value.filter(item => item.status === 'failed').length)
const cards = computed(() => [
  { label: '核心服务', value: coreLabel.value, note: coreStatus.value ? `配置版本 v${coreStatus.value.config_version}` : '等待核心状态响应', icon: Operation },
  { label: '功能模块', value: `${runningModules.value} / ${coreModules.value.length}`, note: failedModules.value ? `${failedModules.value} 个模块启动失败` : '运行中 / 已注册', icon: Connection },
  { label: '统一事件流', value: '等待事件', note: '事件总线尚未接入', icon: ChatDotRound },
  { label: 'OBS 状态', value: '未连接', note: '由 Go 核心统一管理', icon: Monitor },
])

onMounted(() => void refreshCoreStatus())
</script>

<template>
  <section class="dashboard">
    <div class="welcome">
      <div>
        <p class="eyebrow">WORKSPACE OVERVIEW</p>
        <h2>欢迎使用 My-LiveHub</h2>
        <p class="muted">多平台直播互动控制中心。连接平台并启用模块后，运行状态将在这里汇总。</p>
      </div>
      <el-tag effect="plain" :type="coreStatus?.status === 'running' ? 'success' : coreStatus?.status === 'failed' ? 'danger' : 'info'">
        {{ coreLabel }}
      </el-tag>
    </div>

    <el-alert v-if="coreStatusError" :title="`核心状态读取失败：${coreStatusError}`" type="error" :closable="false" show-icon />

    <div class="stat-grid">
      <el-card v-for="card in cards" :key="card.label" class="stat-card" shadow="never">
        <div class="stat-head">
          <span>{{ card.label }}</span>
          <el-icon><component :is="card.icon" /></el-icon>
        </div>
        <strong>{{ card.value }}</strong>
        <small>{{ card.note }}</small>
      </el-card>
    </div>

    <el-card class="activity-card" shadow="never">
      <template #header>
        <div class="card-heading">
          <span>模块运行状态</span>
          <el-tag size="small" type="info">{{ coreModules.length }} 个模块</el-tag>
        </div>
      </template>
      <el-empty v-if="coreModules.length === 0" description="当前尚未注册业务模块" :image-size="92" />
      <el-table v-else :data="coreModules" size="small">
        <el-table-column prop="name" label="模块名称" min-width="160" />
        <el-table-column prop="status" label="运行状态" width="140" />
        <el-table-column prop="error" label="错误信息" min-width="220" show-overflow-tooltip />
      </el-table>
    </el-card>
  </section>
</template>

<style scoped>
.dashboard { display: flex; flex-direction: column; gap: 22px; }
.welcome { display: flex; justify-content: space-between; align-items: flex-start; gap: 20px; }
.eyebrow { margin: 0 0 9px; color: var(--el-color-primary); font-size: 11px; letter-spacing: .16em; }
h2 { margin: 0; font-size: 25px; font-weight: 650; }
.muted { margin: 9px 0 0; color: var(--el-text-color-secondary); }
.stat-grid { display: grid; grid-template-columns: repeat(4, minmax(0, 1fr)); gap: 14px; }
.stat-card { border-color: var(--el-border-color); background: var(--el-bg-color); }
.stat-head { display: flex; align-items: center; justify-content: space-between; color: var(--el-text-color-secondary); }
.stat-head .el-icon { font-size: 19px; color: var(--el-color-primary); }
.stat-card strong { display: block; margin: 20px 0 8px; font-size: 23px; font-weight: 650; }
.stat-card small { color: var(--el-text-color-secondary); }
.activity-card { min-height: 290px; }
.card-heading { display: flex; justify-content: space-between; align-items: center; font-weight: 600; }
@media (max-width: 1200px) { .stat-grid { grid-template-columns: repeat(2, minmax(0, 1fr)); } }
</style>
