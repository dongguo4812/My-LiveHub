<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { Refresh } from '@element-plus/icons-vue'
import { connectPlatform, disconnectPlatform, platformActionError, platforms, bilibiliEvents, platformsError, refreshPlatforms, refreshBilibiliEvents } from '../services/platforms'
import { bilibiliAccount, bilibiliAuthBusy, bilibiliAuthError, bilibiliCookieBusy, bilibiliLoginSession, bilibiliLoginStatus, logoutBilibili, pollBilibiliLogin, refreshBilibiliAccount, startBilibiliLogin, loginBilibiliWithCookie } from '../services/bilibiliAuth'
import { bilibiliSettings, bilibiliSettingsBusy, bilibiliSettingsError, loadBilibiliSettings, saveBilibiliRoomID } from '../services/bilibiliSettings'

const busyPlatformID = ref('')
const browserCookie = ref('')
const roomID = ref(0)
const avatarLoadFailed = ref(false)
const avatarSrc = computed(() => {
  const value = String(bilibiliAccount.value?.avatar ?? '').trim()
  if (!value) return ''
  if (value.startsWith('//')) return `https:${value}`
  if (value.startsWith('http://')) return value.replace(/^http:/, 'https:')
  return value
})
watch(avatarSrc, () => { avatarLoadFailed.value = false })
const statusLabels: Record<string, string> = { disconnected:'未连接', connecting:'连接中', connected:'已连接', disconnecting:'断开中', failed:'连接失败' }
const statusTypes: Record<string, 'info'|'warning'|'success'|'danger'> = { disconnected:'info', connecting:'warning', connected:'success', disconnecting:'warning', failed:'danger' }
let pollTimer: ReturnType<typeof setInterval> | undefined
let eventTimer: ReturnType<typeof setInterval> | undefined
const bilibiliConnected = computed(() => platforms.value.some(item => item.platform_id === 'bilibili' && item.status !== 'disconnected'))

async function submitBrowserCookie() {
  const ok = await loginBilibiliWithCookie(browserCookie.value)
  if (ok) browserCookie.value = ''
}
async function saveRoomID() {
  if (await saveBilibiliRoomID(roomID.value)) await loadBilibiliSettings()
}
function formatEventTime(timestamp: number) { return new Date(timestamp).toLocaleTimeString() }
async function runAction(platformID: string, action: 'connect' | 'disconnect') {
  busyPlatformID.value = platformID
  try {
    if (action === 'connect') await connectPlatform(platformID)
    else await disconnectPlatform(platformID)
  } finally {
    busyPlatformID.value = ''
    void refreshPlatforms()
  }
}
onMounted(() => {
  void refreshPlatforms()
  void refreshBilibiliAccount()
  void loadBilibiliSettings().then(() => { roomID.value = bilibiliSettings.value.room_id })
  void refreshBilibiliEvents()
  pollTimer = setInterval(() => { if (bilibiliLoginSession.value) void pollBilibiliLogin() }, 2000)
  eventTimer = setInterval(() => { void refreshPlatforms(); void refreshBilibiliEvents() }, 1500)
})
onUnmounted(() => {
  if (pollTimer) clearInterval(pollTimer)
  if (eventTimer) clearInterval(eventTimer)
})
</script>

