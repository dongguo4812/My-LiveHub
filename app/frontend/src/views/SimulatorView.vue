<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import { ElMessage } from 'element-plus'
import * as PlatformService from '../../bindings/github.com/dongguo4812/My-LiveHub/app/internal/desktop/platformservice.js'

type EventItem = Awaited<ReturnType<typeof PlatformService.GetEvents>>[number]
type MediaResource = Awaited<ReturnType<typeof PlatformService.GetMediaResources>>[number]
type PlatformItem = Awaited<ReturnType<typeof PlatformService.GetPlatforms>>[number]
type EventType = 'danmaku' | 'gift' | 'super_chat' | 'guard' | 'follow' | 'like' | 'welcome' | 'share'

const eventTypes: { label: string; value: EventType }[] = [
  { label: '弹幕', value: 'danmaku' },
  { label: '礼物', value: 'gift' },
  { label: 'SC 醒目留言', value: 'super_chat' },
  { label: '上舰', value: 'guard' },
  { label: '关注', value: 'follow' },
  { label: '点赞', value: 'like' },
  { label: '欢迎进场', value: 'welcome' },
  { label: '分享直播间', value: 'share' },
]
const eventLabels = Object.fromEntries(eventTypes.map(item => [item.value, item.label])) as Record<EventType, string>
const route = useRoute()
const eventType = ref<EventType>('danmaku')
const platforms = ref<PlatformItem[]>([])
const platformID = ref(String(route.params.platformID || 'bilibili'))
const mediaResources = ref<MediaResource[]>([])
const resourceKeyword = ref('')
const selectedGiftID = ref('')
const selectedEmoticonIDs = ref<string[]>([])
const selectedEmoticonNames = ref<Record<string, string>>({})
const failedResourceImages = ref(new Set<string>())
const giftImageURL = ref('')
const resourcesBusy = ref(false)
let resourceSearchTimer: ReturnType<typeof setTimeout> | undefined
let resourceRequestID = 0
const userID = ref(10001)
const userName = ref('模拟观众')
const content = ref('这是一条模拟弹幕')
const giftName = ref('小心心')
const giftCount = ref(1)
const unitPrice = ref(0.1)
const amount = ref(30)
const duration = ref(60)
const guardLevel = ref(3)
const months = ref(1)
const events = ref<EventItem[]>([])
const busy = ref(false)
const error = ref('')
let refreshTimer: ReturnType<typeof setInterval> | undefined

const currentEventLabel = computed(() => eventLabels[eventType.value])
const selectedGift = computed(() => mediaResources.value.find(item => (item.resource_id ?? '') === selectedGiftID.value))
const giftTotalPrice = computed(() => Math.round(unitPrice.value * giftCount.value * 100) / 100)
const guardMonthlyPrices: Record<number, number> = { 1: 19998, 2: 1998, 3: 198 }
const guardMonthlyPrice = computed(() => guardMonthlyPrices[guardLevel.value] ?? 0)
const guardTotalPrice = computed(() => guardMonthlyPrice.value * months.value)

function resourceID(item: MediaResource): string { return item.resource_id ?? '' }
function resourceKey(item: MediaResource): string { return `${item.source_id ?? ''}\u0000${resourceID(item)}` }
function resourceScopeLabel(item: MediaResource): string { return item.source_id === 'common' ? '公共' : (item.source_id ?? '未知范围') }
function resourceName(item: MediaResource): string { return item.name ?? '' }
function resourceImageURL(item: MediaResource): string { return item.image_url ?? '' }
function markResourceImageFailed(item: MediaResource) { failedResourceImages.value = new Set(failedResourceImages.value).add(resourceKey(item)) }
function resourceImageFailed(item: MediaResource): boolean { return failedResourceImages.value.has(resourceKey(item)) }
const currentPlatformName = computed(() => platforms.value.find(item => item.platform_id === platformID.value)?.platform_id ?? platformID.value)

