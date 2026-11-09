<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { Window as WailsWindow } from '@wailsio/runtime'
import {
  danmakuDisplaySettings,
  danmakuWindowState,
  loadDanmakuDisplaySettings,
  loadDanmakuDisplayEvents,
  loadDanmakuWindowState,
  type DanmakuDisplayEvent,
} from '../services/danmakuDisplay'

const items = ref<DanmakuDisplayEvent[]>([])
const settings = computed(() => danmakuDisplaySettings.value)
let refreshTimer: ReturnType<typeof setInterval> | undefined
let styleElement: HTMLStyleElement | undefined
let refreshing = false

const platformNames: Record<string, string> = { bilibili: 'B站', douyin: '抖音' }
const eventNames: Record<string, string> = {
  danmaku: '弹幕', gift: '礼物', super_chat: '醒目留言',
  guard: '上舰', follow: '关注', like: '点赞', welcome: '进场', entry: '进场',
}
const visibleItems = computed(() => items.value.slice(-24))

async function refresh() {
  if (refreshing) return
  refreshing = true
  try {
    items.value = await loadDanmakuDisplayEvents()
  } finally {
    refreshing = false
  }
}

watch(danmakuDisplaySettings, value => {
  if (styleElement) styleElement.textContent = value?.custom_css ?? ''
}, { deep: true })

function formatTime(timestamp: number) {
  return new Date(timestamp).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', second: '2-digit' })
}

let resizeStartX = 0
let resizeStartY = 0
let startWidth = 0
let startHeight = 0
async function beginResize(event: PointerEvent) {
  if (event.button !== 0) return
  event.preventDefault()
  const size = await WailsWindow.Size()
  resizeStartX = event.screenX
  resizeStartY = event.screenY
  startWidth = size.width
  startHeight = size.height
  const move = (next: PointerEvent) => {
    void WailsWindow.SetSize(
      Math.max(420, startWidth + next.screenX - resizeStartX),
      Math.max(180, startHeight + next.screenY - resizeStartY),
    )
  }
  const stop = () => {
    window.removeEventListener('pointermove', move)
    window.removeEventListener('pointerup', stop)
    window.removeEventListener('pointercancel', stop)
  }
  window.addEventListener('pointermove', move)
  window.addEventListener('pointerup', stop, { once: true })
  window.addEventListener('pointercancel', stop, { once: true })
}

onMounted(async () => {
  document.documentElement.style.background = 'transparent'
  document.body.style.background = 'transparent'
  const root = document.getElementById('app')
  if (root) root.style.background = 'transparent'
  styleElement = document.createElement('style')
  styleElement.dataset.role = 'danmaku-custom-style'
  document.head.appendChild(styleElement)
  await Promise.all([loadDanmakuDisplaySettings(), loadDanmakuWindowState()])
  await refresh()
  refreshTimer = setInterval(() => {
    void refresh()
    void loadDanmakuWindowState()
  }, 300)
})
onUnmounted(() => {
  if (refreshTimer) clearInterval(refreshTimer)
  styleElement?.remove()
})
</script>

<template>
  <main class="danmaku-overlay" :class="{ pinned: danmakuWindowState.is_pinned }">
    <div class="message-list">
      <article
        v-for="item in visibleItems"
        :key="item.event.id"
        class="message"
        :class="{ highlighted: (item.highlight_until ?? 0) > Date.now(), gift: item.event.type === 'gift' }"
      >
        <img v-if="settings?.show_avatar && item.event.avatar" class="avatar" :src="item.event.avatar" alt="" />
        <div class="message-body">
          <div class="message-meta">
            <span v-if="settings?.show_platform_badge" class="platform-badge">{{ platformNames[item.event.platform_id] ?? item.event.platform_id }}</span>
            <strong class="username">{{ item.event.user_name }}</strong>
            <span class="event-type">{{ eventNames[item.event.type] ?? item.event.type }}</span>
            <time>{{ formatTime(item.event.timestamp) }}</time>
          </div>
          <div class="message-content">{{ item.event.content }}</div>
        </div>
      </article>
</div>
    <div class="resize-handle" title="拖动调整窗口大小" @pointerdown="beginResize"></div>
  </main>
</template>

<style scoped>
:global(html), :global(body), :global(#app) { width:100%; height:100%; margin:0; overflow:hidden; background:transparent !important; }
.danmaku-overlay { --wails-draggable:drag; position:relative; box-sizing:border-box; width:100%; height:100%; overflow:hidden; padding:18px 22px 24px; border:1px solid #8d99b6; border-radius:8px; background:transparent; color:#f2f4fa; font-family:Inter,"Segoe UI","Microsoft YaHei",sans-serif; text-shadow:0 1px 3px #000b; }
.danmaku-overlay.pinned { border:0 !important; outline:0 !important; box-shadow:none !important; border-radius:0; padding:19px 23px 25px; }
.message-list { display:flex; flex-direction:column; justify-content:flex-end; align-items:flex-start; gap:10px; width:100%; height:100%; overflow:hidden; }
.message { display:flex; align-items:flex-start; gap:10px; max-width:100%; padding:8px 12px; border:1px solid #ffffff20; border-radius:10px; background:#111722c9; backdrop-filter:blur(8px); }
.message.highlighted { border-color:#e6b75b; background:#44351bcf; }
.avatar { width:32px; height:32px; flex:0 0 32px; border-radius:50%; object-fit:cover; }
.message-body { min-width:0; }
.message-meta { display:flex; align-items:center; flex-wrap:wrap; gap:7px; font-size:12px; color:#d3d9e8; }
.username { color:#aebaff; font-size:13px; }
.platform-badge { padding:1px 5px; border-radius:4px; background:#46568b; color:#e8ecff; font-size:10px; }
.event-type,time { color:#aab3c6; font-size:11px; }
.message-content { margin-top:4px; overflow-wrap:anywhere; white-space:pre-wrap; font-size:24px; line-height:1.4; }
.empty-state { padding:10px 14px; border-radius:8px; background:#111722a8; color:#d2d8e6; font-size:18px; }
.resize-handle { --wails-draggable:no-drag; position:absolute; right:0; bottom:0; width:22px; height:22px; cursor:nwse-resize; background:linear-gradient(135deg,transparent 48%,#aab5d877 50%,#aab5d877 56%,transparent 58%,transparent 68%,#aab5d877 70%,#aab5d877 76%,transparent 78%); }
.danmaku-overlay.pinned .resize-handle { display:none; }
</style>
