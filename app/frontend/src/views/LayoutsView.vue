<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { CopyDocument, Plus, Delete, Picture, ChatLineRound, Clock, Present, ChatDotRound, Headset, VideoPlay } from '@element-plus/icons-vue'
import { getScenes, saveScene, deleteScene, getBrowserSourceURL, type LayoutScene, type LayoutLayer } from '../services/layouts'
import * as MusicService from '../../bindings/github.com/dongguo4812/My-LiveHub/app/internal/desktop/musicservice.js'
import * as VideoQueueService from '../../bindings/github.com/dongguo4812/My-LiveHub/app/internal/desktop/videoqueueservice.js'
import { getLive2DSettings, listLive2DModels } from '../services/live2d'

type Live2DDisplay = { enabled: boolean; runtime_version: 'moc2' | 'moc3'; model_url: string; model_scale: number; model_offset_x: number; model_offset_y: number }
type Live2DLayer = LayoutLayer & { live2d_scale?: number; live2d_x?: number; live2d_y?: number }
type EmoticonLayer = LayoutLayer & { emoticon_size?: number; emoticon_speed?: number; emoticon_drift?: number; emoticon_max_count?: number }
type GiftLayer = LayoutLayer & { gift_display_duration?: number }
type SuperChatLayer = LayoutLayer & { super_chat_display_duration?: number }
function live2DLayer(layer: LayoutLayer) { return layer as Live2DLayer }
function emoticonLayer(layer: LayoutLayer) { return layer as EmoticonLayer }
function giftLayer(layer: LayoutLayer) { return layer as GiftLayer }
function superChatLayer(layer: LayoutLayer) { return layer as SuperChatLayer }
function live2DScale(layer: LayoutLayer) { return live2DLayer(layer).live2d_scale || 100 }
const live2DDisplay = ref<Live2DDisplay | null>(null)
let live2DRefreshTimer: ReturnType<typeof setInterval> | undefined
let mouthSyncChannel: BroadcastChannel | null = null
let currentMouthOpen = 0
function handleMouthSyncMessage(event: MessageEvent) {
  if (event.data?.type !== 'live2d-mouth-open') return
  currentMouthOpen = Math.max(0, Math.min(1, Number(event.data.value) || 0))
  document.querySelectorAll<HTMLIFrameElement>('.live2d-layer-frame').forEach(frame => {
    ;(frame.contentWindow as (Window & { live2dSetMouthOpen?: (value: number) => void }) | null)?.live2dSetMouthOpen?.(currentMouthOpen)
  })
}
const live2DFrameURL = computed(() => {
  const value = live2DDisplay.value
  if (!value?.enabled || !value.model_url) return ''
  const params = new URLSearchParams({ model: value.model_url, scale: String(value.model_scale), x: String(value.model_offset_x), y: String(value.model_offset_y) })
  return value.runtime_version === 'moc2' ? '/live2d-runtime/moc2/index.html?' + params : '/live2d-runtime/moc3/index.html?model=' + encodeURIComponent(value.model_url)
})
function applyLive2DFrameTransform() {
  document.querySelectorAll<HTMLIFrameElement>('.live2d-layer-frame').forEach(frame => {
    const layer = scene.value?.layers.find(item => item.id === frame.dataset.layerId)
    if (!layer) return
    const value = live2DLayer(layer)
    const target = frame.contentWindow as (Window & { live2dSetModelTransform?: (scale: number, x: number, y: number) => boolean }) | null
    target?.live2dSetModelTransform?.(live2DDisplay.value ? live2DDisplay.value.model_scale * live2DScale(layer) / 100 : live2DScale(layer) / 100, (live2DDisplay.value?.model_offset_x || 0) / 100 + (value.live2d_x || 0) / 100, (live2DDisplay.value?.model_offset_y || 0) / 100 + (value.live2d_y || 0) / 100)
  })
}
function handleLive2DMessage(event: MessageEvent) {
  if (event.data?.type === 'moc3-model-loaded' || event.data?.type === 'moc2-model-loaded') {
    applyLive2DFrameTransform()
    const frame = Array.from(document.querySelectorAll<HTMLIFrameElement>('.live2d-layer-frame')).find(item => item.contentWindow === event.source)
    ;(frame?.contentWindow as (Window & { live2dSetMouthOpen?: (value: number) => void }) | null)?.live2dSetMouthOpen?.(currentMouthOpen)
  }
}
async function loadLive2DDisplay() {
  try {
    const [settings, models] = await Promise.all([getLive2DSettings(), listLive2DModels()])
    const modelID = settings.runtime_version === 'moc2' ? settings.default_model_moc2 : settings.default_model_moc3
    const model = models.find(item => item.format === settings.runtime_version && item.id === modelID)
    live2DDisplay.value = {
      enabled: settings.enabled,
      runtime_version: settings.runtime_version,
      model_url: model?.model_url ?? '',
      model_scale: settings.runtime_version === 'moc2' ? settings.model_scale_moc2 : settings.model_scale_moc3,
      model_offset_x: settings.runtime_version === 'moc2' ? settings.model_offset_x_moc2 : settings.model_offset_x_moc3,
      model_offset_y: settings.runtime_version === 'moc2' ? settings.model_offset_y_moc2 : settings.model_offset_y_moc3,
    }
  } catch { /* Keep the last known Live2D configuration. */ }
}