async function refreshEvents() {
  try {
    events.value = (await PlatformService.GetEvents()).filter(event => event.platform_id === platformID.value)
    error.value = ''
  } catch (cause) {
    error.value = cause instanceof Error ? cause.message : String(cause)
  }
}

async function loadPlatforms() {
  try {
    platforms.value = await PlatformService.GetPlatforms()
    if (platforms.value.length && !platforms.value.some(item => item.platform_id === platformID.value)) platformID.value = platforms.value[0].platform_id
  } catch (cause) { error.value = cause instanceof Error ? cause.message : String(cause) }
}

async function loadMediaResources() {
  const requestID = ++resourceRequestID
  const kind = eventType.value === 'gift' ? 'gift' : eventType.value === 'danmaku' ? 'emoticon' : ''
  if (!kind) { mediaResources.value = []; return }
  resourcesBusy.value = true
  try {
    const items = await PlatformService.GetMediaResources(platformID.value, kind, resourceKeyword.value, 0)
    if (requestID === resourceRequestID) mediaResources.value = items
  } catch (cause) { if (requestID === resourceRequestID) error.value = cause instanceof Error ? cause.message : String(cause) }
  finally { if (requestID === resourceRequestID) resourcesBusy.value = false }
}

function selectGift(resourceID: string) {
  selectedGiftID.value = resourceID
  const item = mediaResources.value.find(resource => resource.resource_id === resourceID)
  if (item) { giftName.value = resourceName(item); giftImageURL.value = resourceImageURL(item); unitPrice.value = Number(item.unit_price ?? 0) }
}

function insertEmoticon(item: MediaResource) {
  const id = resourceKey(item)
  const name = resourceName(item)
  if (!id || !name) return
  // A text token can resolve to only one image resource. If the same
  // emoticon exists in multiple scopes, the most recently chosen scope wins.
  const nextNames = { ...selectedEmoticonNames.value }
  for (const selectedID of selectedEmoticonIDs.value) {
    if (nextNames[selectedID] === name) delete nextNames[selectedID]
  }
  selectedEmoticonIDs.value = selectedEmoticonIDs.value.filter(selectedID => nextNames[selectedID] !== undefined)
  selectedEmoticonIDs.value.push(id)
  nextNames[id] = name
  selectedEmoticonNames.value = nextNames
  content.value = content.value ? `${content.value}${name}` : name
}

async function sendEvent() {
  if (eventType.value === 'gift' && !selectedGiftID.value) { error.value = '请先从礼物目录选择礼物'; return }
  busy.value = true
  error.value = ''
  try {
    await PlatformService.SimulatePlatformEventWithMediaResources(
      platformID.value,
      eventType.value,
      userID.value,
      userName.value,
      content.value,
      giftName.value,
      giftCount.value,
      unitPrice.value,
      eventType.value === 'guard' ? guardTotalPrice.value : amount.value,
      duration.value,
      guardLevel.value,
      months.value,
      Number(selectedGiftID.value) || 0,
      giftImageURL.value,
      selectedGiftID.value,
      selectedEmoticonIDs.value,
    )
    await refreshEvents()
    ElMessage.success(`已发送模拟${currentEventLabel.value}事件`)
  } catch (cause) {
    error.value = cause instanceof Error ? cause.message : String(cause)
  } finally {
    busy.value = false
  }
}

function formatTime(timestamp: number) {
  return new Date(timestamp).toLocaleTimeString()
}

watch([platformID, eventType], () => { resourceKeyword.value = ''; selectedGiftID.value = ''; selectedEmoticonIDs.value = []; selectedEmoticonNames.value = {}; giftImageURL.value = ''; void loadMediaResources() })
watch(resourceKeyword, () => { if (resourceSearchTimer) clearTimeout(resourceSearchTimer); resourceSearchTimer = setTimeout(() => void loadMediaResources(), 250) })

