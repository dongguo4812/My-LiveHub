<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import * as PlatformService from '../../bindings/github.com/dongguo4812/My-LiveHub/app/internal/desktop/platformservice.js'

type EventItem = Awaited<ReturnType<typeof PlatformService.GetEvents>>[number]
interface UserProfile {
  key: string
  platformID: string
  userID: number
  name: string
  avatar: string
  latestAt: number
  eventCount: number
  danmakuCount: number
  giftCount: number
  giftAmount: number
  scAmount: number
  guardCount: number
  events: EventItem[]
}
const events = ref<EventItem[]>([])
const keyword = ref('')
const platformFilter = ref('')
const sortBy = ref<'recent' | 'events' | 'spend'>('recent')
const selectedUser = ref<UserProfile | null>(null)
const detailVisible = ref(false)
const loading = ref(false)
const error = ref('')
let timer: ReturnType<typeof setInterval> | undefined
let pending = false

const profiles = computed<UserProfile[]>(() => {
  const map = new Map<string, UserProfile>()
  for (const event of events.value) {
    const key = `${event.platform_id}::${event.user_id}`
    let profile = map.get(key)
    if (!profile) {
      profile = {
        key, platformID: event.platform_id, userID: event.user_id,
        name: event.user_name || '未知用户', avatar: event.avatar || '',
        latestAt: Number(event.timestamp) || 0, eventCount: 0,
        danmakuCount: 0, giftCount: 0, giftAmount: 0, scAmount: 0,
        guardCount: 0, events: [],
      }
      map.set(key, profile)
    }
    profile.events.push(event)
    profile.eventCount++
    if (Number(event.timestamp) >= profile.latestAt) {
      profile.latestAt = Number(event.timestamp) || profile.latestAt
      if (event.user_name) profile.name = event.user_name
      if (event.avatar) profile.avatar = event.avatar
    }
    if (event.type === 'danmaku') profile.danmakuCount++
    if (event.type === 'gift') {
      profile.giftCount += Number(event.gift_count || 1)
      profile.giftAmount += Number(event.total_price || 0)
    }
    if (event.type === 'super_chat') profile.scAmount += Number(event.amount || 0)
    if (event.type === 'guard') { profile.guardCount++; profile.scAmount += 0 }
  }
  return [...map.values()].map(profile => ({
    ...profile,
    events: profile.events.sort((a, b) => Number(b.timestamp) - Number(a.timestamp)),
  }))
})
const platformOptions = computed(() => [...new Set(profiles.value.map(item => item.platformID))].sort())
const filteredProfiles = computed(() => {
  const query = keyword.value.trim().toLocaleLowerCase()
  const list = profiles.value.filter(item =>
    (!platformFilter.value || item.platformID === platformFilter.value) &&
    (!query || [item.name, String(item.userID), item.platformID].some(value => value.toLocaleLowerCase().includes(query))),
  )
  return list.sort((a, b) => sortBy.value === 'events'
    ? b.eventCount - a.eventCount
    : sortBy.value === 'spend'
      ? (b.giftAmount + b.scAmount) - (a.giftAmount + a.scAmount)
      : b.latestAt - a.latestAt)
})
const totalSpend = computed(() => profiles.value.reduce((sum, item) => sum + item.giftAmount + item.scAmount, 0))
const eventLabels: Record<string, string> = {
  danmaku: '弹幕', gift: '礼物', super_chat: '醒目留言', guard: '舰队',
  follow: '关注', like: '点赞', welcome: '进入直播间', share: '分享',
}
function formatTime(value: number) {
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? '—' : date.toLocaleString()
}
function eventText(event: EventItem) {
  if (event.type === 'gift') return `${event.gift_name || '礼物'} × ${event.gift_count || 1} · ¥${Number(event.total_price || 0).toFixed(2)}`
  if (event.type === 'super_chat') return `¥${Number(event.amount || 0).toFixed(2)} · ${event.content}`
  if (event.type === 'guard') return `${event.content} · ¥${Number(event.amount || 0).toFixed(2)}`
  return event.content || '—'
}
function openProfile(profile: UserProfile) {
  selectedUser.value = profile
  detailVisible.value = true
}
async function refresh(showLoading = false) {
  if (pending) return
  pending = true
  if (showLoading) loading.value = true
  try {
    events.value = await PlatformService.GetEvents()
    error.value = ''
    if (selectedUser.value) {
      const updated = profiles.value.find(item => item.key === selectedUser.value?.key)
      if (updated) selectedUser.value = updated
    }
  } catch (cause) {
    error.value = cause instanceof Error ? cause.message : String(cause)
  } finally {
    pending = false
    loading.value = false
  }
}
onMounted(() => {
  void refresh(true)
  timer = setInterval(() => void refresh(), 2000)
})
onUnmounted(() => { if (timer) clearInterval(timer) })
</script>

