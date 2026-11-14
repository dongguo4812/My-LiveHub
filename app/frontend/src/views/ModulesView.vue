<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import * as CoreService from '../../bindings/github.com/dongguo4812/My-LiveHub/app/internal/desktop/coreservice.js'
import { coreModules } from '../services/coreStatus'
import { getLive2DSettings, updateLive2DSettings } from '../services/live2d'
import { getScenes, getBrowserSourceURL } from '../services/layouts'

const loading = ref<Record<string, boolean>>({})
const microphoneLoading = ref(false)
const microphoneActive = ref(false)
const microphoneError = ref('')
let microphoneStream: MediaStream | null = null
let microphoneContext: AudioContext | null = null
let microphoneAnalyser: AnalyserNode | null = null
let microphoneSamples: Float32Array<ArrayBuffer> | null = null
let microphoneFrame = 0
let microphoneEndpoint = ''

function stopModuleMicrophone() {
  if (microphoneFrame) cancelAnimationFrame(microphoneFrame)
  microphoneFrame = 0
  microphoneStream?.getTracks().forEach(track => track.stop())
  microphoneStream = null
  microphoneAnalyser = null
  microphoneSamples = null
  void microphoneContext?.close()
  microphoneContext = null
  microphoneActive.value = false
  if (microphoneEndpoint) {
    void fetch(microphoneEndpoint, { method: 'POST', headers: { 'Content-Type': 'text/plain;charset=UTF-8' }, body: JSON.stringify({ value: 0 }), cache: 'no-store' }).catch(() => undefined)
  }
}

function analyseModuleMicrophone(sensitivity: number, smoothing: number, previous = 0) {
  if (!microphoneAnalyser || !microphoneSamples) return
  microphoneAnalyser.getFloatTimeDomainData(microphoneSamples)
  let sum = 0
  for (let i = 0; i < microphoneSamples.length; i++) sum += microphoneSamples[i] * microphoneSamples[i]
  const rms = Math.sqrt(sum / microphoneSamples.length)
  const raw = Math.max(0, Math.min(1, (rms * sensitivity - 0.012) * 8))
  const value = previous * smoothing + raw * (1 - smoothing)
  if (microphoneEndpoint) {
    void fetch(microphoneEndpoint, { method: 'POST', headers: { 'Content-Type': 'text/plain;charset=UTF-8' }, body: JSON.stringify({ value }), cache: 'no-store' }).catch(() => undefined)
  }
  microphoneFrame = requestAnimationFrame(() => analyseModuleMicrophone(sensitivity, smoothing, value))
}