onMounted(() => {
  void loadPlatforms()
  void loadMediaResources()
  void refreshEvents()
  refreshTimer = setInterval(() => void refreshEvents(), 1000)
})
onUnmounted(() => {
  if (refreshTimer) clearInterval(refreshTimer)
  if (resourceSearchTimer) clearTimeout(resourceSearchTimer)
})
</script>

<template>
  <main class="simulator-page">
    <header class="simulator-heading">
      <div>
        <p class="eyebrow">PLATFORM EVENT TESTER</p>
        <h1>{{ currentPlatformName }} 互动模拟器</h1>
        <p class="subtitle">生成本地模拟事件，验证 My-LiveHub 的事件接收与后续处理。模拟操作不会触发真实平台行为；资源目录需先从平台获取。</p>
      </div>
      <el-tag type="warning" effect="plain">仅本地模拟</el-tag>
    </header>

    <el-alert
      title="模拟事件不会发送到真实平台，也不会执行真实平台操作；所有事件都会带有“模拟”标记。"
      type="info"
      :closable="false"
      show-icon
    />

    <el-row :gutter="18" class="content-grid">
      <el-col :xs="24" :lg="10">
        <el-card shadow="never" class="form-card">
          <template #header>
            <div class="card-heading">
              <strong>模拟操作</strong>
              <el-tag size="small" effect="plain">{{ currentEventLabel }}</el-tag>
            </div>
          </template>

          <el-form label-position="top" @submit.prevent="sendEvent">
            <el-form-item label="直播平台"><el-select v-model="platformID" class="full-width"><el-option v-for="item in platforms" :key="item.platform_id" :label="item.platform_id" :value="item.platform_id" /></el-select></el-form-item>
            <el-form-item label="事件类型">
              <el-select v-model="eventType" class="full-width">
                <el-option v-for="item in eventTypes" :key="item.value" :label="item.label" :value="item.value" />
              </el-select>
            </el-form-item>

            <el-form-item label="用户 UID">
              <el-input-number v-model="userID" :min="0" :max="2147483647" class="full-width" />
            </el-form-item>
            <el-form-item label="用户昵称">
              <el-input v-model="userName" maxlength="40" />
            </el-form-item>

            <template v-if="eventType === 'danmaku'">
              <el-form-item label="弹幕内容"><el-input v-model="content" type="textarea" :rows="3" maxlength="500" show-word-limit /></el-form-item>
              <el-form-item label="插入直播间表情">
                <el-input v-model="resourceKeyword" placeholder="搜索表情名称或 ID" clearable><template #append><el-button :loading="resourcesBusy" @click="loadMediaResources">刷新</el-button></template></el-input>
                <div v-if="mediaResources.length" class="resource-picker">
                  <el-button v-for="item in mediaResources" :key="resourceKey(item)" class="emoticon-option" :type="selectedEmoticonIDs.includes(resourceKey(item)) ? 'primary' : 'default'" text @click="insertEmoticon(item)"><img v-if="!resourceImageFailed(item)" :src="resourceImageURL(item)" :alt="resourceName(item)" referrerpolicy="no-referrer" loading="lazy" @error="markResourceImageFailed(item)" /><span>{{ resourceName(item) }}</span><small class="emoticon-scope">{{ resourceScopeLabel(item) }}</small></el-button>
                </div>
                <el-empty v-else-if="!resourcesBusy" description="暂无已发现的表情；连接平台并收到表情弹幕后可自动收录" :image-size="64" />
              </el-form-item>
            </template>

            <template v-else-if="eventType === 'gift'">
              <el-form-item label="选择直播间礼物">
                <el-input v-model="resourceKeyword" placeholder="搜索礼物名称或 ID" clearable><template #append><el-button :loading="resourcesBusy" @click="loadMediaResources">刷新</el-button></template></el-input>
                <el-select v-model="selectedGiftID" class="full-width" filterable @change="selectGift">
                  <el-option v-for="item in mediaResources" :key="resourceID(item)" :label="resourceName(item)" :value="resourceID(item)">
                    <div class="gift-option"><img v-if="!resourceImageFailed(item)" :src="resourceImageURL(item)" :alt="resourceName(item)" referrerpolicy="no-referrer" loading="lazy" @error="markResourceImageFailed(item)" /><span>{{ resourceName(item) }}</span><small>#{{ resourceID(item) }}</small></div>
                  </el-option>
                </el-select>
                <div v-if="selectedGift" class="selected-gift"><img :src="resourceImageURL(selectedGift)" :alt="resourceName(selectedGift)" referrerpolicy="no-referrer" /><span>{{ resourceName(selectedGift) }}（ID：{{ resourceID(selectedGift) }}）</span></div>
                <el-empty v-else-if="!resourcesBusy && mediaResources.length === 0" description="暂无礼物目录；连接平台后会自动获取直播间礼物列表" :image-size="64" />
              </el-form-item>
              <div class="field-grid">
                <el-form-item label="赠送数量">
                  <el-input-number v-model="giftCount" :min="1" :max="100000" class="full-width" />
                </el-form-item>
                <el-form-item label="礼物单价（元）">
                  <el-input-number v-model="unitPrice" :min="0" :precision="2" :step="0.1" class="full-width" />
                </el-form-item>
              </div>
              <div class="gift-total">预计总金额：¥{{ giftTotalPrice.toFixed(2) }}</div>
            </template>

            <template v-else-if="eventType === 'super_chat'">
              <div class="field-grid">
                <el-form-item label="SC 金额（元）">
                  <el-input-number v-model="amount" :min="0.01" :precision="2" :step="0.01" class="full-width" />
                </el-form-item>
                <el-form-item label="展示时长（秒）">
                  <el-input-number v-model="duration" :min="0" :max="86400" class="full-width" />
                </el-form-item>
              </div>
              <el-form-item label="SC 留言">
                <el-input v-model="content" type="textarea" :rows="3" maxlength="500" show-word-limit />
              </el-form-item>
            </template>

            <template v-else-if="eventType === 'guard'">
              <div class="field-grid">
                <el-form-item label="舰队类型">
                  <el-select v-model="guardLevel" class="full-width">
                    <el-option label="总督" :value="1" />
                    <el-option label="提督" :value="2" />
                    <el-option label="舰长" :value="3" />
                  </el-select>
                </el-form-item>
                <el-form-item label="开通月份">
                  <el-input-number v-model="months" :min="1" :max="120" class="full-width" />
                </el-form-item>
                <el-form-item label="预计上舰金额（元）">
                  <el-input :model-value="`¥${guardTotalPrice.toLocaleString('zh-CN', { minimumFractionDigits: 2, maximumFractionDigits: 2 })}`" readonly />
                </el-form-item>
              </div>
              <p class="guard-price-hint">按标准月价自动计算：舰长 ¥198、提督 ¥1,998、总督 ¥19,998；实际平台订单可能因续费优惠等产生差异。</p>
            </template>

            <div v-else class="simple-event-hint">
              {{ eventType === 'follow' ? '发送一条用户关注直播间的模拟事件。' : eventType === 'like' ? '发送一条用户点赞直播间的模拟事件。' : eventType === 'share' ? '发送一条用户分享直播间的模拟事件。' : '发送一条用户进入直播间的欢迎事件。' }}
            </div>

            <el-alert v-if="error" :title="error" type="error" :closable="false" show-icon class="form-error" />
            <el-button type="primary" native-type="submit" :loading="busy" class="send-button">发送模拟事件</el-button>
          </el-form>
        </el-card>
      </el-col>

      <el-col :xs="24" :lg="14">
        <el-card shadow="never" class="history-card">
          <template #header>
            <div class="card-heading">
              <strong>事件记录</strong>
              <div class="history-actions">
                <el-tag size="small" effect="plain">{{ events.length }} 条缓存</el-tag>
                <el-button size="small" @click="refreshEvents">刷新</el-button>
              </div>
            </div>
          </template>
          <el-empty v-if="events.length === 0" description="还没有事件，选择左侧操作开始模拟" :image-size="100" />
          <el-table v-else :data="[...events].reverse()" row-key="id" max-height="590">
            <el-table-column label="时间" width="105">
              <template #default="{ row }">{{ formatTime(row.timestamp) }}</template>
            </el-table-column>
            <el-table-column label="事件" width="115">
              <template #default="{ row }">
                {{ eventLabels[row.type as EventType] ?? row.type }}
              </template>
            </el-table-column>
            <el-table-column label="用户" min-width="115">
              <template #default="{ row }">{{ row.user_name }}</template>
            </el-table-column>
            <el-table-column label="事件内容" min-width="190" show-overflow-tooltip>
              <template #default="{ row }">
                <span v-if="row.type === 'super_chat'">¥{{ Number(row.amount ?? 0).toFixed(2) }} · {{ row.content }}（{{ row.duration }} 秒）</span>
                <span v-else-if="row.type === 'guard'">{{ row.content }}（¥{{ Number(row.amount ?? 0).toFixed(2) }}）</span>
                <span v-else-if="row.type === 'gift'">{{ row.content }}（¥{{ row.total_price }}）</span>
                <span v-else>{{ row.content }}</span>
              </template>
            </el-table-column>
            <el-table-column label="来源" width="85">
              <template #default="{ row }">
                <el-tag v-if="row.is_simulation" type="warning" size="small">模拟</el-tag>
                <el-tag v-else type="success" size="small">真实</el-tag>
              </template>
            </el-table-column>
          </el-table>
        </el-card>
      </el-col>
    </el-row>
  </main>
