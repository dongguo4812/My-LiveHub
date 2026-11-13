<script setup lang="ts">
defineOptions({ name: 'Live2DView' })
import { computed, nextTick, onMounted, onUnmounted, ref } from 'vue'
import { ElMessage } from 'element-plus'
import { getLive2DSettings, updateLive2DSettings, listLive2DModels, type Live2DConfig, type Live2DModel } from '../services/live2d'
import { getScenes, getBrowserSourceURL } from '../services/layouts'

type LegacyModel = {
  initialized?: boolean
  lipSync?: boolean | null
  lipSyncValue?: number
  lipSyncParameterID?: string
  getLive2DModel?: () => { getParamIndex?: (id: string) => number } | null
  modelMatrix?: {
    width: number
    height: number
    tr: Float32Array
    getScaleX: () => number
    getScaleY: () => number
    scale: (x: number, y: number) => void
    centerX: (x: number) => void
    centerY: (y: number) => void
  } | null
}
type LegacyManager = { getModel?: (index: number) => LegacyModel | null }
type Live2DWindow = Window & {
  loadlive2d?: (canvasID: string, modelURL: string) => void
  __live2dManager?: LegacyManager
  live2dSetMouthOpen?: (value: number) => void
  live2dSetModelTransform?: (scale: number, x: number, y: number) => boolean
  live2dPlayMotion?: (group: string, index: number) => Promise<boolean>
  live2dSetExpression?: (name: string) => boolean
  live2dResetExpression?: () => boolean
}

const settings = ref<Live2DConfig | null>(null)
const models = ref<Live2DModel[]>([])
const devices = ref<MediaDeviceInfo[]>([])
const selectedModel = ref('')
const selectedExpression = ref('')
const selectedMotionGroup = ref('')
const selectedMotionIndex = ref(0)
const currentModel = computed(() => modelOptions.value.find(model => model.id === selectedModel.value) || null)
type ModelTransform = { model_scale: number; model_offset_x: number; model_offset_y: number }
const transformSettings = computed<ModelTransform>(() => {
  const config = settings.value
  if (!config) return { model_scale: 0.65, model_offset_x: 0, model_offset_y: 0 }
  return config.runtime_version === 'moc2'
    ? { model_scale: config.model_scale_moc2, model_offset_x: config.model_offset_x_moc2, model_offset_y: config.model_offset_y_moc2 }
    : { model_scale: config.model_scale_moc3, model_offset_x: config.model_offset_x_moc3, model_offset_y: config.model_offset_y_moc3 }
})
function runtimeTransformPatch(patch: Partial<ModelTransform>): Partial<Live2DConfig> {
  const result: Partial<Live2DConfig> = {}
  if (settings.value?.runtime_version === 'moc2') {
    if (patch.model_scale !== undefined) result.model_scale_moc2 = patch.model_scale
    if (patch.model_offset_x !== undefined) result.model_offset_x_moc2 = patch.model_offset_x
    if (patch.model_offset_y !== undefined) result.model_offset_y_moc2 = patch.model_offset_y
  } else {
    if (patch.model_scale !== undefined) result.model_scale_moc3 = patch.model_scale
    if (patch.model_offset_x !== undefined) result.model_offset_x_moc3 = patch.model_offset_x
    if (patch.model_offset_y !== undefined) result.model_offset_y_moc3 = patch.model_offset_y
  }
  return result
}
function previewModelTransform(patch: Partial<ModelTransform>) {
  if (!settings.value) return
  Object.assign(settings.value, runtimeTransformPatch(patch))
  applyModelTransform()
}
function saveModelTransform(patch: Partial<ModelTransform>) {
  return save(runtimeTransformPatch(patch))
}

