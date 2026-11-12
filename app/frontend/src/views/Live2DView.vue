<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { ElMessage } from 'element-plus'
import { getLive2DSettings, updateLive2DSettings, listLive2DModels, type Live2DConfig, type Live2DModel } from '../services/live2d'

const settings = ref<Live2DConfig | null>(null)
const models = ref<Live2DModel[]>([])
const devices = ref<MediaDeviceInfo[]>([])
const selectedModel = ref('')
const loading = ref(false)
const micError = ref('')
const micActive = ref(false)
const level = ref(0)
const mouthOpen = ref(0)
const hasRuntime = computed(() => models.value.some(model => model.runtime_available))
let stream: MediaStream | null = null
let audioContext: AudioContext | null = null
let analyser: AnalyserNode | null = null
let raf = 0
let samples: Float32Array<ArrayBuffer> | null = null

const modelOptions = computed(() => models.value.filter(model => model.format === settings.value?.runtime_version))
const levelPercent = computed(() => Math.round(level.value * 100))
const mouthPercent = computed(() => Math.round(mouthOpen.value * 100))

async function refresh() {
  loading.value = true
  try {
    const [config, found] = await Promise.all([getLive2DSettings(), listLive2DModels()])
    settings.value = config
    models.value = found
    selectedModel.value = config.runtime_version === 'moc2' ? config.default_model_moc2 : config.default_model_moc3
    await enumerateDevices()
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
  const next = { ...settings.value, ...patch }
  try {
    settings.value = await updateLive2DSettings(next)
    ElMessage.success('Live2D 设置已保存')
  } catch (error) {
    ElMessage.error('保存 Live2D 设置失败：' + String(error))
  }
}

async function chooseRuntime(value: 'moc2' | 'moc3') {
  if (!settings.value) return
  await save({ runtime_version: value })
  selectedModel.value = value === 'moc2' ? settings.value.default_model_moc2 : settings.value.default_model_moc3
}

async function chooseModel(value: string) {
  if (!settings.value) return
  selectedModel.value = value
  await save(settings.value.runtime_version === 'moc2' ? { default_model_moc2: value } : { default_model_moc3: value })
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

onMounted(() => { void refresh() })
onUnmounted(stopMicrophone)
</script>

<template>
  <section class="live2d-page">
    <div class="page-heading">
      <div><p class="eyebrow">LIVE2D AVATAR</p><h2>Live2D</h2><p class="muted">管理 MOC2 / MOC3 模型，并通过麦克风实时驱动嘴型。</p></div>
      <el-button :loading="loading" @click="refresh">刷新模型</el-button>
    </div>

    <el-alert type="warning" :closable="false" show-icon title="运行时尚未随程序提供">
      当前已接入模型目录扫描与麦克风实时音频分析。MOC2 / MOC3 的官方运行时及 Core 需要按其许可单独提供；在运行时加载成功前，不会把模型嘴型能力误报为可用。
    </el-alert>

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
      <el-table v-if="models.length" :data="models" size="small" class="model-table">
        <el-table-column prop="name" label="模型" min-width="150" />
        <el-table-column label="格式" width="100"><template #default="{ row }"><el-tag size="small">{{ row.format.toUpperCase() }}</el-tag></template></el-table-column>
        <el-table-column label="嘴型参数检测" min-width="180"><template #default="{ row }">{{ row.capability_status }}</template></el-table-column>
      </el-table>
      <el-empty v-else description="尚未发现模型。请将模型文件夹放入应用数据目录下的 live2d/models/moc2 或 moc3。" :image-size="90" />
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
        <div class="meter-label mouth-label"><span>实时嘴型开合目标值</span><strong>{{ mouthPercent }}%</strong></div>
        <el-progress :percentage="mouthPercent" :show-text="false" status="success" />
        <p class="hint">此数值来自麦克风 RMS 音量分析。待对应 SDK 运行时接入后，将映射到模型实际存在的嘴型参数。</p>
      </div>
      <div class="tuning-row">
        <div><span>麦克风灵敏度</span><el-slider v-if="settings" :model-value="settings.microphone_sensitivity" :min="0.1" :max="10" :step="0.1" @change="save({ microphone_sensitivity: Number($event) })" /></div>
        <div><span>口型平滑度</span><el-slider v-if="settings" :model-value="settings.microphone_smoothing" :min="0" :max="0.99" :step="0.01" @change="save({ microphone_smoothing: Number($event) })" /></div>
      </div>
    </el-card>
  </section>
</template>

<style scoped>
.live2d-page{display:flex;flex-direction:column;gap:20px}.page-heading{display:flex;align-items:flex-start;justify-content:space-between;gap:20px}.eyebrow{margin:0 0 9px;color:var(--el-color-primary);font-size:11px;letter-spacing:.16em}h2{margin:0;font-size:25px;font-weight:650}.muted{margin:9px 0 0;color:var(--el-text-color-secondary)}.runtime-row,.model-row,.mic-controls,.card-heading{display:flex;align-items:center;justify-content:space-between;gap:16px}.runtime-row{margin-bottom:20px}.model-row{margin-bottom:16px}.model-select,.device-control{display:flex;align-items:center;gap:14px;flex:1}.model-select .el-select,.device-control .el-select{max-width:420px;width:100%}.model-table{margin-top:16px}.card-heading{display:flex}.meter-block{margin-top:24px}.meter-label{display:flex;justify-content:space-between;margin:12px 0 8px;font-size:13px}.meter-label strong{font-variant-numeric:tabular-nums}.mouth-label{margin-top:20px}.hint{margin:12px 0 0;color:var(--el-text-color-secondary);font-size:12px}.tuning-row{display:grid;grid-template-columns:1fr 1fr;gap:32px;margin-top:22px}.tuning-row>div>span{font-size:13px;color:var(--el-text-color-regular)}@media(max-width:760px){.tuning-row{grid-template-columns:1fr}.mic-controls,.model-row{align-items:stretch;flex-direction:column}.model-select,.device-control{align-items:stretch;flex-direction:column}}
</style>