<template>
  <section class="users-page">
    <header class="page-heading">
      <div>
        <p class="eyebrow">LIVE AUDIENCE PROFILES</p>
        <h2>用户管理</h2>
        <p class="muted">根据近期互动事件整理平台用户档案与互动记录。不同平台的 UID 独立统计，不会擅自合并为同一身份。</p>
      </div>
      <el-button :loading="loading" @click="refresh(true)">立即刷新</el-button>
    </header>

    <el-alert v-if="error" :title="`用户数据读取失败：${error}`" type="error" :closable="false" show-icon />
    <el-alert title="当前用户档案由最近 200 条事件实时汇总，数据暂存在内存中；程序重启或事件被新数据挤出缓存后，历史互动统计会变化。持久化用户档案与跨平台身份绑定尚未实现。" type="info" :closable="false" show-icon />

    <div class="summary-grid">
      <el-card shadow="never"><span>用户数量</span><strong>{{ profiles.length }}</strong><small>平台 UID 独立计数</small></el-card>
      <el-card shadow="never"><span>互动事件</span><strong>{{ events.length }}</strong><small>当前事件缓存</small></el-card>
      <el-card shadow="never"><span>涉及平台</span><strong>{{ platformOptions.length }}</strong><small>有事件记录的平台</small></el-card>
      <el-card shadow="never"><span>礼物与 SC 金额</span><strong>¥{{ totalSpend.toFixed(2) }}</strong><small>仅统计当前缓存中可识别的金额</small></el-card>
    </div>

    <el-card shadow="never">
      <div class="filters">
        <el-input v-model="keyword" placeholder="搜索昵称、UID 或平台" clearable />
        <el-select v-model="platformFilter" placeholder="全部平台" clearable>
          <el-option v-for="platform in platformOptions" :key="platform" :label="platform" :value="platform" />
        </el-select>
        <el-select v-model="sortBy">
          <el-option label="最近互动优先" value="recent" />
          <el-option label="互动次数最多" value="events" />
          <el-option label="礼物与 SC 金额最多" value="spend" />
        </el-select>
      </div>
      <div class="table-meta">找到 {{ filteredProfiles.length }} 位用户</div>
      <el-table :data="filteredProfiles" v-loading="loading" row-key="key" max-height="620" empty-text="暂无用户记录；收到直播间互动事件后会自动建立档案">
        <el-table-column label="用户" min-width="190">
          <template #default="{ row }">
            <div class="user-cell">
              <el-avatar :src="row.avatar || undefined" :size="36">{{ (row.name || '?').slice(0, 1) }}</el-avatar>
              <div><strong>{{ row.name }}</strong><small>{{ row.platformID }} · UID {{ row.userID }}</small></div>
            </div>
          </template>
        </el-table-column>
        <el-table-column prop="eventCount" label="互动次数" width="100" sortable />
        <el-table-column prop="danmakuCount" label="弹幕" width="80" />
        <el-table-column label="礼物数量" width="100"><template #default="{ row }">{{ row.giftCount }}</template></el-table-column>
        <el-table-column label="礼物金额" width="115"><template #default="{ row }">¥{{ row.giftAmount.toFixed(2) }}</template></el-table-column>
        <el-table-column label="SC 金额" width="110"><template #default="{ row }">¥{{ row.scAmount.toFixed(2) }}</template></el-table-column>
        <el-table-column label="最近互动" min-width="165"><template #default="{ row }">{{ formatTime(row.latestAt) }}</template></el-table-column>
        <el-table-column label="档案" width="90" fixed="right"><template #default="{ row }"><el-button link type="primary" @click="openProfile(row)">查看</el-button></template></el-table-column>
      </el-table>
    </el-card>

    <el-drawer v-model="detailVisible" title="用户档案" size="min(620px, 94vw)" destroy-on-close>
      <template v-if="selectedUser">
        <div class="profile-heading">
          <el-avatar :src="selectedUser.avatar || undefined" :size="58">{{ selectedUser.name.slice(0, 1) }}</el-avatar>
          <div><h3>{{ selectedUser.name }}</h3><p>{{ selectedUser.platformID }} · UID {{ selectedUser.userID }}</p></div>
        </div>
        <div class="profile-stats">
          <div><span>互动次数</span><strong>{{ selectedUser.eventCount }}</strong></div>
          <div><span>弹幕</span><strong>{{ selectedUser.danmakuCount }}</strong></div>
          <div><span>礼物数量</span><strong>{{ selectedUser.giftCount }}</strong></div>
          <div><span>舰队事件</span><strong>{{ selectedUser.guardCount }}</strong></div>
          <div><span>礼物金额</span><strong>¥{{ selectedUser.giftAmount.toFixed(2) }}</strong></div>
          <div><span>SC 金额</span><strong>¥{{ selectedUser.scAmount.toFixed(2) }}</strong></div>
        </div>
        <h4>近期互动记录</h4>
        <el-timeline>
          <el-timeline-item v-for="(event, index) in selectedUser.events" :key="event.id || index" :timestamp="formatTime(event.timestamp)" placement="top">
            <div class="history-item"><el-tag size="small" effect="plain">{{ eventLabels[event.type] || event.type }}</el-tag><span>{{ eventText(event) }}</span><el-tag v-if="event.is_simulation" size="small" type="warning">模拟</el-tag></div>
          </el-timeline-item>
        </el-timeline>
      </template>
    </el-drawer>
  </section>
