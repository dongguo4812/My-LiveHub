<script setup lang="ts">
import { onMounted, reactive, watch } from 'vue'
import { ElMessage } from 'element-plus'
import * as PlatformService from '../../bindings/github.com/dongguo4812/My-LiveHub/app/internal/desktop/platformservice.js'
import { danmakuDisplaySettings, danmakuDisplayError, danmakuDisplayBusy, danmakuWindowState, loadDanmakuDisplaySettings, saveDanmakuDisplaySettings, loadDanmakuWindowState, openDanmakuWindow, closeDanmakuWindow, setDanmakuWindowPinned } from '../services/danmakuDisplay'

const form = reactive({
  font_size: 24, display_duration_seconds: 8,
  show_moderator_badge: true, show_avatar: true, show_noble_badge: true,
  show_fan_medal: true, show_user_level: true, show_gift_image: true,
  show_platform_badge: true, highlight_gifts: true, gift_highlight_minutes: 3,
  gift_merge_seconds: 5, duplicate_danmaku_filter_seconds: 3,
  show_free_gifts: true, show_danmaku: true, show_likes: true,
  show_follows: true, show_welcome: true, show_share: true, show_gifts: true,
  show_super_chat: true, show_guard: true, custom_css: '',
})
watch(danmakuDisplaySettings, value => { if (value) Object.assign(form, value) }, { immediate: true })
onMounted(() => { void loadDanmakuDisplaySettings(); void loadDanmakuWindowState() })

async function toggleOverlay() {
  const wasOpen = danmakuWindowState.value.is_open
  const succeeded = wasOpen ? await closeDanmakuWindow() : await openDanmakuWindow()
  if (succeeded) ElMessage.success(wasOpen ? '弹幕展示窗口已关闭' : '弹幕展示窗口已打开')
}
async function togglePinned(value: string | number | boolean) {
  const pinned = Boolean(value)
  if (await setDanmakuWindowPinned(pinned)) {
    ElMessage.success(pinned ? '已置顶并开启鼠标穿透' : '已取消置顶，可拖动和调整窗口大小')
  }
}
async function openSimulator() {
  try {
    await PlatformService.OpenSimulatorWindow('bilibili')
  } catch (error) {
    ElMessage.error(error instanceof Error ? error.message : String(error))
  }
}
async function save() {
  if (await saveDanmakuDisplaySettings({ ...form })) ElMessage.success('弹幕显示设置已保存')
}
</script>