const scenes = ref<LayoutScene[]>([])
const activeSceneID = ref('')
const selectedLayerID = ref('')
const loading = ref(false)
const musicCommandEnabled = ref<boolean | null>(null)
const videoQueueSnapshot = ref<{ enabled: boolean; current?: { title: string; bvid: string; requester: string; duration_seconds: number }; items: Array<{ id: number; title: string; bvid: string; requester: string; duration_seconds: number }>; queue_count: number } | null>(null)
let musicConfigRefreshTimer: ReturnType<typeof setInterval> | undefined
let videoQueueRefreshTimer: ReturnType<typeof setInterval> | undefined
const sourceURL = ref('')
const canvas = ref<HTMLElement>()
const scene = computed(() => scenes.value.find(item => item.id === activeSceneID.value) ?? scenes.value[0] ?? null)
const selectedLayer = computed(() => scene.value?.layers.find(item => item.id === selectedLayerID.value) ?? null)
const selectedQueueLayer = computed(() => selectedLayer.value as (LayoutLayer & { queue_display_count?: number }) | null)
const selectedEmoticonLayer = computed(() => selectedLayer.value?.type === 'emoticon' ? emoticonLayer(selectedLayer.value) : null)
const sortedLayers = computed(() => [...(scene.value?.layers ?? [])].sort((a, b) => b.z_index - a.z_index))
const scale = ref(1)
let drag: { x: number; y: number; layerX: number; layerY: number } | null = null
let live2DSaveQueue: Promise<void> = Promise.resolve()