</template>

<style scoped>
.users-page{display:flex;flex-direction:column;gap:18px;min-width:0}.page-heading{display:flex;align-items:flex-start;justify-content:space-between;gap:18px}.eyebrow{margin:0 0 8px;color:var(--el-color-primary);font-size:11px;letter-spacing:.15em}.page-heading h2{margin:0;font-size:25px}.muted{margin:8px 0 0;color:var(--el-text-color-secondary);font-size:13px;line-height:1.6}.summary-grid{display:grid;grid-template-columns:repeat(4,minmax(0,1fr));gap:12px}.summary-grid span,.summary-grid small{display:block;color:var(--el-text-color-secondary);font-size:12px}.summary-grid strong{display:block;margin:10px 0 5px;font-size:23px;font-variant-numeric:tabular-nums}.filters{display:grid;grid-template-columns:minmax(200px,1fr) 180px 200px;gap:10px;margin-bottom:14px}.table-meta{margin-bottom:10px;color:var(--el-text-color-secondary);font-size:12px}.user-cell{display:flex;align-items:center;gap:10px}.user-cell strong,.user-cell small{display:block}.user-cell small{margin-top:4px;color:var(--el-text-color-secondary);font-size:11px}.profile-heading{display:flex;align-items:center;gap:14px;margin-bottom:22px}.profile-heading h3{margin:0;font-size:20px}.profile-heading p{margin:6px 0 0;color:var(--el-text-color-secondary);font-size:12px}.profile-stats{display:grid;grid-template-columns:repeat(3,minmax(0,1fr));gap:12px;margin-bottom:26px}.profile-stats div{padding:12px;border:1px solid var(--el-border-color-lighter);border-radius:8px}.profile-stats span{display:block;color:var(--el-text-color-secondary);font-size:11px}.profile-stats strong{display:block;margin-top:7px;font-size:17px}.history-item{display:flex;align-items:center;gap:9px;flex-wrap:wrap;font-size:13px}
@media(max-width:1000px){.summary-grid{grid-template-columns:repeat(2,minmax(0,1fr))}.filters{grid-template-columns:1fr 1fr}.filters>*:first-child{grid-column:1/-1}}
@media(max-width:600px){.page-heading{flex-direction:column}.filters{grid-template-columns:1fr}.filters>*:first-child{grid-column:auto}.profile-stats{grid-template-columns:repeat(2,minmax(0,1fr))}}
</style>
