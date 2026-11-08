<script setup lang="ts">
import { computed, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import {
  HomeFilled, DataLine, Connection, ChatDotRound, User, SetUp,
  Grid, Monitor, Operation, Setting, Document, Fold, Expand,
} from '@element-plus/icons-vue'

const route = useRoute()
const router = useRouter()
const collapsed = ref(false)
const activeTitle = computed(() => String(route.meta.title ?? '总览'))

const groups = [
  { title: '工作台', items: [{ path: '/', title: '总览', icon: HomeFilled }, { path: '/monitor', title: '实时监控', icon: DataLine }] },
  { title: '直播接入', items: [{ path: '/platforms', title: '平台管理', icon: Connection }] },
  { title: '互动中心', items: [{ path: '/events', title: '事件中心', icon: ChatDotRound }, { path: '/users', title: '用户管理', icon: User }, { path: '/rules', title: '互动规则', icon: SetUp }] },
  { title: '功能模块', items: [{ path: '/modules', title: '功能模块', icon: Grid }] },
  { title: '展示管理', items: [{ path: '/layouts', title: '综合展示', icon: Monitor }, { path: '/obs', title: 'OBS 管理', icon: Operation }] },
  { title: '扩展中心', items: [{ path: '/extensions', title: '扩展管理', icon: Grid }] },
  { title: '系统管理', items: [{ path: '/settings', title: '系统设置', icon: Setting }, { path: '/logs', title: '日志诊断', icon: Document }] },
]

function navigate(path: string) { router.push(path) }
</script>

<template>
  <el-container class="shell">
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
          <span class="status-dot"></span><span>核心待启动</span>
          <i></i><span class="status-muted">OBS 未连接</span>
        </div>
      </el-header>
      <el-main class="workspace">
        <router-view />
      </el-main>
      <footer class="footer">
        <span>My-LiveHub · 本地桌面版</span>
        <span>架构初始化阶段</span>
      </footer>
    </el-container>
  </el-container>
</template>