function refreshScale() {
  if (canvas.value && scene.value) scale.value = canvas.value.clientWidth / scene.value.width
}
function makeID(prefix: string) { return prefix + '-' + Date.now().toString(36) }
function addLayer(type: string) {
  if (!scene.value) return
  const defaults: Record<string, Partial<LayoutLayer>> = {
    text: { name: '文字', text: '双击编辑文字', width: 480, height: 80, font_size: 36, color: '#ffffff' },
    image: { name: '图片', url: 'https://', width: 240, height: 160 },
    clock: { name: '时钟', width: 240, height: 64, font_size: 32, color: '#ffffff' },
    danmaku: { name: '弹幕列表', width: 760, height: 260, font_size: 24, color: '#ffffff' },
    gift: { name: '礼物展示', width: 520, height: 180, font_size: 28, color: '#ffffff' },
    super_chat: { name: 'SC 展示', width: 520, height: 180, font_size: 28, color: '#ffffff' },
    music: { name: '音乐展示', width: 620, height: 190, font_size: 24, color: '#ffffff' },
    music_queue: { name: '歌曲点播队列', width: 500, height: 360, font_size: 22, color: '#ffffff' },
    video_queue: { name: '视频点播队列', width: 500, height: 360, font_size: 22, color: '#ffffff' },
    live2d: { name: 'Live2D', width: 520, height: 760, color: '#ffffff' },
    emoticon: { name: '互动表情', width: 520, height: 420, color: '#ffffff' },
    lol_hex_display: { name: '海克斯展示', url: 'http://127.0.0.1:8767/display', width: 520, height: 420, color: '#ffffff' },
    lol_hex_bar: { name: 'LOL 战绩条', url: 'http://127.0.0.1:8767/bar', width: 1000, height: 180, color: '#ffffff' },
  }
  const base = defaults[type]
  if (!base) return
  const layer = { id: makeID(type), type, name: base.name ?? type, x: 100, y: 120, width: base.width ?? 300, height: base.height ?? 100, z_index: scene.value.layers.length + 1, visible: true, opacity: 1, text: base.text ?? '', url: base.url ?? '', color: base.color ?? '#ffffff', font_size: base.font_size ?? 24 } as LayoutLayer
  if (type === 'music_queue' || type === 'video_queue') (layer as LayoutLayer & { queue_display_count?: number }).queue_display_count = 10
  if (type === 'live2d') Object.assign(layer, { live2d_scale: 100, live2d_x: 0, live2d_y: 0 })
  if (type === 'emoticon') Object.assign(layer, { emoticon_size: 64, emoticon_speed: 90, emoticon_drift: 48, emoticon_max_count: 12 })
  if (type === 'gift') Object.assign(layer, { gift_display_duration: 8 })
  if (type === 'super_chat') Object.assign(layer, { super_chat_display_duration: 8 })
  scene.value.layers.push(layer)
  selectedLayerID.value = layer.id
}
function selectLayer(layer: LayoutLayer) {
  selectedLayerID.value = layer.id
  if (layer.type === 'live2d') {
    const value = live2DLayer(layer)
    value.live2d_scale ??= 100
    value.live2d_x ??= 0
    value.live2d_y ??= 0
  }
  if (layer.type === 'music_queue' || layer.type === 'video_queue') {
    layer.name = layer.type === 'music_queue' ? '歌曲点播队列' : '视频点播队列'
    const queueLayer = layer as LayoutLayer & { queue_display_count?: number }
    if (!queueLayer.queue_display_count || queueLayer.queue_display_count < 1) queueLayer.queue_display_count = 10
  }
}
function removeLayer() {
  if (!scene.value || !selectedLayer.value) return
  scene.value.layers = scene.value.layers.filter(layer => layer.id !== selectedLayerID.value)
  selectedLayerID.value = ''
}
function persistLive2DLayer(layer: LayoutLayer) {
  const sceneID = scene.value?.id
  if (!sceneID || layer.type !== 'live2d') return
  applyLive2DFrameTransform()
  const saveCurrentScene = async () => {
    const currentScene = scenes.value.find(item => item.id === sceneID)
    if (!currentScene) return
    const snapshot = JSON.parse(JSON.stringify(currentScene)) as LayoutScene
    const saved = await saveScene(snapshot)
    const index = scenes.value.findIndex(item => item.id === saved.id)
    if (index >= 0) scenes.value[index] = saved
  }
  live2DSaveQueue = live2DSaveQueue.then(saveCurrentScene, saveCurrentScene).catch(error => {
    ElMessage.error(error instanceof Error ? error.message : String(error))
  })
  return live2DSaveQueue
}
function startDrag(event: PointerEvent, layer: LayoutLayer) {
  if (!canvas.value || event.button !== 0) return
  selectedLayerID.value = layer.id
  if (layer.type === 'live2d') {
    const value = live2DLayer(layer)
    value.live2d_scale ??= 100
    value.live2d_x ??= 0
    value.live2d_y ??= 0
  }
  drag = { x: event.clientX, y: event.clientY, layerX: layer.x, layerY: layer.y }
  const move = (next: PointerEvent) => {
    if (!drag || !scene.value) return
    layer.x = Math.max(0, Math.min(scene.value.width - layer.width, drag.layerX + (next.clientX - drag.x) / scale.value))
    layer.y = Math.max(0, Math.min(scene.value.height - layer.height, drag.layerY + (next.clientY - drag.y) / scale.value))
  }
  const stop = () => {
    drag = null
    window.removeEventListener('pointermove', move)
    window.removeEventListener('pointerup', stop)
    window.removeEventListener('pointercancel', stop)
  }
  event.preventDefault()
  window.addEventListener('pointermove', move)
  window.addEventListener('pointerup', stop, { once: true })
  window.addEventListener('pointercancel', stop, { once: true })
}
async function load() {
  loading.value = true
  try {
    scenes.value = await getScenes()
    activeSceneID.value = scenes.value[0]?.id ?? ''
    await refreshURL()
    await nextTick()
    refreshScale()
  } catch (error) { ElMessage.error(error instanceof Error ? error.message : String(error)) }
  finally { loading.value = false }
}
async function selectScene() {
  selectedLayerID.value = ''
  await refreshURL()
  await nextTick()
  refreshScale()
}
async function save() {
  if (!scene.value) return
  loading.value = true
  try {
    const saved = await saveScene(JSON.parse(JSON.stringify(scene.value)) as LayoutScene)
    const index = scenes.value.findIndex(item => item.id === saved.id)
    if (index >= 0) scenes.value[index] = saved
    ElMessage.success('场景已保存')
    await refreshURL()
  } catch (error) { ElMessage.error(error instanceof Error ? error.message : String(error)) }
  finally { loading.value = false }
}
async function refreshURL() {
  if (!scene.value) return
  try { sourceURL.value = await getBrowserSourceURL(scene.value.id) }
  catch { sourceURL.value = '' }
}
function preview() { if (sourceURL.value) window.open(sourceURL.value, '_blank', 'noopener') }
async function copyURL() {
  if (!sourceURL.value) return
  await navigator.clipboard.writeText(sourceURL.value)
  ElMessage.success('浏览器源地址已复制')
}
async function addScene() {
  const id = makeID('scene')
  const next: LayoutScene = { id, name: '新场景', width: 1920, height: 1080, background: 'transparent', layers: [] }
  try {
    await saveScene(next)
    scenes.value.push(next)
    activeSceneID.value = id
    selectedLayerID.value = ''
    await refreshURL()
  } catch (error) { ElMessage.error(error instanceof Error ? error.message : String(error)) }
}
async function removeScene() {
  if (!scene.value || scenes.value.length <= 1) return
  try {
    await ElMessageBox.confirm('删除后无法恢复，确定删除当前场景吗？', '删除场景', { type: 'warning' })
    await deleteScene(scene.value.id)
    scenes.value = scenes.value.filter(item => item.id !== activeSceneID.value)
    activeSceneID.value = scenes.value[0]?.id ?? ''
    selectedLayerID.value = ''
    await refreshURL()
  } catch (error) {
    if (error !== 'cancel' && error !== 'close') ElMessage.error(error instanceof Error ? error.message : String(error))
  }
}
function layerLabel(type: string) {
  return ({ text: '文字', image: '图片', clock: '时钟', danmaku: '弹幕', gift: '礼物', super_chat: 'SC', music: '音乐', music_queue: '歌曲点播队列', video_queue: '视频点播队列', live2d: 'Live2D', emoticon: '互动表情', lol_hex_display: '海克斯展示', lol_hex_bar: 'LOL 战绩条' } as Record<string, string>)[type] ?? type
}
function videoQueuePreviewItems(layer: LayoutLayer) {
  const count = (layer as LayoutLayer & { queue_display_count?: number }).queue_display_count || 10
  return (videoQueueSnapshot.value?.items ?? []).slice(0, count)
}
async function loadVideoQueueSnapshot() {
  try {
    videoQueueSnapshot.value = await VideoQueueService.GetSnapshot() as typeof videoQueueSnapshot.value
  } catch { /* Keep the last known video queue snapshot. */ }
}
async function loadMusicCommandConfig() {
  try {
    const config = await MusicService.GetConfig()
    musicCommandEnabled.value = config.command_enabled === true
  } catch { /* Keep the last known command setting. */ }
}
onMounted(() => {
  mouthSyncChannel = typeof BroadcastChannel !== 'undefined' ? new BroadcastChannel('my-livehub-live2d-mouth') : null
  if (mouthSyncChannel) mouthSyncChannel.onmessage = handleMouthSyncMessage
  void load()
  void loadMusicCommandConfig()
  void loadVideoQueueSnapshot()
  void loadLive2DDisplay()
  live2DRefreshTimer = setInterval(() => { void loadLive2DDisplay() }, 3000)
  window.addEventListener('message', handleLive2DMessage)
  musicConfigRefreshTimer = setInterval(() => { void loadMusicCommandConfig() }, 3000)
  videoQueueRefreshTimer = setInterval(() => { void loadVideoQueueSnapshot() }, 1000)
  window.addEventListener('resize', refreshScale)
})
onUnmounted(() => {
  mouthSyncChannel?.close()
  mouthSyncChannel = null
  window.removeEventListener('resize', refreshScale)
  if (musicConfigRefreshTimer) clearInterval(musicConfigRefreshTimer)
  if (videoQueueRefreshTimer) clearInterval(videoQueueRefreshTimer)
  if (live2DRefreshTimer) clearInterval(live2DRefreshTimer)
  window.removeEventListener('message', handleLive2DMessage)
})
</script>

