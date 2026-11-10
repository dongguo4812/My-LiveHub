<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import * as VoiceService from '../../bindings/github.com/dongguo4812/My-LiveHub/app/internal/voice/voiceservice.js'
import { voiceAudioPlaying, voiceAudioUnlocked, unlockVoiceAudio } from '../services/voiceAudio'

type VoiceConfig = Awaited<ReturnType<typeof VoiceService.GetConfig>>
type VoiceStatus = Awaited<ReturnType<typeof VoiceService.GetStatus>>
type VoiceOption = Awaited<ReturnType<typeof VoiceService.GetVoices>>[number]

const config = ref<VoiceConfig | null>(null)
const status = ref<VoiceStatus | null>(null)
const voices = ref<VoiceOption[]>([])
const loading = ref(false)
const saving = ref(false)
const voiceLoading = ref(false)
const error = ref('')
const success = ref('')
const testText = ref('My-LiveHub 语音播报测试。')
let statusTimer: ReturnType<typeof setInterval> | undefined

const rate = computed({
  get: () => Number.parseInt(config.value?.rate ?? '+0%', 10) || 0,
  set: (value: number) => { if (config.value) config.value.rate = `${value >= 0 ? '+' : ''}${value}%` },
})
const volume = computed({
  get: () => Number.parseInt(config.value?.volume ?? '+0%', 10) || 0,
  set: (value: number) => { if (config.value) config.value.volume = `${value >= 0 ? '+' : ''}${value}%` },
})
const pitch = computed({
  get: () => Number.parseInt(config.value?.pitch ?? '+0Hz', 10) || 0,
  set: (value: number) => { if (config.value) config.value.pitch = `${value >= 0 ? '+' : ''}${value}Hz` },
})

async function loadConfig() {
  loading.value = true
  error.value = ''
  try {
    config.value = await VoiceService.GetConfig()
    await loadVoices()
    status.value = await VoiceService.GetStatus()
  } catch (reason) {
    error.value = reason instanceof Error ? reason.message : String(reason)
  } finally {
    loading.value = false
  }
}

async function loadVoices() {
  voiceLoading.value = true
  try {
    voices.value = await VoiceService.GetVoices('zh-CN')
    if (config.value && !voices.value.some(item => item.id === config.value?.voice)) {
      voices.value = await VoiceService.GetVoices('')
    }
  } catch (reason) {
    error.value = reason instanceof Error ? reason.message : String(reason)
  } finally {
    voiceLoading.value = false
  }
}

async function saveConfig() {
  if (!config.value) return
  saving.value = true
  error.value = ''
  success.value = ''
  try {
    await VoiceService.SaveConfig(config.value)
    success.value = '语音设置已保存'
    status.value = await VoiceService.GetStatus()
  } catch (reason) {
    error.value = reason instanceof Error ? reason.message : String(reason)
  } finally {
    saving.value = false
  }
}

async function testSpeech() {
  error.value = ''
  success.value = ''
  try {
    if (!voiceAudioUnlocked.value) {
      const unlocked = await unlockVoiceAudio()
      if (!unlocked) {
        error.value = '无法启用音频输出，请检查系统音频设备'
        return
      }
    }
    await VoiceService.TestSpeech(testText.value)
    success.value = '测试语音已加入播报队列'
  } catch (reason) {
    error.value = reason instanceof Error ? reason.message : String(reason)
  }
}

async function pauseSpeech() {
  await VoiceService.PauseSpeech()
}
async function resumeSpeech() {
  await VoiceService.ResumeSpeech()
}
async function stopSpeech() {
  await VoiceService.StopSpeech()
  status.value = await VoiceService.GetStatus()
}

onMounted(() => {
  void loadConfig()
  statusTimer = setInterval(async () => {
    try { status.value = await VoiceService.GetStatus() } catch { /* Keep the last known status. */ }
  }, 1000)
})
onUnmounted(() => {
  if (statusTimer) clearInterval(statusTimer)
})
</script>