async function toggleModuleMicrophone() {
  if (microphoneLoading.value) return
  if (microphoneActive.value) {
    stopModuleMicrophone()
    try {
      const config = await getLive2DSettings()
      await updateLive2DSettings({ ...config, microphone_enabled: false })
    } catch (error) {
      microphoneError.value = '保存麦克风设置失败：' + String(error)
    }
    return
  }
  microphoneLoading.value = true
  microphoneError.value = ''
  try {
    const live2d = modules.value.find(item => item.name === 'live2d')
    if (live2d?.status !== 'running') throw new Error('请先启动 Live2D 模块')
    if (!navigator.mediaDevices?.getUserMedia) throw new Error('当前 WebView 不支持麦克风采集')
    const scenes = await getScenes()
    if (!scenes.length) throw new Error('尚未创建综合展示场景，无法连接 Live2D 口型接口')
    const sourceURL = await getBrowserSourceURL(scenes[0].id)
    const statusURL = new URL('/api/live2d', sourceURL).toString()
    microphoneEndpoint = new URL('/api/live2d/mouth', sourceURL).toString()
    const statusResponse = await fetch(statusURL, { cache: 'no-store' })
    const status = statusResponse.ok ? await statusResponse.json() as { enabled?: boolean } : null
    if (status?.enabled !== true) throw new Error('Live2D 服务尚未就绪，请稍后重试')
    const config = await getLive2DSettings()
    microphoneStream = await navigator.mediaDevices.getUserMedia({
      audio: {
        deviceId: config.microphone_device_id ? { exact: config.microphone_device_id } : undefined,
        echoCancellation: false,
        noiseSuppression: false,
        autoGainControl: false,
      },
    })
    microphoneContext = new AudioContext()
    await microphoneContext.resume()
    microphoneAnalyser = microphoneContext.createAnalyser()
    microphoneAnalyser.fftSize = 1024
    microphoneAnalyser.smoothingTimeConstant = 0
    microphoneContext.createMediaStreamSource(microphoneStream).connect(microphoneAnalyser)
    microphoneSamples = new Float32Array(microphoneAnalyser.fftSize) as Float32Array<ArrayBuffer>
    await updateLive2DSettings({ ...config, microphone_enabled: true })
    microphoneActive.value = true
    analyseModuleMicrophone(config.microphone_sensitivity, config.microphone_smoothing)
    ElMessage.success('麦克风已启动')
  } catch (error) {
    stopModuleMicrophone()
    microphoneError.value = String(error)
    ElMessage.error('启动麦克风失败：' + microphoneError.value)
  } finally {
    microphoneLoading.value = false
  }
}
const info: Record<string, { title: string; description: string; controllable: boolean }> = {
  'platform-connections': { title: '直播平台连接', description: '平台适配器生命周期；具体直播间连接仍在平台管理中操作。', controllable: false },
  voice: { title: '语音播报', description: '控制弹幕事件语音播报服务。', controllable: true },
  music: { title: '音乐播放', description: '控制弹幕点歌监听与播放服务。停止时会结束当前播放并清空待播队列。', controllable: true },
  video: { title: '视频点播', description: '控制视频点播事件监听与待播队列。停止时清空待播队列，重新启动后可继续点播。', controllable: true },
  live2d: { title: 'Live2D', description: '控制 Live2D 模型资源服务及场景展示。', controllable: true },
  'lol-hex-display': { title: 'LOL Hex Display', description: '控制 LOL Hex 独立扩展服务；应用启动时不会自动启动。', controllable: true },
}
const modules = computed(() => coreModules.value.filter(item => info[item.name]?.controllable).map(item => ({ ...item, ...info[item.name] })))
const statusText = (s: string) => ({ registered: '待启动', starting: '启动中', running: '运行中', stopping: '停止中', stopped: '已停止', failed: '启动失败' } as Record<string,string>)[s] ?? s
const statusType = (s: string) => s === 'running' ? 'success' : s === 'failed' ? 'danger' : ['starting','stopping'].includes(s) ? 'warning' : 'info'
async function refresh() {
  try { coreModules.value = await CoreService.GetModules() }
  catch (e) { ElMessage.error(e instanceof Error ? e.message : String(e)) }
}
async function toggle(name: string, status: string) {
  if (loading.value[name]) return
  if (status === 'running') {
    try { await ElMessageBox.confirm('停止模块可能中断正在进行的功能，确定继续吗？', '停止模块', { type: 'warning', confirmButtonText: '停止', cancelButtonText: '取消' }) }
    catch { return }
  }
  loading.value = { ...loading.value, [name]: true }
  try {
    if (status === 'running') await CoreService.StopModule(name)
    else await CoreService.StartModule(name)
    await refresh()
    ElMessage.success(status === 'running' ? '模块已停止' : '模块已启动')
  } catch (e) {
    await refresh()
    ElMessage.error(e instanceof Error ? e.message : String(e))
  } finally { loading.value = { ...loading.value, [name]: false } }
}
let timer: ReturnType<typeof setInterval> | undefined
onMounted(() => { void refresh(); timer = setInterval(() => void refresh(), 3000) })
onUnmounted(() => { if (timer) clearInterval(timer); stopModuleMicrophone() })
</script>
<template>
  <section class="modules-page">
    <header class="page-heading"><div><h2>功能模块</h2><p>这里可以独立启动或停止各功能模块。模块停止不会关闭 My-LiveHub。</p></div><el-button @click="refresh">刷新状态</el-button></header>
    <div class="module-grid">
      <el-card v-for="item in modules" :key="item.name" shadow="never" class="module-card">
        <div class="module-heading"><div><h3>{{ item.title }}</h3><small>{{ item.name }}</small></div><el-tag :type="statusType(item.status)">{{ statusText(item.status) }}</el-tag></div>
        <p>{{ item.description }}</p>
        <el-alert v-if="item.error" :title="item.error" type="error" :closable="false" show-icon />
        <div class="module-actions">
          <el-button v-if="item.name === 'live2d'" :type="microphoneActive ? 'danger' : 'success'" plain :loading="microphoneLoading" :disabled="microphoneLoading || (item.status !== 'running' && !microphoneActive)" @click="toggleModuleMicrophone">{{ microphoneActive ? '停止麦克风' : '启动麦克风' }}</el-button>
          <el-button v-if="item.controllable" :type="item.status === 'running' ? 'danger' : 'primary'" :loading="loading[item.name]" :disabled="loading[item.name] || ['starting','stopping'].includes(item.status)" @click="toggle(item.name, item.status)">{{ item.status === 'running' ? '停止模块' : '启动模块' }}</el-button>
          <el-tag v-else type="info" effect="plain">{{ item.name === 'lol-hex-display' ? '前往扩展管理' : '暂不支持单独启停' }}</el-tag>
        </div>
        <el-alert v-if="item.name === 'live2d' && microphoneError" :title="microphoneError" type="error" :closable="false" show-icon class="mic-error" />
        <el-tag v-if="item.name === 'live2d'" :type="microphoneActive ? 'success' : 'info'" size="small" class="mic-state">{{ microphoneActive ? '麦克风采集中' : '麦克风未启动' }}</el-tag>
      </el-card>
    </div>
    <el-empty v-if="modules.length === 0" description="尚未读取到模块状态" />
  </section>
</template>
<style scoped>
.modules-page{display:flex;flex-direction:column;gap:22px}.page-heading{display:flex;justify-content:space-between;align-items:center;gap:16px}.page-heading h2{margin:0;font-size:24px}.page-heading p{margin:9px 0 0;color:var(--el-text-color-secondary)}.module-grid{display:grid;grid-template-columns:repeat(auto-fill,minmax(290px,1fr));gap:16px}.module-card{min-width:0}.module-heading{display:flex;align-items:flex-start;justify-content:space-between;gap:12px}.module-heading h3{margin:0;font-size:17px}.module-heading small{display:block;margin-top:5px;color:var(--el-text-color-secondary)}.module-card p{min-height:42px;color:var(--el-text-color-secondary);font-size:13px;line-height:1.6}.module-actions{display:flex;align-items:center;margin-top:16px}
</style>