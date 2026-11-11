<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import {
  HomeFilled, DataLine, Connection, ChatDotRound, User, SetUp,
  Grid, Monitor, Operation, Setting, Document, Fold, Expand, Microphone, Headset, VideoPlay,
} from '@element-plus/icons-vue'
import { coreStatus, refreshCoreStatus } from './services/coreStatus'
import { initializeVoiceAudio, disposeVoiceAudio } from './services/voiceAudio'

const route = useRoute()
const router = useRouter()
const collapsed = ref(false)
const activeTitle = computed(() => String(route.meta.title ?? '总览'))
const coreStatusText = computed(() => {
  const labels: Record<string, string> = {
    created: '核心待启动',
    starting: '核心启动中',
    running: '核心运行中',
    stopping: '核心停止中',
    stopped: '核心已停止',
    failed: '核心启动失败',
  }
  return coreStatus.value ? labels[coreStatus.value.status] ?? '核心状态未知' : '正在获取核心状态'
})
const coreStatusClass = computed(() => coreStatus.value?.status ?? 'unknown')

const groups = [
  { title: '工作台', items: [{ path: '/', title: '总览', icon: HomeFilled }, { path: '/monitor', title: '实时监控', icon: DataLine }] },
  { title: '直播接入', items: [{ path: '/platforms', title: '平台管理', icon: Connection }] },
  { title: '互动中心', items: [{ path: '/events', title: '事件中心', icon: ChatDotRound }, { path: '/users', title: '用户管理', icon: User }, { path: '/rules', title: '互动规则', icon: SetUp }] },
  { title: '功能模块', items: [{ path: '/modules', title: '功能模块', icon: Grid }, { path: '/danmaku', title: '弹幕显示', icon: ChatDotRound }, { path: '/voice', title: '语音播报', icon: Microphone }, { path: '/music', title: '音乐播放器', icon: Headset }, { path: '/video', title: 'MV 播放器', icon: VideoPlay }] },
  { title: '展示管理', items: [{ path: '/layouts', title: '综合展示', icon: Monitor }, { path: '/obs', title: 'OBS 管理', icon: Operation }] },
  { title: '扩展中心', items: [{ path: '/extensions', title: '扩展管理', icon: Grid }] },
  { title: '系统管理', items: [{ path: '/settings', title: '系统设置', icon: Setting }, { path: '/logs', title: '日志诊断', icon: Document }] },
]

let refreshTimer: ReturnType<typeof setInterval> | undefined
function navigate(path: string) { router.push(path) }

onMounted(() => {
  initializeVoiceAudio()
  void refreshCoreStatus()
  refreshTimer = setInterval(() => void refreshCoreStatus(), 3000)
})
onUnmounted(() => {
  disposeVoiceAudio()
  if (refreshTimer) clearInterval(refreshTimer)
})
</script>

<template>
  <router-view v-if="route.meta.standalone" />
  <el-container v-else class="shell">
    <el-aside :width="collapsed ? '68px' : '226px'" class="sidebar">
      <div class="brand">
        <div class="brand-mark">L</div>
        <div v-if="!collapsed" class="brand-copy">
          <strong>My-LiveHub</strong>
          <small>LIVE CONTROL CENTER</small>
        </div>
      </div>
      <el-scrollbar class="nav-scroll">
        <section v-for="group in groups" :key="group.title" class="nav-group">
          <div v-if="!collapsed" class="nav-label">{{ group.title }}</div>
          <button
            v-for="item in group.items"
            :key="item.path"
            class="nav-item"
            :class="{ active: route.path === item.path }"
            :title="collapsed ? item.title : undefined"
            @click="navigate(item.path)"
          >
            <el-icon><component :is="item.icon" /></el-icon>
            <span v-if="!collapsed">{{ item.title }}</span>
          </button>
        </section>
      </el-scrollbar>
      <button class="collapse-button" @click="collapsed = !collapsed">
        <el-icon><component :is="collapsed ? Expand : Fold" /></el-icon>
        <span v-if="!collapsed">收起导航</span>
      </button>
    </el-aside>

    <el-container class="main-column">
      <el-header class="topbar">
        <div class="breadcrumb"><span>My-LiveHub</span><b>/</b><strong>{{ activeTitle }}</strong></div>
        <div class="system-status">
          <span class="status-dot" :class="coreStatusClass"></span><span>{{ coreStatusText }}</span>
          <i></i><span class="status-muted">OBS 未连接</span>
        </div>
      </el-header>
      <el-main class="workspace">
        <router-view />
      </el-main>
      <footer class="footer">
        <span>My-LiveHub · 本地桌面版</span>
        <span>核心服务运行状态实时同步</span>
      </footer>
    </el-container>
  </el-container>
</template>