</template>

<style scoped>
.simulator-page{display:flex;flex-direction:column;gap:18px;padding:26px 28px;width:100%;height:100vh;min-height:0;overflow-y:auto;overflow-x:hidden;background:var(--app-bg-page);color:var(--app-text-primary)}
.simulator-heading{display:flex;align-items:flex-start;justify-content:space-between;gap:20px}
.eyebrow{margin:0 0 8px;color:var(--app-accent);font-size:10px;letter-spacing:.16em}
h1{margin:0;font-size:24px;font-weight:650}
.subtitle{margin:9px 0 0;color:var(--app-text-secondary);font-size:13px}
.content-grid{margin-top:0}
.card-heading,.history-actions{display:flex;align-items:center;justify-content:space-between;gap:10px}
.field-grid{display:grid;grid-template-columns:1fr 1fr;gap:12px}
.full-width{width:100%}
.send-button{width:100%;margin-top:8px}
.resource-picker{display:flex;flex-wrap:wrap;gap:6px;max-height:190px;overflow:auto;margin-top:10px}.emoticon-option{display:flex;align-items:center;gap:6px;height:34px;padding:4px 7px}.emoticon-option img,.gift-option img,.selected-gift img{width:24px;height:24px;object-fit:contain}.emoticon-scope{color:var(--app-text-secondary);font-size:10px}.gift-option,.selected-gift{display:flex;align-items:center;gap:9px}.gift-option small{margin-left:auto;color:var(--app-text-secondary)}.selected-gift{margin-top:10px;color:var(--app-text-secondary);font-size:12px}.gift-total{margin-top:-8px;margin-bottom:12px;text-align:right;color:var(--app-text-secondary);font-size:13px}.guard-price-hint{margin:-4px 0 12px;color:var(--app-text-secondary);font-size:12px;line-height:1.5}.simple-event-hint{padding:12px 14px;border-radius:7px;background:var(--app-bg-control);color:var(--app-text-secondary);font-size:13px}
.form-error{margin-top:12px}
.history-card{min-height:100%}
@media(max-width:700px){.simulator-page{padding:18px}.field-grid{grid-template-columns:1fr}.simulator-heading{flex-direction:column}}
</style>
