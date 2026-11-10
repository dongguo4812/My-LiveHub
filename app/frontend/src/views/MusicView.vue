<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import * as MusicService from '../../bindings/github.com/dongguo4812/My-LiveHub/app/internal/desktop/musicservice.js'

type MusicConfig = Awaited<ReturnType<typeof MusicService.GetConfig>>

const config = ref<MusicConfig | null>(null)
const loading = ref(false)
const saving = ref(false)
const error = ref('')
const success = ref('')
const sources = [
  { id: 'netease', name: '网易云音乐' },
  { id: 'qq_music', name: 'QQ 音乐' },
  { id: 'kugou', name: '酷狗音乐' },
  { id: 'kuwo', name: '酷我音乐' },
]
const enabledCount = computed(() => Object.values(config.value?.sources ?? {}).filter(Boolean).length)
const canSave = computed(() => enabledCount.value > 0)

async function loadConfig() {
  loading.value = true
  error.value = ''
  try {
    config.value = await MusicService.GetConfig()
  } catch (reason) {
    error.value = reason instanceof Error ? reason.message : String(reason)
  } finally {
    loading.value = false
  }
}

async function saveConfig() {
  if (!config.value) return
  if (!canSave.value) {
    error.value = '至少需要启用一个音乐音源'
    return
  }
  saving.value = true
  error.value = ''
  success.value = ''
  try {
    await MusicService.SaveConfig(config.value)
    success.value = '音乐设置已保存'
    config.value = await MusicService.GetConfig()
  } catch (reason) {
    error.value = reason instanceof Error ? reason.message : String(reason)
  } finally {
    saving.value = false
  }
}

onMounted(() => { void loadConfig() })
</script>

<template>
  <section class="music-page">
    <header class="page-heading">
      <div>
        <p class="eyebrow">MUSIC PLAYER</p>
        <h2>音乐播放器</h2>
        <p class="description">一个 My-LiveHub 播放器，多个可同时启用的音乐音源。搜索结果会标注来源，实际播放统一交给 My-LiveHub 播放引擎。</p>
      </div>
      <el-tag type="info" effect="plain">播放器开发中</el-tag>
    </header>

    <el-alert v-if="error" :title="error" type="error" :closable="true" show-icon @close="error = ''" />
    <el-alert v-if="success" :title="success" type="success" :closable="true" show-icon @close="success = ''" />

    <el-card shadow="never" v-loading="loading">
      <template #header>
        <div class="card-heading">
          <strong>音源管理</strong>
          <el-tag :type="enabledCount > 0 ? 'success' : 'danger'" effect="plain">已启用 {{ enabledCount }} 个</el-tag>
        </div>
      </template>
      <p class="section-description">每个音源独立启停，可以同时勾选多个。搜索时会并行查询所有已启用且已接入的音源。</p>
      <div v-if="config" class="source-list">
        <div v-for="source in sources" :key="source.id" class="source-row">
          <div class="source-copy">
            <strong>{{ source.name }}</strong>
            <span>音源适配器尚未接入</span>
          </div>
          <el-checkbox v-model="config.sources[source.id]">启用</el-checkbox>
        </div>
      </div>
      <el-alert type="info" :closable="false" show-icon title="音源开关和配置迁移已接入；在线搜索适配器及实际播放流程仍在开发，当前不会显示虚假的可用状态。" />
      <div class="card-actions">
        <el-button @click="loadConfig">重新加载</el-button>
        <el-button type="primary" :loading="saving" :disabled="!canSave" @click="saveConfig">保存设置</el-button>
      </div>
    </el-card>

    <el-card shadow="never">
      <template #header><strong>点歌设置</strong></template>
      <div v-if="config" class="setting-list">
        <div class="setting-row">
          <div><strong>启用音乐模块</strong><p>关闭后不启动音乐播放服务。</p></div>
          <el-switch v-model="config.enabled" />
        </div>
        <el-divider />
        <div class="setting-row">
          <div><strong>允许弹幕点歌</strong><p>收到点歌指令后搜索已启用的音源并加入统一队列。</p></div>
          <el-switch v-model="config.command_enabled" />
        </div>
        <el-divider />
        <div class="setting-row">
          <div><strong>点歌指令前缀</strong><p>例如：点歌 稻香 周杰伦。</p></div>
          <el-input v-model="config.command_prefix" maxlength="16" style="width: 220px" />
        </div>
        <el-divider />
        <div class="setting-row">
          <div><strong>播放队列容量</strong><p>统一队列允许等待的歌曲数量。</p></div>
          <el-input-number v-model="config.queue_capacity" :min="1" :max="1000" />
        </div>
        <el-divider />
        <div class="setting-row">
          <div><strong>每个音源搜索数量</strong><p>各音源单次搜索的候选数量上限。</p></div>
          <el-input-number v-model="config.search_limit" :min="1" :max="50" />
        </div>
      </div>
      <div class="card-actions">
        <el-button type="primary" :loading="saving" :disabled="!config || !canSave" @click="saveConfig">保存全部设置</el-button>
      </div>
    </el-card>
  </section>
</template>

<style scoped>
.music-page{display:flex;flex-direction:column;gap:20px}.page-heading{display:flex;align-items:flex-start;justify-content:space-between;gap:20px}.page-heading h2{margin:0;font-size:25px}.eyebrow{margin:0 0 9px;color:var(--app-accent);font-size:11px;letter-spacing:.15em}.description,.section-description,.source-copy span,.setting-row p{color:var(--el-text-color-secondary);font-size:13px}.section-description{margin:0 0 16px}.card-heading{display:flex;align-items:center;justify-content:space-between}.source-list,.setting-list{display:flex;flex-direction:column}.source-row,.setting-row{display:flex;align-items:center;justify-content:space-between;gap:24px;padding:14px 0;border-bottom:1px solid var(--app-border)}.source-row:last-child{border-bottom:0}.source-copy{display:flex;flex-direction:column;gap:5px}.source-copy span{font-size:12px}.setting-row p{margin:6px 0 0}.card-actions{display:flex;justify-content:flex-end;gap:10px;margin-top:20px}@media(max-width:800px){.source-row,.setting-row{align-items:flex-start;flex-direction:column}}
</style>