<template>
  <section class="danmaku-page">
    <div class="page-heading"><div><p class="eyebrow">DANMAKU DISPLAY</p><h2>弹幕显示</h2><p class="description">配置桌面弹幕展示内容、礼物处理规则和自定义样式。设置保存在本地。</p></div><el-button type="primary" :loading="danmakuDisplayBusy" @click="save">保存设置</el-button></div>
    <el-card shadow="never" class="window-card">
      <template #header><div class="window-card-header"><strong>独立展示窗口</strong><el-tag :type="danmakuWindowState.is_open ? 'success' : 'info'" effect="plain">{{ danmakuWindowState.is_open ? '已打开' : '未打开' }}</el-tag></div></template>
      <div class="window-controls">
        <div class="window-description">
          <p>{{ danmakuWindowState.is_pinned ? '窗口已置顶，鼠标操作会穿透到下层游戏。' : '取消置顶时可直接拖动窗口，并通过右下角调整大小。' }}</p>
        </div>
        <div class="window-actions">
          <el-button type="primary" @click="toggleOverlay">{{ danmakuWindowState.is_open ? '关闭展示窗口' : '打开展示窗口' }}</el-button>
          <el-button @click="openSimulator">事件模拟</el-button>
          <el-switch
            :model-value="danmakuWindowState.is_pinned"
            :disabled="!danmakuWindowState.is_open"
            active-text="置顶并鼠标穿透"
            inactive-text="允许交互"
            @change="togglePinned"
          />
        </div>
      </div>
    </el-card>
    <el-alert v-if="danmakuDisplayError" :title="danmakuDisplayError" type="error" :closable="false" show-icon />
    <el-card shadow="never"><template #header><strong>基础显示</strong></template>
      <div class="setting-grid">
        <div class="setting-item"><div><strong>文字大小</strong><p>弹幕内容字号，12–64 px。</p></div><el-input-number v-model="form.font_size" :min="12" :max="64" /></div>
        <div class="setting-item"><div><strong>弹幕显示时间</strong><p>每条消息在展示窗口停留的时间。</p></div><el-input-number v-model="form.display_duration_seconds" :min="1" :max="60" /><span class="unit">秒</span></div>
      </div>
      <el-divider />
      <div class="switch-grid">
        <el-switch v-model="form.show_moderator_badge" active-text="房管标识" />
        <el-switch v-model="form.show_avatar" active-text="用户头像" />
        <el-switch v-model="form.show_noble_badge" active-text="贵族牌子" />
        <el-switch v-model="form.show_fan_medal" active-text="粉丝勋章" />
        <el-switch v-model="form.show_user_level" active-text="用户等级" />
        <el-switch v-model="form.show_gift_image" active-text="礼物图片" />
        <el-switch v-model="form.show_platform_badge" active-text="平台标识" />
      </div>
    </el-card>
    <el-card shadow="never"><template #header><strong>礼物处理</strong></template>
      <div class="switch-grid"><el-switch v-model="form.show_gifts" active-text="显示礼物消息" /><el-switch v-model="form.show_free_gifts" active-text="显示免费礼物" /><el-switch v-model="form.highlight_gifts" active-text="礼物高亮显示" /></div>
      <el-divider />
      <div class="setting-grid">
        <div class="setting-item"><div><strong>高亮持续时间</strong><p>礼物消息保持高亮的时间。</p></div><el-input-number v-model="form.gift_highlight_minutes" :min="1" :max="30" /><span class="unit">分钟</span></div>
        <div class="setting-item"><div><strong>礼物合并时间</strong><p>同一用户同一礼物在时间范围内合并数量；0 表示不合并。</p></div><el-input-number v-model="form.gift_merge_seconds" :min="0" :max="120" /><span class="unit">秒</span></div>
        <div class="setting-item"><div><strong>重复弹幕过滤</strong><p>同一用户重复发送相同内容时的过滤时间；0 表示不过滤。</p></div><el-input-number v-model="form.duplicate_danmaku_filter_seconds" :min="0" :max="60" /><span class="unit">秒</span></div>
      </div>
    </el-card>
    <el-card shadow="never"><template #header><strong>消息类型</strong></template>
      <div class="switch-grid">
        <el-switch v-model="form.show_danmaku" active-text="普通弹幕" />
        <el-switch v-model="form.show_likes" active-text="点赞消息" />
        <el-switch v-model="form.show_follows" active-text="关注消息" />
        <el-switch v-model="form.show_welcome" active-text="欢迎用户进场" />
        <el-switch v-model="form.show_share" active-text="分享消息" />
        <el-switch v-model="form.show_super_chat" active-text="SC 醒目留言" />
        <el-switch v-model="form.show_guard" active-text="上舰消息" />
      </div>
    </el-card>
    <el-card shadow="never"><template #header><strong>自定义 CSS</strong></template>
      <p class="css-hint">CSS 仅作用于弹幕展示窗口，不会影响客户端其他页面。可使用 .danmaku-content、.danmaku-username、.gift-highlight 等类名。</p>
      <el-input v-model="form.custom_css" type="textarea" :rows="12" resize="vertical" placeholder=".danmaku-content {&#10;  font-weight: 600;&#10;} " />
    </el-card>
  </section>
</template>

<style scoped>
.danmaku-page{display:flex;flex-direction:column;gap:18px}.page-heading{display:flex;justify-content:space-between;align-items:flex-start;gap:20px}.page-heading h2{margin:0;font-size:25px}.eyebrow{margin:0 0 8px;color:var(--app-accent);font-size:11px;letter-spacing:.15em}.description,.setting-item p,.css-hint{color:var(--el-text-color-secondary);font-size:13px}.window-card-header{display:flex;align-items:center;justify-content:space-between;width:100%}.window-controls{display:flex;align-items:center;justify-content:space-between;gap:20px}.window-description{min-width:0}.window-description p{margin:6px 0 0;color:var(--el-text-color-secondary);font-size:13px}.window-actions{display:flex;align-items:center;gap:12px;flex-wrap:wrap}.setting-grid{display:grid;grid-template-columns:repeat(2,minmax(0,1fr));gap:22px}.setting-item{display:flex;align-items:center;gap:10px;min-width:0}.setting-item>div{flex:1;min-width:0}.setting-item p{margin:6px 0 0}.unit{color:var(--el-text-color-secondary);font-size:13px}.switch-grid{display:grid;grid-template-columns:repeat(3,minmax(0,1fr));gap:20px 16px}.css-hint{margin:0 0 12px}@media(max-width:900px){.window-controls{align-items:flex-start;flex-direction:column}.setting-grid{grid-template-columns:1fr}.switch-grid{grid-template-columns:repeat(2,minmax(0,1fr))}}@media(max-width:560px){.switch-grid{grid-template-columns:1fr}.page-heading{flex-direction:column}}
</style>