<template>
  <section class="platform-page">
    <div class="page-heading"><div><p class="eyebrow">LIVE PLATFORM ACCESS</p><h2>平台连接</h2><p class="muted">先完成平台账号授权，再连接直播间。</p></div><el-button :icon="Refresh" @click="refreshPlatforms">刷新状态</el-button></div>
    <el-alert v-if="platformsError" :title="'平台状态读取失败：' + platformsError" type="error" :closable="false" show-icon />
    <el-alert v-if="platformActionError" :title="'连接操作失败：' + platformActionError" type="error" :closable="false" show-icon />
    <el-alert v-if="bilibiliAuthError" :title="'B站登录操作失败：' + bilibiliAuthError" type="error" :closable="false" show-icon />
    <el-alert v-if="bilibiliSettingsError" :title="'B站设置保存失败：' + bilibiliSettingsError" type="error" :closable="false" show-icon />
    <el-card shadow="never">
      <template #header><div class="card-heading"><div class="card-title"><svg class="bilibili-logo" viewBox="0 0 48 48" role="img" aria-label="哔哩哔哩"><path d="M17 10 12 4M31 10l5-6" /><path d="M11 13h26a4 4 0 0 1 4 4v17a5 5 0 0 1-5 5H12a5 5 0 0 1-5-5V17a4 4 0 0 1 4-4Z" /><path d="M18 22v7m12-7v7" /></svg><span>B 站账号</span></div><el-tag v-if="bilibiliAccount?.is_login" type="success">已登录</el-tag><el-tag v-else type="info">未登录</el-tag></div></template>
      <div v-if="bilibiliAccount?.is_login" class="account-row"><img v-if="avatarSrc && !avatarLoadFailed" class="account-avatar" :src="avatarSrc" alt="B站用户头像" referrerpolicy="no-referrer" @error="avatarLoadFailed = true" /><div v-else class="account-avatar account-avatar-fallback" aria-label="头像暂不可用">B</div><div class="account-info"><strong>{{ bilibiliAccount.name }}</strong><span>UID：{{ bilibiliAccount.uid }}</span></div><el-button type="danger" plain :loading="bilibiliAuthBusy" :disabled="bilibiliConnected" @click="logoutBilibili">退出登录</el-button></div>
      <div v-else class="login-area">
        <div v-if="bilibiliLoginSession" class="qr-panel"><img :src="bilibiliLoginSession.qr_code_data_url" alt="B站登录二维码" /><div><strong>{{ bilibiliLoginStatus?.message || '请使用哔哩哔哩手机客户端扫码' }}</strong><p>二维码有效期约 3 分钟，过期后请重新生成。</p><el-button v-if="bilibiliLoginStatus?.status === 'expired'" type="primary" @click="startBilibiliLogin">重新生成二维码</el-button></div></div>
        <div v-else class="login-prompt"><p>连接 B 站直播间前，需要先完成账号登录。</p><el-button type="primary" :loading="bilibiliAuthBusy" @click="startBilibiliLogin">扫码登录 B 站</el-button></div>
        <el-divider>或使用浏览器 Cookie 登录</el-divider>
        <div class="cookie-login"><el-input v-model="browserCookie" type="password" show-password autocomplete="off" placeholder="粘贴浏览器中 bilibili.com 的 Cookie 请求头（包含 SESSDATA）" /><div class="cookie-actions"><span>仅提取登录验证所需字段；验证成功后使用 Windows DPAPI 加密保存。</span><el-button type="primary" :loading="bilibiliCookieBusy" :disabled="!browserCookie.trim()" @click="submitBrowserCookie">使用 Cookie 登录</el-button></div></div>
      </div>
      <el-divider />
      <div class="card-heading room-heading"><span>B 站直播间</span><el-tag size="small" effect="plain">{{ bilibiliSettings.room_id ? '房间号：' + bilibiliSettings.room_id : '尚未设置房间号' }}</el-tag></div>
      <div class="room-settings"><div><strong>直播间房间号</strong><p>填写直播间 URL 中的房间号，设置保存在本地配置。</p></div><div class="room-controls"><el-input-number v-model="roomID" :min="1" :max="2147483647" :disabled="bilibiliConnected" controls-position="right" /><el-button type="primary" :loading="bilibiliSettingsBusy" :disabled="bilibiliConnected || roomID === bilibiliSettings.room_id" @click="saveRoomID">保存房间号</el-button></div></div>
      <p v-if="bilibiliConnected" class="hint">断开直播间连接后才能修改房间号或退出账号。</p>
    </el-card>
    <el-card shadow="never" class="planned-platform-card">
      <template #header><div class="card-heading"><div class="card-title"><svg class="douyin-logo" viewBox="0 0 48 48" role="img" aria-label="抖音"><path class="douyin-cyan" d="M29 7v22.5a8 8 0 1 1-7-7.95V14.5c4.2 4.2 8.5 6.3 13 6.5V13c-2.7-.5-4.8-2.5-6-6Z" /><path class="douyin-red" d="M32 7v22.5a8 8 0 1 1-7-7.95V14.5c4.2 4.2 8.5 6.3 13 6.5V13c-2.7-.5-4.8-2.5-6-6Z" /><path class="douyin-main" d="M30.5 7v22.5a8 8 0 1 1-7-7.95V14.5c4.2 4.2 8.5 6.3 13 6.5V13c-2.7-.5-4.8-2.5-6-6Z" /></svg><span>抖音账号</span></div><el-tag type="info" effect="plain">暂未接入</el-tag></div></template>
      <div class="planned-platform-content"><span>抖音直播平台</span><p>已预留平台入口，登录、直播间连接及弹幕功能将在后续版本开发。</p></div>
    </el-card>
    <el-card shadow="never">
      <template #header><div class="card-heading"><span>平台连接状态</span><el-tag size="small" effect="plain">{{ platforms.length }} 个适配器</el-tag></div></template>
      <el-empty v-if="platforms.length === 0" description="尚未接入直播平台适配器" :image-size="100"><template #default><p class="empty-note">平台连接管理服务已就绪。</p></template></el-empty>
      <el-table v-else :data="platforms" row-key="platform_id">
        <el-table-column label="平台" min-width="130"><template #default="{ row }">{{ row.platform_id === 'bilibili' ? '哔哩哔哩' : row.platform_id }}</template></el-table-column>
        <el-table-column label="连接状态" width="140"><template #default="{ row }"><el-tag :type="statusTypes[row.status] ?? 'info'" effect="light">{{ statusLabels[row.status] ?? row.status }}</el-tag></template></el-table-column>
        <el-table-column prop="error" label="错误信息" min-width="220" show-overflow-tooltip />
        <el-table-column label="操作" width="180" fixed="right"><template #default="{ row }"><el-button v-if="row.status === 'disconnected' || row.status === 'failed'" type="primary" plain size="small" :loading="busyPlatformID === row.platform_id" :disabled="!!busyPlatformID" @click="runAction(row.platform_id, 'connect')">连接</el-button><el-button v-else-if="row.status === 'connected'" type="danger" plain size="small" :loading="busyPlatformID === row.platform_id" :disabled="!!busyPlatformID" @click="runAction(row.platform_id, 'disconnect')">断开连接</el-button><el-button v-else size="small" disabled>处理中</el-button></template></el-table-column>
      </el-table>
    </el-card>
    <el-card shadow="never">
      <template #header><div class="card-heading"><span>实时弹幕</span><el-tag size="small" effect="plain">{{ bilibiliEvents.length }} 条缓存</el-tag></div></template>
      <el-empty v-if="!bilibiliEvents.length" description="连接直播间后，收到的弹幕会显示在这里" :image-size="80" />
      <el-table v-else :data="[...bilibiliEvents].reverse()" row-key="id" max-height="420"><el-table-column label="时间" width="105"><template #default="{ row }">{{ formatEventTime(row.timestamp) }}</template></el-table-column><el-table-column label="用户" width="160"><template #default="{ row }">{{ row.user_name }}</template></el-table-column><el-table-column prop="content" label="弹幕内容" min-width="260" show-overflow-tooltip /></el-table>
    </el-card>
  </section>