<template>
  <section class="layouts-page">
    <header class="page-heading">
      <div><p class="eyebrow">SCENE STUDIO</p><h2>综合展示</h2><p class="description">设计独立直播场景，通过 OBS 浏览器源实时呈现。画布默认 1920 × 1080，支持透明背景。</p></div>
      <div class="heading-actions"><el-button @click="addScene"><el-icon><Plus /></el-icon>新建场景</el-button><el-button type="primary" :loading="loading" @click="save">保存场景</el-button></div>
    </header>
    <div class="scene-toolbar">
      <div class="scene-tabs">
        <el-select v-model="activeSceneID" style="width:190px" @change="selectScene">
          <el-option v-for="item in scenes" :key="item.id" :label="item.name" :value="item.id" />
        </el-select>
        <el-input v-if="scene" v-model="scene.name" size="default" style="width:150px" placeholder="场景名称" />
        <el-input v-if="scene" v-model="scene.background" size="default" style="width:150px" placeholder="transparent 或 #000000" />
        <el-button text type="danger" :disabled="scenes.length <= 1" @click="removeScene"><el-icon><Delete /></el-icon></el-button>
      </div>
      <div class="source-actions">
        <el-tag type="success" effect="plain">浏览器源服务运行中</el-tag>
        <el-button :disabled="!sourceURL" @click="copyURL"><el-icon><CopyDocument /></el-icon>复制 OBS 地址</el-button>
        <el-button :disabled="!sourceURL" @click="preview">预览</el-button>
      </div>
    </div>
    <div class="studio-grid" v-loading="loading">
      <aside class="studio-panel layer-panel">
        <div class="panel-heading"><strong>组件</strong><span>添加到画布</span></div>
        <div class="add-grid">
          <button @click="addLayer('text')"><el-icon><ChatLineRound /></el-icon>文字</button>
          <button @click="addLayer('image')"><el-icon><Picture /></el-icon>图片</button>
          <button @click="addLayer('clock')"><el-icon><Clock /></el-icon>时钟</button>
          <button @click="addLayer('danmaku')"><el-icon><ChatDotRound /></el-icon>弹幕</button>
          <button @click="addLayer('gift')"><el-icon><Present /></el-icon>礼物</button>
          <button @click="addLayer('super_chat')"><el-icon><ChatDotRound /></el-icon>SC 留言</button>
          <button @click="addLayer('music')"><el-icon><Headset /></el-icon>音乐展示</button>
          <button @click="addLayer('music_queue')">歌曲点播队列</button>
          <button @click="addLayer('video_queue')"><el-icon><VideoPlay /></el-icon>视频点播队列</button>
          <button @click="addLayer('live2d')">Live2D</button>
          <button @click="addLayer('emoticon')">互动表情</button>
          <button @click="addLayer('lol_hex_display')">海克斯展示</button>
          <button @click="addLayer('lol_hex_bar')">LOL 战绩条</button>
        </div>
        <el-divider />
        <div class="panel-heading"><strong>图层</strong><span>{{ scene?.layers.length ?? 0 }} 个</span></div>
        <div class="layer-list">
          <button v-for="layer in sortedLayers" :key="layer.id" :class="{ selected: selectedLayerID === layer.id }" @click="selectLayer(layer)">
            <span>{{ layer.type === 'music_queue' || layer.type === 'video_queue' ? layerLabel(layer.type) : (layer.name || layerLabel(layer.type)) }}</span><small>{{ layerLabel(layer.type) }}</small>
          </button>
          <el-empty v-if="!scene?.layers.length" description="添加组件开始设计" :image-size="64" />
        </div>
      </aside>
      <main class="canvas-area">
        <div class="canvas-caption"><span>画布预览</span><span>{{ scene?.width }} × {{ scene?.height }} · 16:9</span></div>
        <div ref="canvas" class="scene-canvas" :style="{ background: scene?.background === 'transparent' ? 'transparent' : scene?.background }" @click.self="selectedLayerID=''">
          <div class="checkerboard"></div>
          <div v-for="layer in scene?.layers.filter(item => item.visible && (item.type !== 'music_queue' || musicCommandEnabled === true) && (item.type !== 'video_queue' || videoQueueSnapshot?.enabled === true))" :key="layer.id" class="canvas-layer" :class="{ active: selectedLayerID === layer.id, 'live2d-canvas-layer': layer.type === 'live2d' }"
            :style="{ left: (layer.x / (scene?.width ?? 1920) * 100) + '%', top: (layer.y / (scene?.height ?? 1080) * 100) + '%', width: (layer.width / (scene?.width ?? 1920) * 100) + '%', height: (layer.height / (scene?.height ?? 1080) * 100) + '%', zIndex: layer.z_index, opacity: layer.opacity, color: layer.color || '#fff', fontSize: ((layer.font_size || 24) / (scene?.height ?? 1080) * 100) + 'cqh' }"
            @pointerdown="startDrag($event, layer)">
            <span v-if="layer.type === 'text'">{{ layer.text }}</span>
            <span v-else-if="layer.type === 'clock'">12:34:56</span>
            <img v-else-if="layer.type === 'image' && layer.url && layer.url !== 'https://'" :src="layer.url" alt="" />
            <div v-else-if="layer.type === 'lol_hex_display'" class="extension-layer-preview">LOL HEX<br><strong>海克斯强化展示</strong></div>
            <div v-else-if="layer.type === 'lol_hex_bar'" class="extension-layer-preview">LOL HEX<br><strong>对局战绩条</strong></div>
            <iframe v-else-if="layer.type === 'live2d' && live2DFrameURL" class="live2d-layer-frame" :data-layer-id="layer.id" :src="live2DFrameURL" title="Live2D 预览" allowtransparency="true" :style="{ backgroundColor: 'transparent' }" @load="applyLive2DFrameTransform" />
            <span v-else-if="layer.type === 'live2d'" class="live2d-layer-empty">请先在 Live2D 页面配置并启用默认模型</span>
            <div v-else-if="layer.type === 'music'" class="music-layer-preview"><div class="music-cover-preview">♫</div><div><strong>歌曲名称 · 歌手</strong><span>━━━━━━━●━━━━  01:23 / 04:12</span><p>当前歌词 · 歌词翻译</p></div></div>
            <div v-else-if="layer.type === 'video_queue'" class="video-queue-layer-preview">
              <div class="queue-preview-prompt"><strong>▸ 视频点播指令</strong><span>点播 BV号</span></div>
              <div class="queue-preview-current"><strong>▶ 当前播放</strong><span>{{ videoQueueSnapshot?.current?.title || '暂无正在播放的视频' }} <small v-if="videoQueueSnapshot?.current">{{ videoQueueSnapshot.current.requester }}</small></span></div>
              <div class="queue-preview-pending"><strong>Ⅱ 待播队列 <small>{{ videoQueueSnapshot?.queue_count ?? 0 }} 个</small></strong>
                <span v-for="(item, index) in videoQueuePreviewItems(layer)" :key="item.id">{{ String(index + 1).padStart(2, '0') }}　{{ item.title }} <small>{{ item.requester }}</small></span>
                <span v-if="!videoQueueSnapshot?.items.length">暂无待播视频</span>
              </div>
            </div>
            <div v-else-if="layer.type === 'gift'" class="gift-layer-preview"><div class="gift-preview-image">🎁</div><strong>礼物名称 × 1</strong><span>观众昵称 赠送</span></div>
            <div v-else-if="layer.type === 'emoticon'" class="emoticon-layer-preview"><span>🥰</span><span>✨</span><span>🎉</span><span>❤️</span></div>
            <div v-else-if="layer.type === 'music_queue'" class="music-queue-layer-preview">
              <div class="queue-preview-prompt"><strong>✦ 点歌提示</strong><span>点歌：点歌 歌曲名称</span><span>切歌：切歌（主播 / 房管）</span></div>
              <div class="queue-preview-current"><strong>♫ 当前播放</strong><span>歌曲名称 · 歌手 <small>观众昵称</small></span></div>
              <div class="queue-preview-pending"><strong>Ⅱ 待播队列 <small>2 首</small></strong><span>01　歌曲名称 · 歌手 <small>观众昵称</small></span><span>02　歌曲名称 · 歌手 <small>观众昵称</small></span></div>
            </div>
            <span v-else>{{ layerLabel(layer.type) }}组件</span>
          </div>
        </div>
        <div class="canvas-hint">选择 Live2D 图层后，在右侧属性中调整模型大小和位置</div>
      </main>
      <aside class="studio-panel property-panel">
        <div class="panel-heading"><strong>属性</strong><el-button v-if="selectedLayer" text type="danger" @click="removeLayer">删除图层</el-button></div>
        <template v-if="selectedLayer !== null">
          <el-form label-position="top" size="small">
            <el-form-item label="图层名称"><el-input v-model="selectedLayer!.name" /></el-form-item>
            <el-form-item v-if="selectedLayer!.type === 'text'" label="文字内容"><el-input v-model="selectedLayer!.text" type="textarea" :rows="3" /></el-form-item>
            <el-form-item v-if="selectedLayer!.type === 'image'" label="图片 URL"><el-input v-model="selectedLayer!.url" placeholder="https://..." /></el-form-item>
            <el-form-item v-if="selectedLayer!.type === 'lol_hex_display' || selectedLayer!.type === 'lol_hex_bar'" label="扩展页面 URL"><el-input v-model="selectedLayer!.url" placeholder="http://127.0.0.1:8767/..." /></el-form-item>
            <el-form-item v-if="selectedLayer !== null && (selectedLayer!.type === 'music_queue' || selectedLayer!.type === 'video_queue')" label="待播显示数量"><el-input-number v-model="selectedQueueLayer!.queue_display_count" :min="1" :max="1000" /><div class="el-form-item__description">仅控制此队列组件显示的待播条数；队列标题仍显示实际数量。</div></el-form-item>
            <el-form-item v-if="selectedLayer!.type === 'gift'" label="显示时间"><el-input-number v-model="giftLayer(selectedLayer!).gift_display_duration" :min="1" :max="120" :step="1" controls-position="right" /> <span class="el-form-item__description">礼物出现后保留的秒数（1–120 秒）</span></el-form-item>
            <el-form-item v-if="selectedLayer!.type === 'super_chat'" label="显示时间"><el-input-number v-model="superChatLayer(selectedLayer!).super_chat_display_duration" :min="1" :max="120" :step="1" controls-position="right" /> <span class="el-form-item__description">SC 留言出现后保留的秒数（1–120 秒）</span></el-form-item>
            <template v-if="selectedEmoticonLayer">
              <el-form-item label="表情大小"><div class="live2d-adjustment"><strong>{{ selectedEmoticonLayer.emoticon_size ?? 64 }} px</strong><el-slider v-model="selectedEmoticonLayer.emoticon_size" :min="24" :max="160" :step="4" /></div></el-form-item>
              <el-form-item label="上升速度"><div class="live2d-adjustment"><strong>{{ selectedEmoticonLayer.emoticon_speed ?? 90 }} px/s</strong><el-slider v-model="selectedEmoticonLayer.emoticon_speed" :min="20" :max="300" :step="5" /></div></el-form-item>
              <el-form-item label="左右飘动幅度"><div class="live2d-adjustment"><strong>{{ selectedEmoticonLayer.emoticon_drift ?? 48 }} px</strong><el-slider v-model="selectedEmoticonLayer.emoticon_drift" :min="0" :max="120" :step="4" /></div></el-form-item>
              <el-form-item label="同时显示数量"><el-input-number v-model="selectedEmoticonLayer.emoticon_max_count" :min="1" :max="40" /></el-form-item>
            </template>
            <div class="property-grid">
              <el-form-item label="X"><el-input-number v-model="selectedLayer!.x" :min="0" :max="scene?.width ?? 1920" controls-position="right" /></el-form-item>
              <el-form-item label="Y"><el-input-number v-model="selectedLayer!.y" :min="0" :max="scene?.height ?? 1080" controls-position="right" /></el-form-item>
              <el-form-item label="宽度"><el-input-number v-model="selectedLayer!.width" :min="20" :max="scene?.width ?? 1920" controls-position="right" /></el-form-item>
              <el-form-item label="高度"><el-input-number v-model="selectedLayer!.height" :min="20" :max="scene?.height ?? 1080" controls-position="right" /></el-form-item>
            </div>
            <template v-if="selectedLayer!.type === 'live2d'">
              <el-form-item label="模型大小"><div class="live2d-adjustment"><strong>{{ live2DScale(selectedLayer!) }}%</strong><el-slider v-model="live2DLayer(selectedLayer!).live2d_scale" :min="10" :max="500" :step="5" @input="applyLive2DFrameTransform" @change="persistLive2DLayer(selectedLayer!)" /></div></el-form-item>
              <el-form-item label="水平位置"><div class="live2d-adjustment"><strong>{{ live2DLayer(selectedLayer!).live2d_x ?? 0 }}%</strong><el-slider v-model="live2DLayer(selectedLayer!).live2d_x" :min="-200" :max="200" :step="1" @input="applyLive2DFrameTransform" @change="persistLive2DLayer(selectedLayer!)" /></div></el-form-item>
              <el-form-item label="垂直位置"><div class="live2d-adjustment"><strong>{{ live2DLayer(selectedLayer!).live2d_y ?? 0 }}%</strong><el-slider v-model="live2DLayer(selectedLayer!).live2d_y" :min="-200" :max="200" :step="1" @input="applyLive2DFrameTransform" @change="persistLive2DLayer(selectedLayer!)" /></div></el-form-item>
            </template>
            <el-form-item v-if="selectedLayer!.type !== 'live2d'" label="字体大小"><el-input-number v-model="selectedLayer!.font_size" :min="10" :max="160" /></el-form-item>
            <el-form-item v-if="selectedLayer!.type !== 'live2d'" label="文字颜色"><el-color-picker v-model="selectedLayer!.color" show-alpha /></el-form-item>
            <el-form-item label="透明度"><el-slider v-model="selectedLayer!.opacity" :min="0" :max="1" :step="0.05" /></el-form-item>
            <el-form-item label="显示图层"><el-switch v-model="selectedLayer!.visible" /></el-form-item>
          </el-form>
        </template>
        <el-empty v-else description="选择一个图层编辑属性" :image-size="72" />
      </aside>
    </div>
    <el-card shadow="never" class="obs-card">
      <div class="obs-card-content"><div><strong>OBS 浏览器源</strong><p>将下面地址添加到 OBS → 来源 → 浏览器。建议宽度 1920、高度 1080；透明背景由场景自动提供。</p><code>{{ sourceURL || '正在获取浏览器源地址…' }}</code></div><el-button type="primary" :disabled="!sourceURL" @click="copyURL">复制地址</el-button></div>
    </el-card>
  </section>