const loading = ref(false)
const micError = ref('')
const micActive = ref(false)
const level = ref(0)
const mouthOpen = ref(0)
const runtimeStatus = ref('尚未加载模型')
const runtimeError = ref('')
const mouthCapability = ref('尚未检测')
const canvas = ref<HTMLCanvasElement | null>(null)
const runtimeFrame = ref<HTMLIFrameElement | null>(null)
const modelOptions = computed(() => models.value.filter(model => model.format === settings.value?.runtime_version))
const levelPercent = computed(() => Math.round(level.value * 100))
const mouthPercent = computed(() => Math.round(mouthOpen.value * 100))
let stream: MediaStream | null = null
let audioContext: AudioContext | null = null
let analyser: AnalyserNode | null = null
let raf = 0
let samples: Float32Array<ArrayBuffer> | null = null
let activeModelURL = ''
let activeMoc2MouthParameter = ''
let activeMoc2TransformBase: { model: LegacyModel; scaleX: number; scaleY: number; centerX: number; centerY: number } | null = null
let settingsSaveQueue: Promise<void> = Promise.resolve()
let legacyScriptPromise: Promise<void> | null = null
let runtimeMessageHandler: ((event: MessageEvent) => void) | null = null
let mouthSyncChannel: BroadcastChannel | null = null
let mouthSyncEndpoint = ''

function broadcastMouthOpen(value: number) {
  const bounded = Math.max(0, Math.min(1, value))
  mouthSyncChannel?.postMessage({ type: 'live2d-mouth-open', value: bounded })
  if (mouthSyncEndpoint) {
    void fetch(mouthSyncEndpoint, {
      method: 'POST',
      headers: { 'Content-Type': 'text/plain;charset=UTF-8' },
      body: JSON.stringify({ value: bounded }),
      cache: 'no-store',
    }).catch(() => undefined)
  }
}

async function resolveMouthSyncEndpoint() {
  try {
    const scenes = await getScenes()
    const scene = scenes[0]
    if (!scene) return
    const sourceURL = await getBrowserSourceURL(scene.id)
    mouthSyncEndpoint = new URL('/api/live2d/mouth', sourceURL).toString()
  } catch {
    mouthSyncEndpoint = ''
  }
}

function live2dWindow(): Live2DWindow {
  return window as Live2DWindow
}

async function loadLegacyRuntime() {
  if (typeof live2dWindow().loadlive2d === 'function') return
  if (!legacyScriptPromise) {
    legacyScriptPromise = new Promise((resolve, reject) => {
      const script = document.createElement('script')
      script.src = '/live2d-runtime/moc2/live2d.min.js'
      script.onload = () => resolve()
      script.onerror = () => reject(new Error('MOC2 运行时脚本加载失败'))
      document.head.appendChild(script)
    })
  }
  await legacyScriptPromise
  if (typeof live2dWindow().loadlive2d !== 'function') throw new Error('MOC2 运行时未导出 loadlive2d 接口')
}

function onRuntimeMessage(event: MessageEvent) {
  if (!runtimeFrame.value || event.source !== runtimeFrame.value.contentWindow || !event.data || event.data.modelUrl !== activeModelURL) return
  if (event.data.type === 'moc3-model-loaded') {
    runtimeStatus.value = 'MOC3 模型已加载并开始渲染'
  } else if (event.data.type === 'moc3-mouth-sync-status') {
    const ids = Array.isArray(event.data.parameterIds) ? event.data.parameterIds.join('、') : ''
    mouthCapability.value = event.data.supported ? '支持：' + ids : (event.data.reason || '模型没有可用的嘴型参数')
  } else if (event.data.type === 'moc3-model-error') {
    runtimeError.value = String(event.data.message || 'MOC3 模型资源加载失败')
    runtimeStatus.value = '模型加载失败'
  }
}

async function waitForLegacyModel(modelURL: string) {
  const started = Date.now()
  while (Date.now() - started < 30000) {
    if (activeModelURL !== modelURL) return
    const model = live2dWindow().__live2dManager?.getModel?.(0)
    if (model?.initialized && model.getLive2DModel) {
      const liveModel = model.getLive2DModel()
      if (!liveModel) throw new Error('MOC2 模型对象尚未就绪')
      const candidates = ['PARAM_MOUTH_OPEN_Y', 'ParamMouthOpenY', 'MouthOpenY', 'ParamMouthOpen', 'MouthOpen']
      const available = candidates.find(id => (liveModel.getParamIndex?.(id) ?? -1) >= 0)
      activeMoc2MouthParameter = available || ''
      model.lipSyncParameterID = activeMoc2MouthParameter
      mouthCapability.value = available ? '支持：' + available : '未找到兼容的嘴型参数'
      return
    }
    await new Promise(resolve => window.setTimeout(resolve, 100))
  }
  throw new Error('MOC2 模型加载超时，请检查模型配置、贴图和 MOC 文件')
}