</template>

<style scoped>
.platform-page{display:flex;flex-direction:column;gap:20px}.page-heading{display:flex;justify-content:space-between;align-items:flex-start;gap:20px}.eyebrow{margin:0 0 9px;color:var(--el-color-primary);font-size:11px;letter-spacing:.16em}h2{margin:0;font-size:25px;font-weight:650}.muted{margin:9px 0 0;color:var(--el-text-color-secondary)}.card-heading{display:flex;align-items:center;justify-content:space-between;font-weight:600}.card-title{display:flex;align-items:center;gap:10px}.bilibili-logo{width:28px;height:28px;fill:none;stroke:#00A1D6;stroke-width:3;stroke-linecap:round;stroke-linejoin:round;flex:none}.douyin-logo{width:28px;height:28px;flex:none;fill:none;stroke-width:3.2;stroke-linecap:round;stroke-linejoin:round}.douyin-cyan{stroke:#25F4EE;transform:translate(-1.2px,0)}.douyin-red{stroke:#FE2C55;transform:translate(1.2px,0)}.douyin-main{stroke:var(--el-text-color-primary)}.planned-platform-content{display:flex;flex-direction:column;gap:6px}.planned-platform-content span{font-weight:600}.planned-platform-content p{margin:0;color:var(--el-text-color-secondary);font-size:13px}.empty-note{color:var(--el-text-color-secondary);font-size:13px}.account-row{display:flex;align-items:center;gap:16px}.account-avatar{width:52px;height:52px;flex:none;border-radius:50%;object-fit:cover;background:var(--el-fill-color);border:1px solid var(--el-border-color)}.account-avatar-fallback{display:flex;align-items:center;justify-content:center;color:var(--el-text-color-secondary);font-size:20px;font-weight:700}.account-info{display:flex;flex:1;flex-direction:column;gap:6px}.account-info span,.login-prompt p,.qr-panel p,.room-settings p,.hint{color:var(--el-text-color-secondary);font-size:13px}.login-prompt{display:flex;align-items:center;justify-content:space-between;gap:20px}.qr-panel{display:flex;align-items:center;gap:24px}.qr-panel img{width:180px;height:180px}.qr-panel p{margin:8px 0 0}.cookie-login{display:flex;flex-direction:column;gap:10px}.cookie-actions{display:flex;align-items:center;justify-content:space-between;gap:16px}.cookie-actions span{color:var(--el-text-color-secondary);font-size:12px}.room-heading{margin-bottom:16px}.room-settings{display:flex;align-items:center;justify-content:space-between;gap:20px}.room-settings p{margin:7px 0 0}.room-controls{display:flex;align-items:center;gap:12px}.hint{margin:14px 0 0}
</style>
