<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { useRoute } from 'vue-router'

type Layer = { id: string; type: string; name: string; x: number; y: number; width: number; height: number; z_index: number; visible: boolean; opacity: number; text?: string; url?: string; color?: string; font_size?: number }
type Scene = { id: string; name: string; width: number; height: number; background: string; layers: Layer[] }
type LiveEvent = { id: number; type: string; user_name: string; content: string; gift_name?: string; gift_count?: number; amount?: number; timestamp: number }
const route = useRoute()
const scene = ref<Scene | null>(null)
const liveEvents = ref<LiveEvent[]>([])
const now = ref(new Date())
const error = ref('')
let source: EventSource | undefined
let clockTimer: ReturnType<typeof setInterval> | undefined
let sceneRefreshTimer: ReturnType<typeof setInterval> | undefined

const danmaku = computed(() => liveEvents.value.filter(item => item.type === 'danmaku').slice(-8).reverse())
const gifts = computed(() => liveEvents.value.filter(item => item.type === 'gift').slice(-3).reverse())
const superChats = computed(() => liveEvents.value.filter(item => item.type === 'super_chat').slice(-3).reverse())

function layerStyle(layer: Layer) {
  const current = scene.value
  if (!current) return {}
  return {
    left: (layer.x / current.width * 100) + '%',
    top: (layer.y / current.height * 100) + '%',
    width: (layer.width / current.width * 100) + '%',
    height: (layer.height / current.height * 100) + '%',
    zIndex: layer.z_index,
    opacity: layer.opacity,
    color: layer.color || '#ffffff',
    fontSize: ((layer.font_size || 24) / current.height * 100) + 'cqh',
  }
}
function formatClock() { return now.value.toLocaleTimeString('zh-CN', { hour12: false }) }
function handleEvent(event: MessageEvent<string>) {
  try {
    const value = JSON.parse(event.data) as LiveEvent
    if (liveEvents.value.some(item => item.id === value.id)) return
    liveEvents.value.push(value)
    liveEvents.value.sort((a, b) => a.id - b.id)
    if (liveEvents.value.length > 100) liveEvents.value.splice(0, liveEvents.value.length - 100)
  } catch { /* Ignore malformed event frames. */ }
}
async function loadRecentEvents() {
  try {
    const response = await fetch('/api/events/recent', { cache: 'no-store' })
    if (!response.ok) return
    const items = await response.json() as LiveEvent[]
    const known = new Set(liveEvents.value.map(item => item.id))
    liveEvents.value = [...items.filter(item => !known.has(item.id)), ...liveEvents.value]
      .sort((a, b) => a.id - b.id)
      .slice(-100)
  } catch { /* The live stream remains available if history is unavailable. */ }
}
async function loadScene() {
  const id = encodeURIComponent(String(route.params.sceneID ?? ''))
  try {
    const response = await fetch('/api/scenes/' + id, { cache: 'no-store' })
    if (!response.ok) throw new Error('场景不存在或浏览器源服务不可用')
    scene.value = await response.json() as Scene
    document.title = scene.value.name + ' - My-LiveHub'
    document.documentElement.style.background = 'transparent'
    document.body.style.background = 'transparent'
    const root = document.getElementById('app')
    if (root) root.style.background = 'transparent'
  } catch (cause) { error.value = cause instanceof Error ? cause.message : String(cause) }
}
onMounted(() => {
  void loadScene()
  source = new EventSource('/api/events')
  source.onmessage = handleEvent
  source.onerror = () => { error.value = '直播事件连接中断，正在自动重连…' }
  source.onopen = () => { error.value = ''; void loadRecentEvents() }
  clockTimer = setInterval(() => { now.value = new Date() }, 500)
})
onUnmounted(() => {
  source?.close()
  if (clockTimer) clearInterval(clockTimer)
  if (sceneRefreshTimer) clearInterval(sceneRefreshTimer)
})
</script>

<template>
  <main v-if="scene" class="scene-output" :style="{ aspectRatio: scene.width + ' / ' + scene.height, background: scene.background === 'transparent' ? 'transparent' : scene.background }">
    <div v-for="layer in scene.layers.filter(item => item.visible)" :key="layer.id" class="output-layer" :style="layerStyle(layer)">
      <span v-if="layer.type === 'text'">{{ layer.text }}</span>
      <img v-else-if="layer.type === 'image' && layer.url" :src="layer.url" alt="" />
      <time v-else-if="layer.type === 'clock'">{{ formatClock() }}</time>
      <div v-else-if="layer.type === 'danmaku'" class="event-stack">
        <div v-for="item in danmaku" :key="item.id" class="event-line"><strong>{{ item.user_name }}</strong><span>{{ item.content }}</span></div>
      </div>
      <div v-else-if="layer.type === 'gift'" class="event-stack">
        <div v-for="item in gifts" :key="item.id" class="event-line gift-line"><strong>{{ item.user_name }}</strong><span>赠送 {{ item.gift_name }} × {{ item.gift_count || 1 }}</span></div>
      </div>
      <div v-else-if="layer.type === 'super_chat'" class="event-stack">
        <div v-for="item in superChats" :key="item.id" class="event-line sc-line"><strong>{{ item.user_name }} · ¥{{ Number(item.amount || 0).toFixed(2) }}</strong><span>{{ item.content }}</span></div>
      </div>
    </div>
  </main>
  <main v-else class="scene-error">{{ error || '正在加载场景…' }}</main>
</template>

<style>
html,body,#app{width:100%;height:100%;margin:0;overflow:hidden;background:transparent!important}
body{color:#fff;font-family:Inter,"Segoe UI","Microsoft YaHei",sans-serif}
.scene-output{position:relative;width:100vw;height:100vh;overflow:hidden;container-type:size;background:transparent}
.output-layer{position:absolute;display:flex;align-items:flex-start;justify-content:flex-start;overflow:hidden;white-space:pre-wrap;word-break:break-word;font-weight:600;text-shadow:0 2px 6px #000b}
.output-layer img{width:100%;height:100%;object-fit:contain}
.event-stack{display:flex;flex-direction:column;gap:.35em;width:100%;height:100%;overflow:hidden}
.event-line{display:flex;gap:.5em;align-items:baseline;padding:.2em .4em;border-radius:.3em;background:#10131ac9;font-size:1em;line-height:1.35}
.event-line strong{color:#ffe08a;flex:none}
.event-line span{min-width:0;overflow-wrap:anywhere}
.gift-line{background:#382448dc}.sc-line{background:#603c1edc}
.scene-error{display:grid;place-items:center;width:100vw;height:100vh;background:transparent;color:#fff;font:16px "Microsoft YaHei",sans-serif;text-shadow:0 1px 4px #000}
</style>