function applyModelTransform() {
  const config = settings.value
  if (!config) return
  if (config.runtime_version === 'moc3') {
    const frameWindow = runtimeFrame.value?.contentWindow as Live2DWindow | undefined
    frameWindow?.live2dSetModelTransform?.(transformSettings.value.model_scale, transformSettings.value.model_offset_x / 100, transformSettings.value.model_offset_y / 100)
    return
  }

  // MOC2 must scale the model matrix, not the canvas element. CSS-scaling the
  // canvas only shrinks an already-clipped viewport and cannot restore the model.
  const model = live2dWindow().__live2dManager?.getModel?.(0)
  const matrix = model?.modelMatrix
  if (!model || !matrix || !matrix.width || !matrix.height) return
  if (!activeMoc2TransformBase || activeMoc2TransformBase.model !== model) {
    const scaleX = matrix.getScaleX()
    const scaleY = matrix.getScaleY()
    activeMoc2TransformBase = {
      model,
      scaleX,
      scaleY,
      centerX: matrix.tr[12] + matrix.width * scaleX / 2,
      centerY: matrix.tr[13] + matrix.height * scaleY / 2,
    }
  }
  const base = activeMoc2TransformBase
  const width = matrix.width * Math.abs(base.scaleX)
  const height = matrix.height * Math.abs(base.scaleY)
  const fitScale = Math.min(1, 2 / width, 2 / height)
  const scale = fitScale * transformSettings.value.model_scale
  matrix.scale(base.scaleX * scale, base.scaleY * scale)
  matrix.centerX(base.centerX + transformSettings.value.model_offset_x * 2 / 100)
  matrix.centerY(base.centerY - transformSettings.value.model_offset_y * 2 / 100)
}

async function playSelectedExpression() {
  const name = selectedExpression.value
  if (!name) return
  try {
    let ok = false
    if (settings.value?.runtime_version === 'moc3') {
      const frameWindow = runtimeFrame.value?.contentWindow as Live2DWindow | undefined
      ok = frameWindow?.live2dSetExpression?.(name) ?? false
    } else {
      const model = live2dWindow().__live2dManager?.getModel?.(0)
      if (model && typeof (model as any).setExpression === 'function') { (model as any).setExpression(name); ok = true }
    }
    if (!ok) ElMessage.warning('当前模型无法应用该表情')
  } catch (error) { ElMessage.error('切换表情失败：' + String(error)) }
}

async function resetExpression() {
  if (settings.value?.runtime_version === 'moc3') {
    const frameWindow = runtimeFrame.value?.contentWindow as Live2DWindow | undefined
    if (!frameWindow?.live2dResetExpression?.()) ElMessage.warning('当前模型不支持恢复表情')
    return
  }
  const model = live2dWindow().__live2dManager?.getModel?.(0) as any
  model?.expressionManager?.stopAllMotions?.()
}

async function playSelectedMotion() {
  if (!selectedMotionGroup.value || !currentModel.value) return
  try {
    let ok = false
    if (settings.value?.runtime_version === 'moc3') {
      const frameWindow = runtimeFrame.value?.contentWindow as Live2DWindow | undefined
      ok = await (frameWindow?.live2dPlayMotion?.(selectedMotionGroup.value, selectedMotionIndex.value) ?? false)
    } else {
      const model = live2dWindow().__live2dManager?.getModel?.(0) as any
      if (model && typeof model.startMotion === 'function') { model.startMotion(selectedMotionGroup.value, selectedMotionIndex.value, 3); ok = true }
    }
    if (!ok) ElMessage.warning('动作播放失败，请检查模型动作资源')
  } catch (error) { ElMessage.error('播放动作失败：' + String(error)) }
}

