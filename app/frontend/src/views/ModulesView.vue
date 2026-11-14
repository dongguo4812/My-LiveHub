<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import * as CoreService from '../../bindings/github.com/dongguo4812/My-LiveHub/app/internal/desktop/coreservice.js'
import { coreModules } from '../services/coreStatus'

const loading = ref<Record<string, boolean>>({})
const info: Record<string, { title: string; description: string; controllable: boolean }> = {
  'platform-connections': { title: '直播平台连接', description: '平台适配器生命周期；具体直播间连接仍在平台管理中操作。', controllable: false },
  voice: { title: '语音播报', description: '控制弹幕事件语音播报服务。', controllable: true },
  music: { title: '音乐播放', description: '控制弹幕点歌监听与播放服务。停止时会结束当前播放并清空待播队列。', controllable: true },
  video: { title: '视频点播', description: '视频队列与播放器服务。当前关闭操作会释放播放器资源，暂不提供单独启停。', controllable: false },
  live2d: { title: 'Live2D', description: '资源与展示服务由场景按需调用。', controllable: false },
  'lol-hex-display': { title: 'LOL Hex Display', description: '独立扩展，请前往扩展管理页面启停。', controllable: false },
}
const modules = computed(() => coreModules.value.map(item => ({ ...item, ...(info[item.name] ?? { title: item.name, description: '内置模块', controllable: false }) })))
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
onUnmounted(() => { if (timer) clearInterval(timer) })
</script>
<template>
  <section class="modules-page">
    <header class="page-heading"><div><h2>功能模块</h2><p>查看内置模块运行状态，并独立控制支持启停的模块。模块停止不会关闭 My-LiveHub。</p></div><el-button @click="refresh">刷新状态</el-button></header>
    <div class="module-grid">
      <el-card v-for="item in modules" :key="item.name" shadow="never" class="module-card">
        <div class="module-heading"><div><h3>{{ item.title }}</h3><small>{{ item.name }}</small></div><el-tag :type="statusType(item.status)">{{ statusText(item.status) }}</el-tag></div>
        <p>{{ item.description }}</p>
        <el-alert v-if="item.error" :title="item.error" type="error" :closable="false" show-icon />
        <div class="module-actions">
          <el-button v-if="item.controllable" :type="item.status === 'running' ? 'danger' : 'primary'" :loading="loading[item.name]" :disabled="loading[item.name] || ['starting','stopping'].includes(item.status)" @click="toggle(item.name, item.status)">{{ item.status === 'running' ? '停止模块' : '启动模块' }}</el-button>
          <el-tag v-else type="info" effect="plain">{{ item.name === 'lol-hex-display' ? '前往扩展管理' : '暂不支持单独启停' }}</el-tag>
        </div>
      </el-card>
    </div>
    <el-empty v-if="modules.length === 0" description="尚未读取到模块状态" />
  </section>
</template>
<style scoped>
.modules-page{display:flex;flex-direction:column;gap:22px}.page-heading{display:flex;justify-content:space-between;align-items:center;gap:16px}.page-heading h2{margin:0;font-size:24px}.page-heading p{margin:9px 0 0;color:var(--el-text-color-secondary)}.module-grid{display:grid;grid-template-columns:repeat(auto-fill,minmax(290px,1fr));gap:16px}.module-card{min-width:0}.module-heading{display:flex;align-items:flex-start;justify-content:space-between;gap:12px}.module-heading h3{margin:0;font-size:17px}.module-heading small{display:block;margin-top:5px;color:var(--el-text-color-secondary)}.module-card p{min-height:42px;color:var(--el-text-color-secondary);font-size:13px;line-height:1.6}.module-actions{display:flex;align-items:center;margin-top:16px}
</style>