<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { ElMessage } from 'element-plus'
import { useRouter } from 'vue-router'
import { getLOLHexStatus, type LOLHexStatus } from '../services/lolHex'

const router = useRouter()
const status = ref<LOLHexStatus | null>(null)
const loading = ref(true)
const error = ref('')
let timer: ReturnType<typeof setInterval> | undefined
const managerURL = computed(() => status.value?.running && status.value.base_url
  ? status.value.base_url.replace(/\/$/, '') + '/'
  : '')

async function refresh() {
  try {
    const next = await getLOLHexStatus()
    const wasRunning = status.value?.running
    status.value = next
    error.value = next.error || ''
    if (wasRunning && !next.running) ElMessage.warning('LOL Hex 服务已停止，管理页面暂时不可用')
  } catch (cause) {
    error.value = String(cause)
  } finally {
    loading.value = false
  }
}

onMounted(() => {
  void refresh()
  timer = setInterval(() => void refresh(), 2500)
})
onUnmounted(() => {
  if (timer) clearInterval(timer)
})
</script>

<template>
  <section class="hex-manager-page">
    <header class="page-heading">
      <div>
        <h2>LOL Hex 管理</h2>
        <p>管理海克斯识别、截图、OCR、LCU 连接和战绩条等功能设置。</p>
      </div>
      <el-button @click="router.push('/modules')">功能模块管理</el-button>
    </header>

    <el-skeleton v-if="loading" :rows="4" animated />
    <template v-else-if="status?.running">
      <div class="service-state">
        <el-tag type="success">Hex 服务运行中</el-tag>
        <span>本地管理服务：{{ status.base_url }}</span>
        <el-button size="small" @click="refresh">刷新状态</el-button>
      </div>
      <iframe
        :key="managerURL"
        class="manager-frame"
        :src="managerURL"
        title="LOL Hex 管理页面"
        referrerpolicy="no-referrer"
      />
    </template>
    <el-empty v-else description="LOL Hex 模块当前未运行">
      <template #default>
        <p class="empty-hint">请先在「功能模块」中启动 LOL Hex，启动后即可打开原有管理页面。停止模块不会删除或重置已有配置。</p>
        <el-button type="primary" @click="router.push('/modules')">前往启动 LOL Hex</el-button>
      </template>
    </el-empty>
    <el-alert v-if="error" :title="error" type="error" :closable="false" show-icon />
  </section>
</template>

<style scoped>
.hex-manager-page{display:flex;flex-direction:column;gap:16px;min-height:100%}
.page-heading{display:flex;align-items:center;justify-content:space-between;gap:16px}
.page-heading h2{margin:0;font-size:24px}
.page-heading p{margin:9px 0 0;color:var(--el-text-color-secondary)}
.service-state{display:flex;align-items:center;gap:12px;flex-wrap:wrap;color:var(--el-text-color-secondary);font-size:13px}
.manager-frame{display:block;width:100%;height:calc(100vh - 190px);min-height:620px;border:1px solid var(--el-border-color);border-radius:8px;background:#fff}
.empty-hint{max-width:480px;color:var(--el-text-color-secondary);line-height:1.7;text-align:center}
</style>
