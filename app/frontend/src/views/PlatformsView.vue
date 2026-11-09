<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { Refresh } from '@element-plus/icons-vue'
import {
  connectPlatform,
  disconnectPlatform,
  platformActionError,
  platforms,
  platformsError,
  refreshPlatforms,
} from '../services/platforms'

const busyPlatformID = ref('')
const statusLabels: Record<string, string> = {
  disconnected: '未连接',
  connecting: '连接中',
  connected: '已连接',
  disconnecting: '断开中',
  failed: '连接失败',
}
const statusTypes: Record<string, 'info' | 'warning' | 'success' | 'danger'> = {
  disconnected: 'info',
  connecting: 'warning',
  connected: 'success',
  disconnecting: 'warning',
  failed: 'danger',
}

async function runAction(platformID: string, action: 'connect' | 'disconnect') {
  busyPlatformID.value = platformID
  try {
    if (action === 'connect') await connectPlatform(platformID)
    else await disconnectPlatform(platformID)
  } finally {
    busyPlatformID.value = ''
  }
}

onMounted(() => void refreshPlatforms())
</script>

<template>
  <section class="platform-page">
    <div class="page-heading">
      <div>
        <p class="eyebrow">LIVE PLATFORM ACCESS</p>
        <h2>平台连接</h2>
        <p class="muted">统一查看直播平台连接状态，并管理平台连接。</p>
      </div>
      <el-button :icon="Refresh" @click="refreshPlatforms">刷新状态</el-button>
    </div>

    <el-alert
      v-if="platformsError"
      :title="`平台状态读取失败：${platformsError}`"
      type="error"
      :closable="false"
      show-icon
    />
    <el-alert
      v-if="platformActionError"
      :title="`连接操作失败：${platformActionError}`"
      type="error"
      :closable="false"
      show-icon
    />

    <el-card shadow="never">
      <template #header>
        <div class="card-heading">
          <span>已注册的平台适配器</span>
          <el-tag size="small" effect="plain">{{ platforms.length }} 个平台</el-tag>
        </div>
      </template>

      <el-empty
        v-if="platforms.length === 0"
        description="尚未接入直播平台适配器"
        :image-size="100"
      >
        <template #default>
          <p class="empty-note">连接管理服务已就绪。接入具体平台后，可在此连接账号并查看实时状态。</p>
        </template>
      </el-empty>

      <el-table v-else :data="platforms" row-key="platform_id">
        <el-table-column prop="platform_id" label="平台标识" min-width="180" />
        <el-table-column label="连接状态" width="140">
          <template #default="{ row }">
            <el-tag :type="statusTypes[row.status] ?? 'info'" effect="light">
              {{ statusLabels[row.status] ?? row.status }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column prop="error" label="错误信息" min-width="220" show-overflow-tooltip />
        <el-table-column label="操作" width="180" fixed="right">
          <template #default="{ row }">
            <el-button
              v-if="row.status === 'disconnected' || row.status === 'failed'"
              size="small"
              type="primary"
              :loading="busyPlatformID === row.platform_id"
              :disabled="!!busyPlatformID"
              @click="runAction(row.platform_id, 'connect')"
            >连接</el-button>
            <el-button
              v-else-if="row.status === 'connected'"
              size="small"
              type="danger"
              plain
              :loading="busyPlatformID === row.platform_id"
              :disabled="!!busyPlatformID"
              @click="runAction(row.platform_id, 'disconnect')"
            >断开连接</el-button>
            <el-button v-else size="small" disabled>处理中</el-button>
          </template>
        </el-table-column>
      </el-table>
    </el-card>
  </section>
</template>

<style scoped>
.platform-page { display: flex; flex-direction: column; gap: 20px; }
.page-heading { display: flex; justify-content: space-between; align-items: flex-start; gap: 20px; }
.eyebrow { margin: 0 0 9px; color: var(--el-color-primary); font-size: 11px; letter-spacing: .16em; }
h2 { margin: 0; font-size: 25px; font-weight: 650; }
.muted { margin: 9px 0 0; color: var(--el-text-color-secondary); }
.card-heading { display: flex; align-items: center; justify-content: space-between; font-weight: 600; }
.empty-note { color: var(--el-text-color-secondary); font-size: 13px; }
</style>
