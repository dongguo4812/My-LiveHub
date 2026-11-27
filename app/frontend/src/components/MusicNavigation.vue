<script setup lang="ts">
import { onMounted, onUnmounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import * as MusicService from '../../bindings/github.com/dongguo4812/My-LiveHub/app/internal/desktop/musicservice.js'

type QueueSnapshot = Awaited<ReturnType<typeof MusicService.GetQueue>>
const route = useRoute()
const router = useRouter()
const queueCount = ref(0)
let refreshTimer: ReturnType<typeof setInterval> | undefined

async function refreshQueueCount() {
  try {
    const queue: QueueSnapshot = await MusicService.GetQueue()
    queueCount.value = queue.items?.length ?? 0
  } catch {
    // Preserve the last known count when the desktop service is temporarily unavailable.
  }
}

function isActive(tab: string) {
  if (tab === 'playlists') return route.path === '/music/playlists'
  if (tab === 'sources') return route.path === '/music/sources'
  return route.path === '/music' && (String(route.query.tab ?? 'now-playing') === tab)
}

function openTab(tab: string) {
  if (tab === 'playlists') {
    void router.push('/music/playlists')
    return
  }
  if (tab === 'sources') {
    void router.push('/music/sources')
    return
  }
  void router.push({ path: '/music', query: { tab } })
}

onMounted(() => {
  void refreshQueueCount()
  refreshTimer = setInterval(() => void refreshQueueCount(), 1500)
})
onUnmounted(() => {
  if (refreshTimer) clearInterval(refreshTimer)
})
</script>

<template>
  <nav class="music-tabs player-tabs" aria-label="音乐页面导航">
    <button :class="{ active: isActive('now-playing') }" :aria-current="isActive('now-playing') ? 'page' : undefined" @click="openTab('now-playing')">正在播放</button>
    <button :class="{ active: isActive('search') }" :aria-current="isActive('search') ? 'page' : undefined" @click="openTab('search')">歌曲搜索</button>
    <button :class="{ active: isActive('visitor-requests') }" :aria-current="isActive('visitor-requests') ? 'page' : undefined" @click="openTab('visitor-requests')">观众点播 <span class="queue-count">{{ queueCount }}</span></button>
    <button :class="{ active: isActive('playlists') }" :aria-current="isActive('playlists') ? 'page' : undefined" @click="openTab('playlists')">我的列表</button>
    <button :class="{ active: isActive('settings') }" :aria-current="isActive('settings') ? 'page' : undefined" @click="openTab('settings')">音乐设置</button>
    <button :class="{ active: isActive('sources') }" :aria-current="isActive('sources') ? 'page' : undefined" @click="openTab('sources')">JS 音源脚本管理</button>
  </nav>
</template>

<style scoped>
.music-tabs{display:flex;align-items:center;gap:6px;overflow-x:auto;padding:0 2px 8px;border-bottom:1px solid var(--app-border)}
.music-tabs button{white-space:nowrap;border:0;border-bottom:2px solid transparent;background:transparent;color:var(--el-text-color-secondary);padding:11px 14px;margin-bottom:-1px;font:inherit;font-size:14px;cursor:pointer}
.music-tabs button.active{color:var(--app-accent);border-bottom-color:var(--app-accent);font-weight:600}
.queue-count{display:inline-flex;align-items:center;justify-content:center;min-width:18px;height:18px;margin-left:4px;padding:0 5px;border-radius:10px;background:var(--app-bg-control);font-size:11px;line-height:18px}
</style>
