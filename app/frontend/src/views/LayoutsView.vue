<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { CopyDocument, Plus, Delete, Picture, ChatLineRound, Clock, Present, ChatDotRound, Headset } from '@element-plus/icons-vue'
import { getScenes, saveScene, deleteScene, getBrowserSourceURL, type LayoutScene, type LayoutLayer } from '../services/layouts'

const scenes = ref<LayoutScene[]>([])
const activeSceneID = ref('')
const selectedLayerID = ref('')
const loading = ref(false)
const sourceURL = ref('')
const canvas = ref<HTMLElement>()
const scene = computed(() => scenes.value.find(item => item.id === activeSceneID.value) ?? scenes.value[0] ?? null)
const selectedLayer = computed(() => scene.value?.layers.find(item => item.id === selectedLayerID.value) ?? null)
const sortedLayers = computed(() => [...(scene.value?.layers ?? [])].sort((a, b) => b.z_index - a.z_index))
const scale = ref(1)
let drag: { x: number; y: number; layerX: number; layerY: number } | null = null

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
  }
  const base = defaults[type]
  if (!base) return
  const layer = { id: makeID(type), type, name: base.name ?? type, x: 100, y: 120, width: base.width ?? 300, height: base.height ?? 100, z_index: scene.value.layers.length + 1, visible: true, opacity: 1, text: base.text ?? '', url: base.url ?? '', color: base.color ?? '#ffffff', font_size: base.font_size ?? 24 } as LayoutLayer
  scene.value.layers.push(layer)
  selectedLayerID.value = layer.id
}
function removeLayer() {
  if (!scene.value || !selectedLayer.value) return
  scene.value.layers = scene.value.layers.filter(layer => layer.id !== selectedLayerID.value)
  selectedLayerID.value = ''
}
function startDrag(event: PointerEvent, layer: LayoutLayer) {
  if (!canvas.value || event.button !== 0) return
  selectedLayerID.value = layer.id
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
  return ({ text: '文字', image: '图片', clock: '时钟', danmaku: '弹幕', gift: '礼物', super_chat: 'SC', music: '音乐' } as Record<string, string>)[type] ?? type
}
onMounted(() => { void load(); window.addEventListener('resize', refreshScale) })
onUnmounted(() => { window.removeEventListener('resize', refreshScale) })
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
        </div>
        <el-divider />
        <div class="panel-heading"><strong>图层</strong><span>{{ scene?.layers.length ?? 0 }} 个</span></div>
        <div class="layer-list">
          <button v-for="layer in sortedLayers" :key="layer.id" :class="{ selected: selectedLayerID === layer.id }" @click="selectedLayerID=layer.id">
            <span>{{ layer.name || layerLabel(layer.type) }}</span><small>{{ layerLabel(layer.type) }}</small>
          </button>
          <el-empty v-if="!scene?.layers.length" description="添加组件开始设计" :image-size="64" />
        </div>
      </aside>
      <main class="canvas-area">
        <div class="canvas-caption"><span>画布预览</span><span>{{ scene?.width }} × {{ scene?.height }} · 16:9</span></div>
        <div ref="canvas" class="scene-canvas" :style="{ background: scene?.background === 'transparent' ? 'transparent' : scene?.background }" @click.self="selectedLayerID=''">
          <div class="checkerboard"></div>
          <div v-for="layer in scene?.layers.filter(item => item.visible)" :key="layer.id" class="canvas-layer" :class="{ active: selectedLayerID === layer.id }"
            :style="{ left: (layer.x / (scene?.width ?? 1920) * 100) + '%', top: (layer.y / (scene?.height ?? 1080) * 100) + '%', width: (layer.width / (scene?.width ?? 1920) * 100) + '%', height: (layer.height / (scene?.height ?? 1080) * 100) + '%', zIndex: layer.z_index, opacity: layer.opacity, color: layer.color || '#fff', fontSize: ((layer.font_size || 24) / (scene?.height ?? 1080) * 100) + 'cqh' }"
            @pointerdown="startDrag($event, layer)">
            <span v-if="layer.type === 'text'">{{ layer.text }}</span>
            <span v-else-if="layer.type === 'clock'">12:34:56</span>
            <img v-else-if="layer.type === 'image' && layer.url && layer.url !== 'https://'" :src="layer.url" alt="" />
            <div v-else-if="layer.type === 'music'" class="music-layer-preview"><div class="music-cover-preview">♫</div><div><strong>歌曲名称 · 歌手</strong><span>━━━━━━━●━━━━  01:23 / 04:12</span><p>当前歌词 · 歌词翻译</p></div></div>
            <span v-else>{{ layerLabel(layer.type) }}组件</span>
          </div>
        </div>
        <div class="canvas-hint">拖动组件调整位置 · 选中图层后可在右侧修改属性</div>
      </main>
      <aside class="studio-panel property-panel">
        <div class="panel-heading"><strong>属性</strong><el-button v-if="selectedLayer" text type="danger" @click="removeLayer">删除图层</el-button></div>
        <template v-if="selectedLayer">
          <el-form label-position="top" size="small">
            <el-form-item label="图层名称"><el-input v-model="selectedLayer.name" /></el-form-item>
            <el-form-item v-if="selectedLayer.type === 'text'" label="文字内容"><el-input v-model="selectedLayer.text" type="textarea" :rows="3" /></el-form-item>
            <el-form-item v-if="selectedLayer.type === 'image'" label="图片 URL"><el-input v-model="selectedLayer.url" placeholder="https://..." /></el-form-item>
            <div class="property-grid">
              <el-form-item label="X"><el-input-number v-model="selectedLayer.x" :min="0" :max="scene?.width ?? 1920" controls-position="right" /></el-form-item>
              <el-form-item label="Y"><el-input-number v-model="selectedLayer.y" :min="0" :max="scene?.height ?? 1080" controls-position="right" /></el-form-item>
              <el-form-item label="宽度"><el-input-number v-model="selectedLayer.width" :min="20" :max="scene?.width ?? 1920" controls-position="right" /></el-form-item>
              <el-form-item label="高度"><el-input-number v-model="selectedLayer.height" :min="20" :max="scene?.height ?? 1080" controls-position="right" /></el-form-item>
            </div>
            <el-form-item label="字体大小"><el-input-number v-model="selectedLayer.font_size" :min="10" :max="160" /></el-form-item>
            <el-form-item label="文字颜色"><el-color-picker v-model="selectedLayer.color" show-alpha /></el-form-item>
            <el-form-item label="透明度"><el-slider v-model="selectedLayer.opacity" :min="0" :max="1" :step="0.05" /></el-form-item>
            <el-form-item label="显示图层"><el-switch v-model="selectedLayer.visible" /></el-form-item>
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
.layouts-page{display:flex;flex-direction:column;gap:16px;min-width:0}.page-heading{display:flex;justify-content:space-between;gap:20px;align-items:flex-start}.page-heading h2{margin:0;font-size:25px}.eyebrow{margin:0 0 8px;color:var(--app-accent);font-size:11px;letter-spacing:.15em}.description{color:var(--el-text-color-secondary);font-size:13px;margin:8px 0 0}.heading-actions,.scene-tabs,.source-actions{display:flex;align-items:center;gap:9px;flex-wrap:wrap}.scene-toolbar{display:flex;justify-content:space-between;align-items:center;gap:12px;padding:12px 14px;border:1px solid var(--app-border);border-radius:9px;background:var(--app-bg-surface)}.studio-grid{display:grid;grid-template-columns:210px minmax(0,1fr) 250px;gap:14px;min-height:550px}.studio-panel{border:1px solid var(--app-border);border-radius:9px;background:var(--app-bg-surface);padding:13px;min-width:0}.panel-heading{display:flex;align-items:center;justify-content:space-between;margin-bottom:12px;font-size:13px}.panel-heading span{font-size:11px;color:var(--el-text-color-secondary)}.add-grid{display:grid;grid-template-columns:1fr 1fr;gap:7px}.add-grid button{height:55px;display:flex;flex-direction:column;align-items:center;justify-content:center;gap:5px;border:1px solid var(--app-border);border-radius:7px;background:var(--app-bg-control);color:var(--el-text-color-regular);cursor:pointer;font-size:11px}.add-grid button:hover{border-color:var(--app-accent);color:var(--app-accent)}.add-grid .el-icon{font-size:17px}.layer-list{display:flex;flex-direction:column;gap:4px}.layer-list button{display:flex;justify-content:space-between;align-items:center;padding:9px;border:1px solid transparent;border-radius:6px;background:transparent;color:var(--el-text-color-regular);text-align:left;cursor:pointer;font-size:12px}.layer-list button.selected{background:var(--app-bg-selected);border-color:var(--app-accent);color:var(--app-accent-soft)}.layer-list small{color:var(--el-text-color-secondary)}.canvas-area{min-width:0;display:flex;flex-direction:column;justify-content:center;gap:9px}.canvas-caption{display:flex;justify-content:space-between;color:var(--el-text-color-secondary);font-size:11px}.scene-canvas{position:relative;container-type:size;width:100%;aspect-ratio:16/9;overflow:hidden;border:1px solid var(--app-border);border-radius:6px;box-shadow:0 8px 28px #0002}.checkerboard{position:absolute;inset:0;pointer-events:none;background-image:linear-gradient(45deg,#80808018 25%,transparent 25%),linear-gradient(-45deg,#80808018 25%,transparent 25%),linear-gradient(45deg,transparent 75%,#80808018 75%),linear-gradient(-45deg,transparent 75%,#80808018 75%);background-size:24px 24px;background-position:0 0,0 12px,12px -12px,-12px 0}.canvas-layer{position:absolute;display:flex;align-items:center;justify-content:flex-start;padding:4px;border:1px dashed transparent;cursor:move;overflow:hidden;white-space:pre-wrap;word-break:break-word;font-weight:600;text-shadow:0 2px 5px #000a;touch-action:none}.canvas-layer.active{border-color:#8493ff;background:#8290ff18}.canvas-layer img{width:100%;height:100%;object-fit:contain}.music-layer-preview{display:flex;align-items:center;gap:12px;width:100%;height:100%;padding:8px;border-radius:9px;background:#151923d9;color:#fff;font-size:12px;text-shadow:none;overflow:hidden}.music-cover-preview{display:grid;place-items:center;flex:none;width:30%;height:100%;border-radius:6px;background:#343b4c;font-size:32px}.music-layer-preview>div:last-child{display:flex;flex-direction:column;gap:7px;min-width:0}.music-layer-preview strong,.music-layer-preview span,.music-layer-preview p{overflow:hidden;text-overflow:ellipsis;white-space:nowrap;margin:0;font-size:12px}.music-layer-preview span{color:#c6cad4;font-size:10px}.music-layer-preview p{color:#fff;font-size:11px}.canvas-hint{text-align:center;color:var(--el-text-color-secondary);font-size:11px}.property-panel .el-form-item{margin-bottom:12px}.property-grid{display:grid;grid-template-columns:1fr 1fr;gap:0 8px}.property-grid .el-input-number{width:100%}.obs-card-content{display:flex;align-items:center;justify-content:space-between;gap:16px}.obs-card-content p{margin:6px 0 9px;color:var(--el-text-color-secondary);font-size:12px}.obs-card-content code{font-size:11px;word-break:break-all;color:var(--app-accent-soft)}@media(max-width:1100px){.studio-grid{grid-template-columns:180px minmax(0,1fr)}.property-panel{grid-column:1/-1}.property-grid{grid-template-columns:repeat(4,1fr)}}@media(max-width:760px){.page-heading,.scene-toolbar{align-items:flex-start;flex-direction:column}.studio-grid{grid-template-columns:1fr}.canvas-area{grid-row:1}.property-panel{grid-column:auto}.layer-panel{order:2}.obs-card-content{align-items:flex-start;flex-direction:column}}
</style>