async function loadSelectedModel() {
  const format = settings.value?.runtime_version
  const model = modelOptions.value.find(item => item.id === selectedModel.value)
  runtimeError.value = ''
  mouthCapability.value = '正在检测'
  if (!format || !model) {
    activeModelURL = ''
    runtimeStatus.value = '请选择模型'
    mouthCapability.value = '尚未检测'
    return
  }
  if (!model.model_url || !model.runtime_available) {
    runtimeStatus.value = '本地模型资源服务不可用'
    runtimeError.value = '无法访问本地模型文件，请重启应用后重试'
    return
  }
  activeModelURL = model.model_url
  activeMoc2MouthParameter = ''
  runtimeStatus.value = '正在加载 ' + format.toUpperCase() + ' 模型…'
  await nextTick()
  try {
    if (format === 'moc2') {
      if (!canvas.value) throw new Error('MOC2 展示画布尚未创建')
      await loadLegacyRuntime()
      const target = canvas.value
      target.width = Math.max(1, target.clientWidth * window.devicePixelRatio)
      target.height = Math.max(1, target.clientHeight * window.devicePixelRatio)
      live2dWindow().loadlive2d?.(target.id, model.model_url)
      await waitForLegacyModel(model.model_url)
      applyModelTransform()
      runtimeStatus.value = 'MOC2 模型已加载并开始渲染'
    } else {
      if (!runtimeFrame.value) throw new Error('MOC3 展示框架尚未创建')
      const frame = runtimeFrame.value
      await new Promise<void>((resolve, reject) => {
        let finished = false
        const cleanup = () => {
          window.clearTimeout(timeout)
          frame.removeEventListener('load', onLoad)
          window.removeEventListener('message', onMessage)
        }
        const finish = (error?: Error) => {
          if (finished) return
          finished = true
          cleanup()
          error ? reject(error) : resolve()
        }
        const timeout = window.setTimeout(() => finish(new Error('MOC3 模型加载超时，请检查 model3.json、MOC3 文件和贴图')), 45000)
        const onMessage = (event: MessageEvent) => {
          if (event.source !== frame.contentWindow || !event.data || event.data.modelUrl !== model.model_url) return
          if (event.data.type === 'moc3-model-error') finish(new Error(String(event.data.message || 'MOC3 模型资源加载失败')))
          if (event.data.type === 'moc3-model-loaded') finish()
        }
        const onLoad = () => {
          if (!frame.contentWindow) return finish(new Error('MOC3 WebView 初始化失败'))
          if (typeof (frame.contentWindow as Live2DWindow).live2dSetMouthOpen !== 'function') return finish(new Error('Cubism 5 R5 运行时接口未初始化'))
        }
        window.addEventListener('message', onMessage)
        frame.addEventListener('load', onLoad, { once: true })
        frame.src = '/live2d-runtime/moc3/index.html?model=' + encodeURIComponent(model.model_url)
      })
      runtimeStatus.value = 'MOC3 模型已加载并开始渲染'
      applyModelTransform()
      const status = runtimeFrame.value?.contentWindow as Live2DWindow | undefined
      if (status && typeof status.live2dSetMouthOpen === 'function') {
        // The runtime reports the definitive parameter capability through postMessage after its first model update.
        status.live2dSetMouthOpen(0)
      }
    }
  } catch (error) {
    runtimeStatus.value = '模型加载失败'
    runtimeError.value = String(error)
    mouthCapability.value = '检测失败'
  }
}

async function refresh() {
  loading.value = true
  try {
    const [config, found] = await Promise.all([getLive2DSettings(), listLive2DModels()])
    settings.value = config
    models.value = found
    selectedModel.value = config.runtime_version === 'moc2' ? config.default_model_moc2 : config.default_model_moc3
    await enumerateDevices()
    await resolveMouthSyncEndpoint()
    await loadSelectedModel()
  } catch (error) {
    ElMessage.error('读取 Live2D 配置失败：' + String(error))
  } finally {
    loading.value = false
  }
}

async function enumerateDevices() {
  try {
    devices.value = (await navigator.mediaDevices.enumerateDevices()).filter(device => device.kind === 'audioinput')
  } catch {
    devices.value = []
  }
}

