<script setup lang="ts">
import { ElIcon } from 'element-plus'
import { Connection, Monitor, Operation, ChatDotRound } from '@element-plus/icons-vue'

const cards = [
  { label: '直播平台', value: '尚未接入', note: '等待平台适配器启用', icon: Connection },
  { label: '统一事件流', value: '等待事件', note: '事件总线尚未启动', icon: ChatDotRound },
  { label: 'OBS 状态', value: '未连接', note: '由 Go 核心统一管理', icon: Monitor },
  { label: '功能模块', value: '未启用', note: '按需启动模块资源', icon: Operation },
]
</script>

<template>
  <section class="dashboard">
    <div class="welcome">
      <div>
        <p class="eyebrow">WORKSPACE OVERVIEW</p>
        <h2>欢迎使用 My-LiveHub</h2>
        <p class="muted">多平台直播互动控制中心。连接平台并启用模块后，运行状态将在这里汇总。</p>
      </div>
      <el-tag effect="plain" type="info">核心骨架 · 初始化阶段</el-tag>
    </div>

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
          <span>系统活动</span>
          <el-tag size="small" type="info">暂无实时数据</el-tag>
        </div>
      </template>
      <el-empty description="核心事件总线接入后，这里将显示平台事件和系统活动" :image-size="92" />
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