<template>
  <section class="voice-page">
    <header class="page-heading">
      <div>
        <p class="eyebrow">VOICE OUTPUT</p>
        <h2>语音播报</h2>
        <p class="description">通过统一语音引擎接口处理平台事件。当前提供 Edge TTS，后续可扩展其他语音引擎。Edge TTS 使用微软在线服务，播报文本会发送至微软服务器。</p>
      </div>
      <el-tag :type="status?.running ? 'success' : 'info'" effect="plain">
        {{ status?.running ? '模块运行中' : '模块未启动' }}
      </el-tag>
    </header>

    <el-alert v-if="error" :title="error" type="error" :closable="true" show-icon @close="error = ''" />
    <el-alert v-if="success" :title="success" type="success" :closable="true" show-icon @close="success = ''" />

    <el-card shadow="never" v-loading="loading">
      <template #header><div class="card-heading"><strong>播报引擎</strong><el-tag type="success">Edge TTS</el-tag></div></template>
      <div v-if="config" class="setting-list">
        <div class="setting-row">
          <div><strong>启用语音播报</strong><p>启用后，事件中心的新事件会进入语音播报队列。</p></div>
          <el-switch v-model="config.enabled" />
        </div>
        <el-divider />
        <div class="setting-row">
          <div><strong>语音引擎</strong><p>采用可插拔引擎接口，当前版本实现 Edge TTS。</p></div>
          <el-select model-value="edge" disabled style="width: 260px"><el-option label="Microsoft Edge TTS" value="edge" /></el-select>
        </div>
        <el-divider />
        <div class="setting-row">
          <div><strong>播报声音</strong><p>从 Edge TTS 在线语音目录加载普通话声音。</p></div>
          <el-select v-model="config.voice" filterable :loading="voiceLoading" placeholder="选择语音" style="width: 360px">
            <el-option v-for="item in voices" :key="item.id" :label="`${item.name} · ${item.locale} · ${item.gender}`" :value="item.id" />
          </el-select>
        </div>
        <el-divider />
        <div class="slider-row"><div><strong>语速</strong><span>{{ config.rate }}</span></div><el-slider v-model="rate" :min="-50" :max="50" /></div>
        <div class="slider-row"><div><strong>音量</strong><span>{{ config.volume }}</span></div><el-slider v-model="volume" :min="-50" :max="50" /></div>
        <div class="slider-row"><div><strong>音调</strong><span>{{ config.pitch }}</span></div><el-slider v-model="pitch" :min="-50" :max="50" /></div>
      </div>
      <div class="card-actions">
        <el-button type="primary" :loading="saving" @click="saveConfig">保存设置</el-button>
        <el-button @click="loadConfig">重新加载</el-button>
      </div>
    </el-card>

    <el-card shadow="never">
      <template #header><strong>事件播报范围</strong></template>
      <div v-if="config" class="event-grid">
        <el-checkbox v-model="config.events.danmaku">弹幕</el-checkbox>
        <el-checkbox v-model="config.events.gift">礼物</el-checkbox>
        <el-checkbox v-model="config.events.super_chat">醒目留言（SC）</el-checkbox>
        <el-checkbox v-model="config.events.guard">舰队上舰</el-checkbox>
        <el-checkbox v-model="config.events.follow">关注</el-checkbox>
        <el-checkbox v-model="config.events.welcome">进场</el-checkbox>
        <el-checkbox v-model="config.events.share">分享</el-checkbox>
        <el-checkbox v-model="config.events.like">点赞</el-checkbox>
      </div>
      <p class="hint">默认关闭进场和点赞播报，避免高频事件造成语音队列拥堵。修改后请保存设置。</p>
    </el-card>

    <el-card shadow="never">
      <template #header><strong>播报控制与测试</strong></template>
      <div class="audio-unlock">
        <div><strong>音频输出</strong><p>首次使用需要手动启用音频，启用后后台事件可自动播报。</p></div>
        <el-button :type="voiceAudioUnlocked ? 'success' : 'primary'" @click="unlockVoiceAudio">
          {{ voiceAudioUnlocked ? '音频已启用' : '启用音频输出' }}
        </el-button>
      </div>
      <el-divider />
      <el-input v-model="testText" maxlength="500" show-word-limit placeholder="输入测试播报内容" @keyup.enter="testSpeech" />
      <div class="card-actions">
        <el-button type="primary" :disabled="!voiceAudioUnlocked" @click="testSpeech">测试播报</el-button>
        <el-button :disabled="!status?.speaking && !voiceAudioPlaying" @click="pauseSpeech">暂停</el-button>
        <el-button @click="resumeSpeech">继续</el-button>
        <el-button type="danger" plain @click="stopSpeech">停止并清空队列</el-button>
      </div>
      <div class="status-grid">
        <div><small>当前状态</small><strong>{{ voiceAudioPlaying ? '正在播放' : status?.speaking ? '正在合成' : '空闲' }}</strong></div>
        <div><small>待播数量</small><strong>{{ status?.queued ?? 0 }}</strong></div>
        <div><small>丢弃数量</small><strong>{{ status?.dropped ?? 0 }}</strong></div>
      </div>
      <p v-if="status?.current_text" class="current-text">当前内容：{{ status.current_text }}</p>
      <el-alert v-if="status?.last_error" :title="status.last_error" type="warning" :closable="false" show-icon />
    </el-card>
  </section>
</template>

<style scoped>
.voice-page{display:flex;flex-direction:column;gap:20px}.page-heading{display:flex;align-items:flex-start;justify-content:space-between;gap:20px}.page-heading h2{margin:0;font-size:25px}.eyebrow{margin:0 0 9px;color:var(--app-accent);font-size:11px;letter-spacing:.15em}.description,.setting-row p,.hint,.audio-unlock p{color:var(--el-text-color-secondary);font-size:13px}.card-heading{display:flex;align-items:center;justify-content:space-between}.setting-list{display:flex;flex-direction:column;gap:8px}.setting-row,.audio-unlock{display:flex;align-items:center;justify-content:space-between;gap:24px}.setting-row p,.audio-unlock p{margin:6px 0 0}.slider-row{display:grid;grid-template-columns:140px 1fr;align-items:center;gap:24px;margin:16px 0}.slider-row div{display:flex;justify-content:space-between;gap:12px}.slider-row span{color:var(--el-text-color-secondary);font-variant-numeric:tabular-nums}.card-actions{display:flex;justify-content:flex-end;gap:10px;margin-top:20px}.event-grid{display:grid;grid-template-columns:repeat(4,minmax(0,1fr));gap:16px}.hint{margin:16px 0 0}.status-grid{display:grid;grid-template-columns:repeat(3,minmax(0,1fr));gap:12px;margin-top:18px}.status-grid div{display:flex;flex-direction:column;gap:6px;padding:14px;border:1px solid var(--app-border);border-radius:9px;background:var(--app-bg-control)}.status-grid small{color:var(--el-text-color-secondary)}.status-grid strong{font-size:20px}.current-text{color:var(--el-text-color-secondary);font-size:13px;overflow-wrap:anywhere}@media(max-width:900px){.event-grid{grid-template-columns:repeat(2,minmax(0,1fr))}.setting-row,.audio-unlock{align-items:flex-start;flex-direction:column}.slider-row{grid-template-columns:1fr;gap:4px}}
</style>