async function save(patch: Partial<Live2DConfig>) {
  if (!settings.value) return

  // Serialize writes and merge each patch with the latest persisted config.
  // This prevents rapid slider changes from overwriting one another.
  const operation = settingsSaveQueue.then(async () => {
    const latest = await getLive2DSettings()
    const next = { ...latest, ...patch }
    await updateLive2DSettings(next)

    // Read back from the backend so the UI never treats an unsaved draft as persisted.
    const persisted = await getLive2DSettings()
    for (const [key, expected] of Object.entries(patch)) {
      const actual = persisted[key as keyof Live2DConfig]
      if (typeof expected === 'number' && typeof actual === 'number') {
        if (Math.abs(actual - expected) > 0.000001) {
          throw new Error(`设置项 ${key} 未能持久化（提交值：${expected}，读取值：${actual}）`)
        }
      } else if (actual !== expected) {
        throw new Error(`设置项 ${key} 未能持久化`)
      }
    }

    settings.value = persisted
    applyModelTransform()
    ElMessage.success('Live2D 设置已保存并验证')
  })

  settingsSaveQueue = operation.then(() => undefined, () => undefined)
  try {
    await operation
  } catch (error) {
    // Restore the persisted values after a failed write instead of leaving a misleading preview value.
    try {
      settings.value = await getLive2DSettings()
      applyModelTransform()
    } catch {
      // Keep the original save error as the user-facing diagnostic.
    }
    ElMessage.error('保存 Live2D 设置失败：' + String(error))
  }
}

async function chooseRuntime(value: 'moc2' | 'moc3') {
  if (!settings.value) return
  await save({ runtime_version: value })
  selectedModel.value = value === 'moc2' ? settings.value.default_model_moc2 : settings.value.default_model_moc3
  await loadSelectedModel()
}

async function chooseModel(value: string) {
  if (!settings.value) return
  selectedModel.value = value
  selectedExpression.value = ''
  selectedMotionGroup.value = ''
  selectedMotionIndex.value = 0
  await save(settings.value.runtime_version === 'moc2' ? { default_model_moc2: value } : { default_model_moc3: value })
  await loadSelectedModel()
}

function driveModelMouth(value: number) {
  const bounded = Math.max(0, Math.min(1, value))
  broadcastMouthOpen(bounded)
  if (settings.value?.runtime_version === 'moc3') {
    const frameWindow = runtimeFrame.value?.contentWindow as Live2DWindow | undefined
    frameWindow?.live2dSetMouthOpen?.(bounded)
    return
  }
  const model = live2dWindow().__live2dManager?.getModel?.(0)
  if (!model?.initialized) return
  if (!activeMoc2MouthParameter) return
  model.lipSyncParameterID = activeMoc2MouthParameter
  model.lipSync = null
  model.lipSyncValue = bounded
}

function stopMicrophone() {
  if (raf) cancelAnimationFrame(raf)
  raf = 0
  stream?.getTracks().forEach(track => track.stop())
  stream = null
  analyser = null
  samples = null
  void audioContext?.close()
  audioContext = null
  micActive.value = false
  level.value = 0
  mouthOpen.value = 0
  driveModelMouth(0)
}

function analyseMicrophone() {
  if (!analyser || !samples || !settings.value) return
  analyser.getFloatTimeDomainData(samples)
  let sum = 0
  for (let i = 0; i < samples.length; i++) sum += samples[i] * samples[i]
  const rms = Math.sqrt(sum / samples.length)
  const raw = Math.max(0, Math.min(1, (rms * settings.value.microphone_sensitivity - 0.012) * 8))
  const smoothing = settings.value.microphone_smoothing
  level.value = level.value * smoothing + Math.min(1, rms * 8) * (1 - smoothing)
  mouthOpen.value = mouthOpen.value * smoothing + raw * (1 - smoothing)
  driveModelMouth(mouthOpen.value)
  raf = requestAnimationFrame(analyseMicrophone)
}

async function startMicrophone() {
  micError.value = ''
  if (!navigator.mediaDevices?.getUserMedia) {
    micError.value = '当前 WebView 不支持麦克风采集'
    return
  }
  try {
    stopMicrophone()
    stream = await navigator.mediaDevices.getUserMedia({
      audio: {
        deviceId: settings.value?.microphone_device_id ? { exact: settings.value.microphone_device_id } : undefined,
        echoCancellation: false,
        noiseSuppression: false,
        autoGainControl: false,
      },
    })
    audioContext = new AudioContext()
    await audioContext.resume()
    analyser = audioContext.createAnalyser()
    analyser.fftSize = 1024
    analyser.smoothingTimeConstant = 0
    audioContext.createMediaStreamSource(stream).connect(analyser)
    samples = new Float32Array(analyser.fftSize) as Float32Array<ArrayBuffer>
    micActive.value = true
    await enumerateDevices()
    analyseMicrophone()
  } catch (error) {
    stopMicrophone()
    micError.value = String(error)
  }
}

