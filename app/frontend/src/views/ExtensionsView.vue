<script setup lang="ts">
import { onMounted, onUnmounted, ref } from 'vue'
import { ElMessage } from 'element-plus'
import { getLOLHexStatus, startLOLHex, stopLOLHex, type LOLHexStatus } from '../services/lolHex'

const status = ref<LOLHexStatus | null>(null)
const loading = ref(false)
let refreshTimer: ReturnType<typeof setInterval> | undefined

async function refresh() {
  try { status.value = await getLOLHexStatus() }
  catch (error) { ElMessage.error(error instanceof Error ? error.message : String(error)) }
}
async function start() {
  loading.value = true
  try {
    status.value = await startLOLHex()
    if (status.value.running) ElMessage.success('LOL Hex 已启动')
    else if (status.value.error) ElMessage.warning(status.value.error)
  } catch (error) {
    await refresh()
    ElMessage.error(error instanceof Error ? error.message : String(error))
  } finally { loading.value = false }
}
async function stop() {
  loading.value = true
  try {
    status.value = await stopLOLHex()
    ElMessage.success('已向 LOL Hex 发送停止请求')
  } catch (error) {
    await refresh()
    ElMessage.error(error instanceof Error ? error.message : String(error))
  } finally { loading.value = false }
}
function openPage(path: string) {
  const base = status.value?.base_url
  if (!base) return
  window.open(base + path, '_blank', 'noopener')
}
onMounted(() => {
  void refresh()
  refreshTimer = setInterval(() => void refresh(), 3000)
})
onUnmounted(() => { if (refreshTimer) clearInterval(refreshTimer) })
</script>

<template>
  <section class="extensions-page">
    <header class="page-heading">
      <div>
        <h2>扩展管理</h2>
        <p>管理独立扩展进程。扩展不会随 My-LiveHub 自动启动。</p>
      </div>
    </header>

    <el-card shadow="never" class="extension-card">
      <div class="extension-header">
        <div class="extension-icon">HEX</div>
        <div class="extension-title">
          <h3>LOL Hex Display</h3>
          <p>英雄联盟海克斯强化识别与战绩展示</p>
        </div>
        <el-tag v-if="status?.state === 'running'" type="success">运行中</el-tag>
        <el-tag v-else-if="status?.state === 'starting'" type="warning">启动中</el-tag>
        <el-tag v-else type="info">已停止</el-tag>
      </div>

      <el-descriptions :column="2" border class="extension-details">
        <el-descriptions-item label="运行方式">{{ status?.owned ? 'My-LiveHub 托管' : status?.external ? '外部启动' : '未运行' }}</el-descriptions-item>
        <el-descriptions-item label="服务端口">{{ status?.port ?? 8767 }}</el-descriptions-item>
        <el-descriptions-item label="进程 PID">{{ status?.pid || '—' }}</el-descriptions-item>
        <el-descriptions-item label="管理地址">{{ status?.base_url || '—' }}</el-descriptions-item>
      </el-descriptions>

      <el-alert v-if="status?.error" :title="status.error" type="warning" :closable="false" show-icon class="extension-alert" />
      <div class="extension-actions">
        <el-button type="primary" :loading="loading" :disabled="loading || status?.running || status?.state === 'starting'" @click="start">启动扩展</el-button>
        <el-button type="danger" plain :loading="loading" :disabled="loading || !status?.owned" @click="stop">停止扩展</el-button>
        <el-button :disabled="!status?.running" @click="openPage('/')">打开管理页</el-button>
        <el-button :disabled="!status?.running" @click="openPage('/display')">打开海克斯展示页</el-button>
        <el-button :disabled="!status?.running" @click="openPage('/bar')">打开战绩条</el-button>
        <el-button text :loading="loading" @click="refresh">刷新状态</el-button>
      </div>
      <p class="extension-note">Windows 下 F1 全局截图需要管理员权限，启动时会出现 UAC 提示。若未安装 Python 3.10+，请在扩展目录部署打包后的 lol_hex_display.exe。</p>
    </el-card>
  </section>
</template>

<style scoped>
.extensions-page { display: flex; flex-direction: column; gap: 22px; }
.page-heading h2 { margin: 0; font-size: 24px; }
.page-heading p { margin: 9px 0 0; color: var(--el-text-color-secondary); }
.extension-card { max-width: 980px; }
.extension-header { display: flex; align-items: center; gap: 14px; margin-bottom: 22px; }
.extension-icon { display: grid; place-items: center; width: 54px; height: 54px; border-radius: 13px; background: #242c43; color: #a8d2ff; font-weight: 800; letter-spacing: 1px; }
.extension-title { flex: 1; min-width: 0; }
.extension-title h3 { margin: 0; font-size: 19px; }
.extension-title p { margin: 6px 0 0; color: var(--el-text-color-secondary); }
.extension-details { margin-bottom: 16px; }
.extension-alert { margin-bottom: 16px; }
.extension-actions { display: flex; flex-wrap: wrap; gap: 8px; }
.extension-note { margin: 18px 0 0; color: var(--el-text-color-secondary); font-size: 12px; line-height: 1.6; }
</style>
