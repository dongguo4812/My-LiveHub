<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { ElMessage } from 'element-plus'
import * as PlatformService from '../../bindings/github.com/dongguo4812/My-LiveHub/app/internal/desktop/platformservice.js'

type EventItem = Awaited<ReturnType<typeof PlatformService.GetBilibiliEvents>>[number]
type EventType = 'danmaku' | 'gift' | 'super_chat' | 'guard' | 'follow' | 'like'

const eventTypes: { label: string; value: EventType }[] = [
  { label: '弹幕', value: 'danmaku' },
  { label: '礼物', value: 'gift' },
  { label: 'SC 醒目留言', value: 'super_chat' },
  { label: '上舰', value: 'guard' },
  { label: '关注', value: 'follow' },
  { label: '点赞', value: 'like' },
]
const eventLabels = Object.fromEntries(eventTypes.map(item => [item.value, item.label])) as Record<EventType, string>
const eventType = ref<EventType>('danmaku')
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

async function refreshEvents() {
  try {
    events.value = await PlatformService.GetBilibiliEvents()
    error.value = ''
  } catch (cause) {
    error.value = cause instanceof Error ? cause.message : String(cause)
  }
}

async function sendEvent() {
  busy.value = true
  error.value = ''
  try {
    await PlatformService.SimulateBilibiliEvent(
      eventType.value,
      userID.value,
      userName.value,
      content.value,
      giftName.value,
      giftCount.value,
      unitPrice.value,
      amount.value,
      duration.value,
      guardLevel.value,
      months.value,
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

onMounted(() => {
  void refreshEvents()
  refreshTimer = setInterval(() => void refreshEvents(), 1000)
})
onUnmounted(() => {
  if (refreshTimer) clearInterval(refreshTimer)
})
</script>

<template>
  <main class="simulator-page">
    <header class="simulator-heading">
      <div>
        <p class="eyebrow">PLATFORM EVENT TESTER</p>
        <h1>B 站互动模拟器</h1>
        <p class="subtitle">生成本地模拟事件，验证 My-LiveHub 的事件接收与后续处理。无需登录或连接真实直播间。</p>
      </div>
      <el-tag type="warning" effect="plain">仅本地模拟</el-tag>
    </header>

    <el-alert
      title="模拟事件不会发送到 B 站，也不会执行真实平台操作；所有事件都会带有“模拟”标记。"
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

            <el-form-item v-if="eventType === 'danmaku'" label="弹幕内容">
              <el-input v-model="content" type="textarea" :rows="3" maxlength="500" show-word-limit />
            </el-form-item>

            <template v-else-if="eventType === 'gift'">
              <el-form-item label="礼物名称">
                <el-input v-model="giftName" maxlength="40" />
              </el-form-item>
              <div class="field-grid">
                <el-form-item label="赠送数量">
                  <el-input-number v-model="giftCount" :min="1" :max="100000" class="full-width" />
                </el-form-item>
                <el-form-item label="礼物单价（元）">
                  <el-input-number v-model="unitPrice" :min="0" :precision="2" :step="0.1" class="full-width" />
                </el-form-item>
              </div>
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
              </div>
            </template>

            <div v-else class="simple-event-hint">
              {{ eventType === 'follow' ? '发送一条用户关注直播间的模拟事件。' : '发送一条用户点赞直播间的模拟事件。' }}
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
                <span v-if="row.type === 'super_chat'">¥{{ row.amount }} · {{ row.content }}</span>
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
.simulator-page{display:flex;flex-direction:column;gap:18px;padding:26px 28px;min-height:100vh;background:var(--app-bg-page);color:var(--app-text-primary)}
.simulator-heading{display:flex;align-items:flex-start;justify-content:space-between;gap:20px}
.eyebrow{margin:0 0 8px;color:var(--app-accent);font-size:10px;letter-spacing:.16em}
h1{margin:0;font-size:24px;font-weight:650}
.subtitle{margin:9px 0 0;color:var(--app-text-secondary);font-size:13px}
.content-grid{margin-top:0}
.card-heading,.history-actions{display:flex;align-items:center;justify-content:space-between;gap:10px}
.field-grid{display:grid;grid-template-columns:1fr 1fr;gap:12px}
.full-width{width:100%}
.send-button{width:100%;margin-top:8px}
.simple-event-hint{padding:12px 14px;border-radius:7px;background:var(--app-bg-control);color:var(--app-text-secondary);font-size:13px}
.form-error{margin-top:12px}
.history-card{min-height:100%}
@media(max-width:700px){.simulator-page{padding:18px}.field-grid{grid-template-columns:1fr}.simulator-heading{flex-direction:column}}
</style>