async function toggleMicrophone(enabled: boolean) {
  if (enabled) {
    await startMicrophone()
    if (micActive.value) await save({ microphone_enabled: true })
  } else {
    stopMicrophone()
    await save({ microphone_enabled: false })
  }
}

async function changeDevice(deviceID: string) {
  await save({ microphone_device_id: deviceID })
  if (micActive.value) await startMicrophone()
}

onMounted(() => {
  mouthSyncChannel = typeof BroadcastChannel !== 'undefined' ? new BroadcastChannel('my-livehub-live2d-mouth') : null
  runtimeMessageHandler = onRuntimeMessage
  window.addEventListener('message', runtimeMessageHandler)
  void refresh()
})
onUnmounted(() => {
  stopMicrophone()
  mouthSyncChannel?.close()
  mouthSyncChannel = null
  if (runtimeMessageHandler) window.removeEventListener('message', runtimeMessageHandler)
})
</script>

<template>
  <section class="live2d-page">
    <div class="page-heading">
      <div><p class="eyebrow">LIVE2D AVATAR</p><h2>Live2D</h2><p class="muted">管理 MOC2 / MOC3 模型，并通过麦克风实时驱动嘴型。</p></div>
      <el-button :loading="loading" @click="refresh">刷新模型</el-button>
    </div>

    <el-card shadow="never" class="preview-card">
      <template #header><div class="card-heading"><strong>模型预览</strong><el-tag :type="runtimeError ? 'danger' : 'success'">{{ runtimeStatus }}</el-tag></div></template>
      <div class="model-preview">
        <canvas v-show="settings?.runtime_version === 'moc2'" id="live2d-model-canvas" ref="canvas"  />
        <iframe v-show="settings?.runtime_version === 'moc3'" ref="runtimeFrame" title="Live2D MOC3 Runtime" allow="autoplay" />
        <div v-if="runtimeError" class="preview-error">{{ runtimeError }}</div>
        <div v-else-if="!selectedModel" class="preview-empty">请选择一个模型开始预览</div>
      </div>
      <el-alert v-if="runtimeError" type="error" :closable="false" show-icon :title="runtimeError" />
      <p class="hint">嘴型参数：{{ mouthCapability }}</p>
      <div class="model-adjustments" v-if="settings && currentModel">
        <div class="adjustment"><label>模型大小 <strong>{{ Math.round(transformSettings.model_scale * 100) }}%</strong></label><el-slider :model-value="transformSettings.model_scale" :min="0.1" :max="2.5" :step="0.05" @input="previewModelTransform({ model_scale: Number($event) })" @change="saveModelTransform({ model_scale: Number($event) })" /></div>
        <div class="adjustment"><label>水平位置 <strong>{{ transformSettings.model_offset_x }}</strong></label><el-slider :model-value="transformSettings.model_offset_x" :min="-100" :max="100" :step="1" @input="previewModelTransform({ model_offset_x: Number($event) })" @change="saveModelTransform({ model_offset_x: Number($event) })" /></div>
        <div class="adjustment"><label>垂直位置 <strong>{{ transformSettings.model_offset_y }}</strong></label><el-slider :model-value="transformSettings.model_offset_y" :min="-100" :max="100" :step="1" @input="previewModelTransform({ model_offset_y: Number($event) })" @change="saveModelTransform({ model_offset_y: Number($event) })" /></div>
        <el-button size="small" @click="saveModelTransform({ model_scale: 0.65, model_offset_x: 0, model_offset_y: 0 })">恢复默认大小和位置</el-button>
      </div>
    </el-card>

    <el-card shadow="never">
      <template #header><strong>模型管理</strong></template>
      <div class="runtime-row">
        <span>模型格式</span>
        <el-radio-group :model-value="settings?.runtime_version" @change="chooseRuntime">
          <el-radio-button value="moc2">MOC2</el-radio-button>
          <el-radio-button value="moc3">MOC3</el-radio-button>
        </el-radio-group>
      </div>
      <div class="model-row">
        <div class="model-select">
          <span>当前模型</span>
          <el-select :model-value="selectedModel" clearable placeholder="选择已放入模型目录的模型" :disabled="!modelOptions.length" @change="chooseModel">
            <el-option v-for="model in modelOptions" :key="model.id" :label="model.name" :value="model.id" />
          </el-select>
        </div>
        <el-tag type="info">{{ modelOptions.length }} 个模型</el-tag>
      </div>
      <el-table v-if="modelOptions.length" :data="modelOptions" size="small" class="model-table">
        <el-table-column prop="name" label="模型" min-width="150" />
        <el-table-column label="格式" width="100"><template #default="{ row }"><el-tag size="small">{{ row.format.toUpperCase() }}</el-tag></template></el-table-column>
        <el-table-column label="嘴型参数检测" min-width="180"><template #default="{ row }">{{ row.capability_status }}</template></el-table-column>
      </el-table>
      <el-empty v-else :description="'当前 ' + (settings?.runtime_version || 'MOC3').toUpperCase() + ' 目录下尚未发现模型。请将对应格式的完整模型文件夹放入 live2d/models/' + (settings?.runtime_version || 'moc3') + '。'" :image-size="90" />
    </el-card>

    <el-card shadow="never" v-if="currentModel">
      <template #header><strong>动作与表情</strong></template>
      <div class="animation-controls">
        <div class="animation-row">
          <div class="animation-select"><span>模型表情</span><el-select v-model="selectedExpression" clearable placeholder="该模型没有可用表情" :disabled="!currentModel.expressions.length"><el-option v-for="name in currentModel.expressions" :key="name" :label="name" :value="name" /></el-select></div>
          <el-button type="primary" :disabled="!selectedExpression" @click="playSelectedExpression">应用表情</el-button>
          <el-button plain @click="resetExpression">恢复默认表情</el-button>
        </div>
        <div class="animation-row">
          <div class="animation-select"><span>动作分组</span><el-select v-model="selectedMotionGroup" clearable placeholder="该模型没有可用动作" :disabled="!currentModel.motion_groups.length" @change="selectedMotionIndex = 0"><el-option v-for="group in currentModel.motion_groups" :key="group.name" :label="group.name + '（' + group.count + '）'" :value="group.name" /></el-select></div>
          <div class="animation-select index-select"><span>动作序号</span><el-select v-model="selectedMotionIndex" :disabled="!selectedMotionGroup"><el-option v-for="index in (currentModel.motion_groups.find(group => group.name === selectedMotionGroup)?.count || 0)" :key="index - 1" :label="'动作 ' + index" :value="index - 1" /></el-select></div>
          <el-button type="primary" :disabled="!selectedMotionGroup" @click="playSelectedMotion">播放动作</el-button>
        </div>
      </div>
      <div class="capability-tags"><el-tag size="small" :type="currentModel.has_physics ? 'success' : 'info'">{{ currentModel.has_physics ? '物理效果' : '无物理配置' }}</el-tag><el-tag size="small" :type="currentModel.has_pose ? 'success' : 'info'">{{ currentModel.has_pose ? '姿势配置' : '无姿势配置' }}</el-tag><el-tag size="small" :type="currentModel.hit_areas.length ? 'success' : 'info'">{{ currentModel.hit_areas.length ? '触摸区域 ' + currentModel.hit_areas.length : '无触摸区域' }}</el-tag><el-tag size="small" type="info">{{ currentModel.expressions.length }} 个表情</el-tag><el-tag size="small" type="info">{{ currentModel.motion_groups.reduce((sum, group) => sum + group.count, 0) }} 个动作</el-tag></div>
    </el-card>

    <el-card shadow="never">
      <template #header><div class="card-heading"><strong>麦克风口型同步</strong><el-tag :type="micActive ? 'success' : 'info'">{{ micActive ? '实时采集中' : '未启动' }}</el-tag></div></template>
      <div class="mic-controls">
        <div class="device-control">
          <span>输入设备</span>
          <el-select :model-value="settings?.microphone_device_id || ''" placeholder="系统默认麦克风" clearable @change="changeDevice">
            <el-option v-for="device in devices" :key="device.deviceId" :label="device.label || '麦克风设备'" :value="device.deviceId" />
          </el-select>
        </div>
        <el-button v-if="!micActive" type="primary" @click="toggleMicrophone(true)">启动麦克风</el-button>
        <el-button v-else type="danger" plain @click="toggleMicrophone(false)">停止采集</el-button>
      </div>
      <el-alert v-if="micError" type="error" :title="'麦克风启动失败：' + micError" :closable="false" show-icon />
      <div class="meter-block">
        <div class="meter-label"><span>输入音量</span><strong>{{ levelPercent }}%</strong></div>
        <el-progress :percentage="levelPercent" :show-text="false" />
        <div class="meter-label mouth-label"><span>实时嘴型开合</span><strong>{{ mouthPercent }}%</strong></div>
        <el-progress :percentage="mouthPercent" :show-text="false" status="success" />
        <p class="hint">麦克风 RMS 音量实时映射到已检测到的模型嘴型参数。模型不支持时会明确显示检测结果。</p>
      </div>
      <div class="tuning-row">
        <div><span>麦克风灵敏度</span><el-slider v-if="settings" :model-value="settings.microphone_sensitivity" :min="0.1" :max="10" :step="0.1" @change="save({ microphone_sensitivity: Number($event) })" /></div>
        <div><span>口型平滑度</span><el-slider v-if="settings" :model-value="settings.microphone_smoothing" :min="0" :max="0.99" :step="0.01" @change="save({ microphone_smoothing: Number($event) })" /></div>
      </div>
    </el-card>
  </section>