</template>

<style scoped>
.extension-layer-preview { display: grid; place-content: center; gap: 8px; width: 100%; height: 100%; border: 1px dashed rgba(255,255,255,.35); border-radius: 10px; color: #b9c8dc; background: rgba(20,28,42,.76); text-align: center; font-size: 13px; letter-spacing: 2px; }
.extension-layer-preview strong { font-size: 1.35em; letter-spacing: 0; }
.layouts-page{display:flex;flex-direction:column;gap:16px;min-width:0}.page-heading{display:flex;justify-content:space-between;gap:20px;align-items:flex-start}.page-heading h2{margin:0;font-size:25px}.eyebrow{margin:0 0 8px;color:var(--app-accent);font-size:11px;letter-spacing:.15em}.description{color:var(--el-text-color-secondary);font-size:13px;margin:8px 0 0}.heading-actions,.scene-tabs,.source-actions{display:flex;align-items:center;gap:9px;flex-wrap:wrap}.scene-toolbar{display:flex;justify-content:space-between;align-items:center;gap:12px;padding:12px 14px;border:1px solid var(--app-border);border-radius:9px;background:var(--app-bg-surface)}.studio-grid{display:grid;grid-template-columns:210px minmax(0,1fr) 250px;gap:14px;min-height:550px}.studio-panel{border:1px solid var(--app-border);border-radius:9px;background:var(--app-bg-surface);padding:13px;min-width:0}.panel-heading{display:flex;align-items:center;justify-content:space-between;margin-bottom:12px;font-size:13px}.panel-heading span{font-size:11px;color:var(--el-text-color-secondary)}.add-grid{display:grid;grid-template-columns:1fr 1fr;gap:7px}.add-grid button{height:55px;display:flex;flex-direction:column;align-items:center;justify-content:center;gap:5px;border:1px solid var(--app-border);border-radius:7px;background:var(--app-bg-control);color:var(--el-text-color-regular);cursor:pointer;font-size:11px}.add-grid button:hover{border-color:var(--app-accent);color:var(--app-accent)}.add-grid .el-icon{font-size:17px}.layer-list{display:flex;flex-direction:column;gap:4px}.layer-list button{display:flex;justify-content:space-between;align-items:center;padding:9px;border:1px solid transparent;border-radius:6px;background:transparent;color:var(--el-text-color-regular);text-align:left;cursor:pointer;font-size:12px}.layer-list button.selected{background:var(--app-bg-selected);border-color:var(--app-accent);color:var(--app-accent-soft)}.layer-list small{color:var(--el-text-color-secondary)}.canvas-area{min-width:0;display:flex;flex-direction:column;justify-content:center;gap:9px}.canvas-caption{display:flex;justify-content:space-between;color:var(--el-text-color-secondary);font-size:11px}.scene-canvas{position:relative;container-type:size;width:100%;aspect-ratio:16/9;overflow:hidden;border:1px solid var(--app-border);border-radius:6px;box-shadow:0 8px 28px #0002}.checkerboard{position:absolute;inset:0;pointer-events:none;background-image:linear-gradient(45deg,#80808018 25%,transparent 25%),linear-gradient(-45deg,#80808018 25%,transparent 25%),linear-gradient(45deg,transparent 75%,#80808018 75%),linear-gradient(-45deg,transparent 75%,#80808018 75%);background-size:24px 24px;background-position:0 0,0 12px,12px -12px,-12px 0}.canvas-layer{position:absolute;display:flex;align-items:center;justify-content:flex-start;padding:4px;border:1px dashed transparent;cursor:move;overflow:hidden;white-space:pre-wrap;word-break:break-word;font-weight:600;text-shadow:0 2px 5px #000a;touch-action:none} .gift-layer-preview{display:flex;flex-direction:column;align-items:center;justify-content:center;gap:3px;width:100%;height:100%;overflow:hidden;border:1px dashed #d6a5ff66;border-radius:6px;background:linear-gradient(180deg,#38244855,#17132188);font-size:clamp(10px,2.2cqh,20px);text-align:center;text-shadow:none}.gift-preview-image{display:grid;place-items:center;flex:1;min-height:0;font-size:clamp(28px,8cqh,76px)}.gift-layer-preview strong{max-width:100%;overflow:hidden;text-overflow:ellipsis;white-space:nowrap;color:#ffe1a6}.gift-layer-preview span{max-width:100%;overflow:hidden;text-overflow:ellipsis;white-space:nowrap;color:#eee0f5;font-size:.78em}.emoticon-layer-preview{position:relative;display:flex;align-items:center;justify-content:space-around;width:100%;height:100%;overflow:hidden;background:linear-gradient(180deg,#ffffff08,#ffffff18);border:1px dashed #ffffff45;border-radius:6px}
.emoticon-layer-preview span{font-size:clamp(20px,5cqh,54px);animation:emoticon-preview-float 3s ease-in-out infinite alternate}
.emoticon-layer-preview span:nth-child(2){animation-delay:-.8s}.emoticon-layer-preview span:nth-child(3){animation-delay:-1.6s}.emoticon-layer-preview span:nth-child(4){animation-delay:-2.2s}
@keyframes emoticon-preview-float{from{transform:translateY(22px) translateX(-8px);opacity:.45}to{transform:translateY(-22px) translateX(8px);opacity:1}}
.canvas-layer.active{border-color:#8493ff;background:#8290ff18}.canvas-layer img,.canvas-layer iframe{width:100%;height:100%;object-fit:contain;border:0;background:transparent;display:block}.canvas-layer img{width:100%;height:100%;object-fit:contain}.music-layer-preview{display:flex;align-items:center;gap:12px;width:100%;height:100%;padding:8px;border-radius:9px;background:#151923d9;color:#fff;font-size:12px;text-shadow:none;overflow:hidden}.music-cover-preview{display:grid;place-items:center;flex:none;width:30%;height:100%;border-radius:6px;background:#343b4c;font-size:32px}.music-layer-preview>div:last-child{display:flex;flex-direction:column;gap:7px;min-width:0}.music-layer-preview strong,.music-layer-preview span,.music-layer-preview p{overflow:hidden;text-overflow:ellipsis;white-space:nowrap;margin:0;font-size:12px}.music-layer-preview span{color:#c6cad4;font-size:10px}.music-layer-preview p{color:#fff;font-size:11px}.music-queue-layer-preview{display:flex;flex-direction:column;gap:5px;width:100%;height:100%;padding:8px;border-radius:8px;background:linear-gradient(135deg,#141a26ed,#0d111acc);color:#f4f6fb;font-size:12px;text-shadow:none;overflow:hidden}.music-queue-layer-preview>div{display:flex;flex-direction:column;gap:3px;min-width:0;padding:5px 7px;border:1px solid #9fc0ff2b;border-radius:6px;background:#ffffff09}.music-queue-layer-preview strong{padding-bottom:3px;border-bottom:1px solid #9fc0ff44;color:#9fc0ff;font-size:12px}.music-queue-layer-preview span{display:flex;justify-content:space-between;gap:6px;overflow:hidden;text-overflow:ellipsis;white-space:nowrap;color:#d8dce6;font-size:10px}.music-queue-layer-preview small{color:#65b8ff;font-size:9px}.music-queue-layer-preview .queue-preview-prompt{background:#30271999;border-color:#ffd66b44}.music-queue-layer-preview .queue-preview-prompt strong{color:#ffd66b;border-color:#ffd66b44}.music-queue-layer-preview .queue-preview-current{border-color:#ffd66b44}.music-queue-layer-preview .queue-preview-current strong{color:#ffd66b} .video-queue-layer-preview{display:flex;flex-direction:column;gap:5px;width:100%;height:100%;padding:8px;border-radius:8px;background:linear-gradient(135deg,#141a26ed,#0d111acc);color:#f4f6fb;font-size:12px;text-shadow:none;overflow:hidden}.video-queue-layer-preview>div{display:flex;flex-direction:column;gap:3px;min-width:0;padding:5px 7px;border:1px solid #9fc0ff2b;border-radius:6px;background:#ffffff09}.video-queue-layer-preview strong{padding-bottom:3px;border-bottom:1px solid #9fc0ff44;color:#9fc0ff;font-size:12px}.video-queue-layer-preview span{display:flex;justify-content:space-between;gap:6px;overflow:hidden;text-overflow:ellipsis;white-space:nowrap;color:#d8dce6;font-size:10px}.video-queue-layer-preview small{color:#65b8ff;font-size:9px}.video-queue-layer-preview .queue-preview-prompt{background:#30271999;border-color:#ffd66b44}.video-queue-layer-preview .queue-preview-prompt strong{color:#ffd66b;border-color:#ffd66b44}.video-queue-layer-preview .queue-preview-current{border-color:#ffd66b44}.video-queue-layer-preview .queue-preview-current strong{color:#ffd66b}.canvas-hint{text-align:center;color:var(--el-text-color-secondary);font-size:11px}.property-panel .el-form-item{margin-bottom:12px}.live2d-adjustment{width:100%;display:flex;flex-direction:column;gap:2px}.live2d-adjustment strong{align-self:flex-end;color:var(--el-text-color-secondary);font-size:11px;font-weight:500}.property-grid{display:grid;grid-template-columns:1fr 1fr;gap:0 8px}.property-grid .el-input-number{width:100%}.obs-card-content{display:flex;align-items:center;justify-content:space-between;gap:16px}.obs-card-content p{margin:6px 0 9px;color:var(--el-text-color-secondary);font-size:12px}.obs-card-content code{font-size:11px;word-break:break-all;color:var(--app-accent-soft)}@media(max-width:1100px){.studio-grid{grid-template-columns:180px minmax(0,1fr)}.property-panel{grid-column:1/-1}.property-grid{grid-template-columns:repeat(4,1fr)}}@media(max-width:760px){.page-heading,.scene-toolbar{align-items:flex-start;flex-direction:column}.studio-grid{grid-template-columns:1fr}.canvas-area{grid-row:1}.property-panel{grid-column:auto}.layer-panel{order:2}.obs-card-content{align-items:flex-start;flex-direction:column}}
.canvas-layer{position:absolute}
.canvas-layer.live2d-canvas-layer{cursor:move}.canvas-layer iframe.live2d-layer-frame{display:block;pointer-events:none;width:100%;height:100%;border:0;background:transparent!important;background-color:transparent!important;color-scheme:normal;pointer-events:none}.live2d-layer-empty{display:grid;place-items:center;width:100%;height:100%;font-size:12px;color:#aeb9ca;text-shadow:none;text-align:center}
</style>