</template>

<style scoped>
.live2d-page{display:flex;flex-direction:column;gap:20px}.page-heading{display:flex;align-items:flex-start;justify-content:space-between;gap:20px}.eyebrow{margin:0 0 9px;color:var(--el-color-primary);font-size:11px;letter-spacing:.16em}h2{margin:0;font-size:25px;font-weight:650}.muted{margin:9px 0 0;color:var(--el-text-color-secondary)}.runtime-row,.model-row,.mic-controls,.card-heading{display:flex;align-items:center;justify-content:space-between;gap:16px}.runtime-row{margin-bottom:20px}.model-row{margin-bottom:16px}.model-select,.device-control{display:flex;align-items:center;gap:14px;flex:1}.model-select .el-select,.device-control .el-select{max-width:420px;width:100%}.model-table{margin-top:16px}.model-adjustments{display:grid;grid-template-columns:1fr 1fr 1fr auto;align-items:center;gap:18px;margin-top:18px;padding-top:16px;border-top:1px solid var(--el-border-color-lighter)}.adjustment label{display:flex;justify-content:space-between;font-size:12px;color:var(--el-text-color-secondary)}.adjustment strong{color:var(--el-text-color-primary)}.animation-controls{display:flex;flex-direction:column;gap:18px}.animation-row{display:flex;align-items:end;gap:12px}.animation-select{display:flex;flex-direction:column;gap:7px;flex:1}.animation-select span{font-size:12px;color:var(--el-text-color-secondary)}.animation-select .el-select{width:100%}.index-select{max-width:150px}.capability-tags{display:flex;flex-wrap:wrap;gap:8px;margin-top:18px}.card-heading{display:flex}.meter-block{margin-top:24px}.meter-label{display:flex;justify-content:space-between;margin:12px 0 8px;font-size:13px}.meter-label strong{font-variant-numeric:tabular-nums}.mouth-label{margin-top:20px}.hint{margin:12px 0 0;color:var(--el-text-color-secondary);font-size:12px}.tuning-row{display:grid;grid-template-columns:1fr 1fr;gap:32px;margin-top:22px}.tuning-row>div>span{font-size:13px;color:var(--el-text-color-regular)}.model-preview{height:420px;position:relative;overflow:hidden;border-radius:8px;background:repeating-conic-gradient(#222 0% 25%,#292929 0% 50%) 50%/24px 24px}.model-preview canvas,.model-preview iframe{display:block;width:100%;height:100%;border:0;background:transparent;transform-origin:center center;transition:transform .12s ease}.preview-error,.preview-empty{position:absolute;inset:0;display:flex;align-items:center;justify-content:center;padding:24px;color:var(--el-text-color-secondary);text-align:center;pointer-events:none}.preview-error{color:var(--el-color-danger)}@media(max-width:980px){.model-adjustments{grid-template-columns:1fr 1fr}.animation-row{flex-wrap:wrap}}@media(max-width:760px){.model-adjustments{grid-template-columns:1fr}.tuning-row{grid-template-columns:1fr}.mic-controls,.model-row{align-items:stretch;flex-direction:column}.model-select,.device-control{align-items:stretch;flex-direction:column}.model-preview{height:300px}}
</style>
